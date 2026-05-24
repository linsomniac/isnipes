//go:build testhooks

package match

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// TestMatchLastStandingE2E drives a real 4-player free-for-all match to a
// natural LAST_STANDING end. It replays the committed
// phase5_last_standing.inputs (a deterministic recorded playthrough in
// which player 1 hunts down players 2–4 until each runs out of lives)
// through the match actor's full per-tick pipeline — sim tick → dead-cam
// transition → scoreboard delta detector → end-reason evaluation — and
// asserts that evaluateMatchEnd fires LAST_STANDING on its own (no forced
// timer), naming player 1 the winner.
//
// This is the multi-life counterpart to TestMatch4PlayerScoreboardE2E
// (which forces a TIMER end): here the match must END BY ITSELF the tick
// the third victim is eliminated, exercising the respawn → lives-
// exhaustion → LAST_STANDING path that startOrAbort's NoRespawn:false
// wiring enables in production.
func TestMatchLastStandingE2E(t *testing.T) {
	inputs := readReplayInputsAt(t, filepath.Join("..", "sim", "testdata", "replays", "phase5_last_standing.inputs"))

	// Free-for-all sim: Config.LevelLetter == 0 ⇒ 3 lives per player, no
	// snipes. NoGenerators mirrors what startOrAbort sets for a zero-level
	// PvP match (match.go), so this replays the production PvP map and
	// PVE_COMPLETE can never apply.
	cfg := sim.Config{
		Seed:         0xDEADBEEF,
		Width:        60,
		Height:       40,
		PlayerIDs:    []sim.EntityID{1, 2, 3, 4},
		NoGenerators: true,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}

	ids := []sim.EntityID{1, 2, 3, 4}
	m := &Match{
		cfg:                 MatchConfig{MatchID: "ls-e2e", Clock: time.Now},
		in:                  make(chan controlMsg, 256),
		sim:                 s,
		slots:               make(map[sim.EntityID]*Slot, 4),
		startingPlayerCount: 4,
		isPvE:               false,
		aoiPrev:             make(map[sim.EntityID]map[sim.EntityID]struct{}),
		pendingInputs:       make(map[sim.EntityID]proto.Input),
		owt:                 make(map[sim.EntityID]*OWTEstimator),
		dcTokens:            make(map[string]sim.EntityID),
		lastScores:          newScoreSnapshot(),
		done:                make(chan struct{}),
		clock:               time.Now,
	}
	m.stateAtomic.Store(uint32(StateLive))

	// Per-slot drainer: keep the out channel from filling (which would
	// drop the slot) and retain the final MatchOver payload.
	type capture struct {
		matchOvers int
		matchOver  []byte
		done       chan struct{}
	}
	caps := make(map[sim.EntityID]*capture, 4)
	for _, id := range ids {
		ch := make(chan OutboundFrame, 1024)
		m.slots[id] = &Slot{PlayerID: id, Joined: true, out: ch, closed: make(chan struct{})}
		m.owt[id] = NewOWTEstimator()
		c := &capture{done: make(chan struct{})}
		caps[id] = c
		go func(c *capture, ch chan OutboundFrame) {
			defer close(c.done)
			for f := range ch {
				if f.Type == proto.MsgMatchOver {
					c.matchOvers++
					c.matchOver = append([]byte(nil), f.Payload...)
				}
			}
		}(c, ch)
	}

	// Replay through the real per-tick pipeline. The match must end
	// naturally; once it does, tick() is a no-op (StateEnded), so we stop.
	ended := false
	endTick := uint32(0)
	for _, tick := range inputs {
		if m.State() == StateEnded {
			ended = true
			break
		}
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
		if m.State() == StateEnded {
			ended = true
			endTick = s.ServerTick()
			break
		}
	}

	if !ended {
		t.Fatalf("match never ended over %d recorded ticks (still %v)", len(inputs), m.State())
	}

	// endMatch closed every slot's out channel; wait for drainers.
	for _, c := range caps {
		<-c.done
	}

	// The win condition must be LAST_STANDING (not a forced timer, not
	// all-eliminated): exactly one player still has lives.
	if endTick == 0 || endTick >= MatchTimerTicks {
		t.Fatalf("match ended at tick %d; expected a natural end well before the %d-tick timer", endTick, MatchTimerTicks)
	}
	if !s.Eliminated(2) || !s.Eliminated(3) || !s.Eliminated(4) {
		t.Fatalf("expected players 2,3,4 eliminated; got elim 2=%v 3=%v 4=%v",
			s.Eliminated(2), s.Eliminated(3), s.Eliminated(4))
	}
	if s.Eliminated(1) {
		t.Fatal("survivor (player 1) was eliminated")
	}

	for _, id := range ids {
		c := caps[id]
		if c.matchOvers != 1 {
			t.Errorf("slot %d received %d MatchOver frames, want exactly 1", id, c.matchOvers)
		}
		if c.matchOver == nil {
			t.Fatalf("slot %d never received MatchOver", id)
		}
	}

	mo, err := proto.DecodeMatchOver(caps[1].matchOver)
	if err != nil {
		t.Fatalf("decode MatchOver: %v", err)
	}
	if mo.Reason != proto.EndLastStanding {
		t.Errorf("MatchOver.Reason = %d, want EndLastStanding (%d)", mo.Reason, proto.EndLastStanding)
	}
	if mo.WinnerIDOr0 != 1 {
		t.Errorf("MatchOver.WinnerIDOr0 = %d, want 1 (sole survivor)", mo.WinnerIDOr0)
	}
	if mo.FinalTick != endTick {
		t.Errorf("MatchOver.FinalTick = %d, want %d", mo.FinalTick, endTick)
	}
}
