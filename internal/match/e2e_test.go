//go:build testhooks

package match

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

var updateMatchOver = flag.Bool("update", false, "regenerate the phase5 matchover.json golden")

// matchOverJSON mirrors §19.3's readable fixture layout.
type matchOverJSON struct {
	FinalTick   uint32               `json:"final_tick"`
	Reason      uint8                `json:"reason"`
	WinnerIDOr0 uint32               `json:"winner_id_or_0"`
	Entries     []matchOverEntryJSON `json:"entries"`
}

type matchOverEntryJSON struct {
	PlayerID       uint32 `json:"player_id"`
	Score          int32  `json:"score"`
	LivesRemaining uint8  `json:"lives_remaining"`
}

// phase5ReplayPath / phase5MatchOverPath co-locate the match fixture
// with the sim-side .inputs/.hash under internal/sim/testdata/replays.
func phase5ReplayPath() string {
	return filepath.Join("..", "sim", "testdata", "replays", "phase5_4p_pvp.inputs")
}

func phase5MatchOverPath() string {
	return filepath.Join("..", "sim", "testdata", "replays", "phase5_4p_pvp.matchover.json")
}

// TestMatch4PlayerScoreboardE2E is Phase 5 DoD #7 / §18.9. It replays
// the committed phase5_4p_pvp.inputs through the match actor's per-tick
// pipeline (sim tick → dead-cam transition → scoreboard delta detector
// → end-reason eval) and asserts the scoreboard is driven through ≥ 5
// kill events, ≥ 1 respawn cycle, and one player (p4) reaching zero
// lives. The match is then forced to the 10-minute TIMER and the
// emitted MatchOver is compared byte-for-byte against the committed
// matchover.json fixture.
func TestMatch4PlayerScoreboardE2E(t *testing.T) {
	inputs := readPhase5Replay(t)

	cfg := sim.Config{
		Seed:         0xDEADBEEF,
		Width:        60,
		Height:       40,
		PlayerIDs:    []sim.EntityID{1, 2, 3, 4},
		LevelLetter:  'A',
		LevelNumber:  1,
		NoGenerators: true,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}

	ids := []sim.EntityID{1, 2, 3, 4}
	m := &Match{
		cfg:                 MatchConfig{MatchID: "e2e", Clock: time.Now},
		in:                  make(chan controlMsg, 256),
		sim:                 s,
		slots:               make(map[sim.EntityID]*Slot, 4),
		startingPlayerCount: 4,
		// PvP match: the A1 level table supplies 9 lives, but there are no
		// generators/snipes, so the PVE_COMPLETE rule must NOT apply.
		isPvE:         false,
		aoiPrev:       make(map[sim.EntityID]map[sim.EntityID]struct{}),
		pendingInputs: make(map[sim.EntityID]proto.Input),
		owt:           make(map[sim.EntityID]*OWTEstimator),
		dcTokens:      make(map[string]sim.EntityID),
		lastScores:    newScoreSnapshot(),
		done:          make(chan struct{}),
		clock:         time.Now,
	}
	m.stateAtomic.Store(uint32(StateLive))

	// Per-slot capture: a drainer goroutine counts Scoreboard frames and
	// retains the final MatchOver payload. Buffered + drained so tick()'s
	// sendFrameTo never sees a full queue (which would drop the slot).
	type capture struct {
		scoreboards    int
		scoreboardTick uint32 // server tick of the last Scoreboard frame
		matchOvers     int
		matchOver      []byte
		done           chan struct{}
	}
	caps := make(map[sim.EntityID]*capture, 4)
	for _, id := range ids {
		ch := make(chan OutboundFrame, 1024)
		slot := &Slot{PlayerID: id, Joined: true, out: ch, closed: make(chan struct{})}
		m.slots[id] = slot
		m.owt[id] = NewOWTEstimator()
		c := &capture{done: make(chan struct{})}
		caps[id] = c
		go func(c *capture, ch chan OutboundFrame) {
			defer close(c.done)
			for f := range ch {
				switch f.Type {
				case proto.MsgScoreboard:
					c.scoreboards++
					if sb, err := proto.DecodeScoreboard(f.Payload); err == nil {
						c.scoreboardTick = sb.ServerTick
					}
				case proto.MsgMatchOver:
					c.matchOvers++
					c.matchOver = append([]byte(nil), f.Payload...)
				}
			}
		}(c, ch)
	}

	// Drive the recorded match. Track kills (life losses), respawns
	// (a player seen dead then alive again), and the elimination tick.
	prevLives := map[sim.EntityID]uint8{1: 9, 2: 9, 3: 9, 4: 9}
	wasDead := map[sim.EntityID]bool{}
	respawns := 0
	kills := 0
	firstKillTick := uint32(0)
	for _, tick := range inputs {
		m.pendingInputs = make(map[sim.EntityID]proto.Input, len(tick))
		for _, in := range tick {
			m.pendingInputs[in.PlayerID] = proto.Input{
				ClientTick: in.ClientTick,
				Dir:        uint8(in.Dir),
				Turbo:      b2u8(in.Turbo),
				FireDir:    uint8(in.FireDir),
			}
		}
		m.tick()

		alive := map[sim.EntityID]bool{}
		for _, e := range s.Entities() {
			if e.Kind == sim.KindPlayer && e.Flags&sim.FlagDead == 0 {
				alive[e.ID] = true
			}
		}
		for _, id := range ids {
			cur := s.LivesRemaining(id)
			if cur < prevLives[id] {
				kills += int(prevLives[id] - cur)
				if firstKillTick == 0 {
					firstKillTick = s.ServerTick()
				}
			}
			prevLives[id] = cur
			if !alive[id] && !s.Eliminated(id) {
				wasDead[id] = true
			} else if alive[id] && wasDead[id] {
				respawns++
				wasDead[id] = false
			}
		}
	}

	if kills < 5 {
		t.Errorf("kills = %d, want ≥ 5", kills)
	}
	if respawns < 1 {
		t.Errorf("respawn cycles = %d, want ≥ 1", respawns)
	}
	if !s.Eliminated(4) {
		t.Errorf("player 4 not eliminated; lives=%d", s.LivesRemaining(4))
	}
	if firstKillTick == 0 {
		t.Fatal("no kill observed during replay")
	}

	// Drive the match to its natural end through the real per-tick
	// pipeline: jump to one tick short of the 10-minute hard timer, then
	// call tick() so tick()'s own evaluateMatchEnd → endMatch fires (a
	// regression that drops the TIMER check from tick() must fail here).
	sim.SetServerTickForTest(s, MatchTimerTicks-1)
	m.pendingInputs = make(map[sim.EntityID]proto.Input)
	m.tick()
	if m.State() != StateEnded {
		t.Fatalf("match did not end after TIMER tick; state=%d", m.State())
	}

	// endMatch closed every slot's out channel; wait for drainers.
	for _, c := range caps {
		<-c.done
	}

	// Every joined slot must have received exactly one MatchOver and at
	// least one kill-driven Scoreboard (proven by a Scoreboard frame at
	// or after the first kill — an initial-state broadcast fires at
	// ~tick 6, before any kill, so this rules out that being the only
	// one).
	for _, id := range ids {
		c := caps[id]
		if c.matchOvers != 1 {
			t.Errorf("slot %d received %d MatchOver frames, want exactly 1", id, c.matchOvers)
		}
		if c.matchOver == nil {
			t.Fatalf("slot %d never received MatchOver", id)
		}
		if c.scoreboardTick < firstKillTick {
			t.Errorf("slot %d last Scoreboard at tick %d, want a kill-driven frame at/after first kill (tick %d)",
				id, c.scoreboardTick, firstKillTick)
		}
	}

	mo, err := proto.DecodeMatchOver(caps[1].matchOver)
	if err != nil {
		t.Fatalf("decode MatchOver: %v", err)
	}

	if *updateMatchOver {
		// NOTE: regenerate in order — first the sim package
		// (`go test -tags testhooks -run Phase5_4PPvP -update ./internal/sim`)
		// to rewrite phase5_4p_pvp.{inputs,hash}, THEN this test to derive
		// matchover.json from the *current* inputs. Running both with
		// -update in one invocation may pin matchover.json to the previous
		// replay.
		writePhase5MatchOver(t, mo)
		return
	}
	want := readPhase5MatchOver(t)
	assertMatchOverEqual(t, want, mo)
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// readPhase5Replay parses the committed binary input stream. The layout
// matches internal/sim/sim_test.go's writeReplayInputs.
func readPhase5Replay(t *testing.T) [][]sim.PlayerInput {
	t.Helper()
	data, err := os.ReadFile(phase5ReplayPath())
	if err != nil {
		t.Fatalf("read replay inputs: %v", err)
	}
	if len(data) < 4 {
		t.Fatal("replay too short")
	}
	off := 0
	tickCount := binary.LittleEndian.Uint32(data[off:])
	off += 4
	out := make([][]sim.PlayerInput, tickCount)
	for i := uint32(0); i < tickCount; i++ {
		if off >= len(data) {
			t.Fatal("replay truncated")
		}
		n := int(data[off])
		off++
		out[i] = make([]sim.PlayerInput, n)
		for j := 0; j < n; j++ {
			pid := binary.LittleEndian.Uint32(data[off:])
			off += 4
			d := data[off]
			off++
			turbo := data[off] == 1
			off++
			fd := data[off]
			off++
			ct := binary.LittleEndian.Uint16(data[off:])
			off += 2
			out[i][j] = sim.PlayerInput{
				PlayerID:   sim.EntityID(pid),
				Dir:        sim.Dir(d),
				Turbo:      turbo,
				FireDir:    sim.Dir(fd),
				ClientTick: ct,
			}
		}
	}
	return out
}

func writePhase5MatchOver(t *testing.T, mo proto.MatchOver) {
	t.Helper()
	doc := matchOverJSON{
		FinalTick:   mo.FinalTick,
		Reason:      mo.Reason,
		WinnerIDOr0: mo.WinnerIDOr0,
	}
	for _, e := range mo.Entries {
		doc.Entries = append(doc.Entries, matchOverEntryJSON{
			PlayerID:       e.PlayerID,
			Score:          e.Score,
			LivesRemaining: e.LivesRemaining,
		})
	}
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal matchover: %v", err)
	}
	buf = append(buf, '\n')
	if err := os.WriteFile(phase5MatchOverPath(), buf, 0o644); err != nil {
		t.Fatalf("write matchover.json: %v", err)
	}
}

func readPhase5MatchOver(t *testing.T) proto.MatchOver {
	t.Helper()
	data, err := os.ReadFile(phase5MatchOverPath())
	if err != nil {
		t.Fatalf("read matchover.json: %v (run with -update)", err)
	}
	var doc matchOverJSON
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal matchover.json: %v", err)
	}
	mo := proto.MatchOver{
		FinalTick:   doc.FinalTick,
		Reason:      doc.Reason,
		WinnerIDOr0: doc.WinnerIDOr0,
	}
	for _, e := range doc.Entries {
		mo.Entries = append(mo.Entries, proto.MatchOverEntry{
			PlayerID:       e.PlayerID,
			Score:          e.Score,
			LivesRemaining: e.LivesRemaining,
		})
	}
	return mo
}

func assertMatchOverEqual(t *testing.T, want, got proto.MatchOver) {
	t.Helper()
	if want.FinalTick != got.FinalTick || want.Reason != got.Reason || want.WinnerIDOr0 != got.WinnerIDOr0 {
		t.Fatalf("MatchOver header mismatch:\n want %+v\n got  %+v", want, got)
	}
	if len(want.Entries) != len(got.Entries) {
		t.Fatalf("entry count: want %d, got %d", len(want.Entries), len(got.Entries))
	}
	for i := range want.Entries {
		if want.Entries[i] != got.Entries[i] {
			t.Fatalf("entry %d: want %+v, got %+v", i, want.Entries[i], got.Entries[i])
		}
	}
}
