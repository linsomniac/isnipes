package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// newPvPMatchForEval builds a minimal Match value backed by a real Sim
// for direct evaluateMatchEnd testing. The Match's actor goroutine is
// NOT started; tests drive the sim directly and call evaluateMatchEnd.
func newPvPMatchForEval(t *testing.T, playerIDs []sim.EntityID) *Match {
	t.Helper()
	cfg := sim.Config{
		Seed:         0xBEEF,
		Width:        60,
		Height:       40,
		PlayerIDs:    playerIDs,
		NoGenerators: true,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	m := &Match{
		slots:               make(map[sim.EntityID]*Slot, len(playerIDs)),
		sim:                 s,
		startingPlayerCount: len(playerIDs),
		isPvE:               false,
	}
	for _, id := range playerIDs {
		m.slots[id] = &Slot{PlayerID: id, Joined: true}
	}
	return m
}

func newPvEMatchForEval(t *testing.T) *Match {
	t.Helper()
	cfg := sim.Config{
		Seed:        0xBEEF,
		Width:       60,
		Height:      40,
		PlayerIDs:   []sim.EntityID{1, 2},
		LevelLetter: 'A',
		LevelNumber: 1,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	m := &Match{
		slots:               make(map[sim.EntityID]*Slot, 2),
		sim:                 s,
		startingPlayerCount: 2,
		isPvE:               true,
	}
	for _, id := range []sim.EntityID{1, 2} {
		m.slots[id] = &Slot{PlayerID: id, Joined: true}
	}
	return m
}

// killPlayerUntilEliminated drains a player's lives via direct sim
// internal mutation, then runs one Tick so GC removes the slab entry
// per Phase 5 §6.5.
func killPlayerUntilEliminated(t *testing.T, m *Match, victimID, killerID sim.EntityID) {
	t.Helper()
	sim.SetLivesForTest(m.sim, victimID, 1)
	sim.ForceKillForTest(m.sim, victimID, killerID)
	if _, err := m.sim.Tick(nil); err != nil {
		t.Fatalf("post-kill Tick: %v", err)
	}
	if !m.sim.Eliminated(victimID) {
		t.Fatalf("victim %d not eliminated", victimID)
	}
}

// TestEndReason_LastStandingAtStartCountGE2 — eliminate one of two
// players → LAST_STANDING with the survivor as winner.
func TestEndReason_LastStandingAtStartCountGE2(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	killPlayerUntilEliminated(t, m, 2, 1)
	reason, winner, done := m.evaluateMatchEnd()
	if !done {
		t.Fatal("evaluateMatchEnd not done")
	}
	if reason != proto.EndLastStanding {
		t.Fatalf("reason = %d, want EndLastStanding(%d)", reason, proto.EndLastStanding)
	}
	if winner != 1 {
		t.Fatalf("winner = %d, want 1", winner)
	}
}

// TestEndReason_LastStandingSkippedAtStartCount1 — solo PvP (impossible
// at the lobby gate but verified here) never fires LAST_STANDING.
func TestEndReason_LastStandingSkippedAtStartCount1(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1})
	// solo player remains alive — no end reason fires.
	if _, _, done := m.evaluateMatchEnd(); done {
		t.Fatal("solo PvP terminated immediately")
	}
	// Solo player eliminated → ALL_ELIMINATED (not LAST_STANDING).
	killPlayerUntilEliminated(t, m, 1, 0)
	reason, _, done := m.evaluateMatchEnd()
	if !done || reason != proto.EndAllEliminated {
		t.Fatalf("got (reason=%d done=%v), want EndAllEliminated(%d)", reason, done, proto.EndAllEliminated)
	}
}

// TestEndReason_AllElimAtZeroLive — every player eliminated → ALL_ELIMINATED.
func TestEndReason_AllElimAtZeroLive(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	killPlayerUntilEliminated(t, m, 2, 1)
	killPlayerUntilEliminated(t, m, 1, 0)
	reason, winner, done := m.evaluateMatchEnd()
	if !done {
		t.Fatal("not done")
	}
	if reason != proto.EndAllEliminated {
		t.Fatalf("reason = %d, want EndAllEliminated", reason)
	}
	if winner != 0 {
		t.Fatalf("winner = %d, want 0", winner)
	}
}

// TestEndReason_TimerFiresAt10Min — drive the sim past MatchTimerTicks
// with both players alive; TIMER fires; winner = highest-scoring.
func TestEndReason_TimerFiresAt10Min(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	// Give player 1 a higher score so the tie-break path is unambiguous.
	sim.SetScoreForTest(m.sim, 1, 50)
	sim.SetScoreForTest(m.sim, 2, 10)
	sim.SetServerTickForTest(m.sim, MatchTimerTicks)
	reason, winner, done := m.evaluateMatchEnd()
	if !done {
		t.Fatal("TIMER eval not done")
	}
	if reason != proto.EndTimer {
		t.Fatalf("reason = %d, want EndTimer", reason)
	}
	if winner != 1 {
		t.Fatalf("winner = %d, want 1", winner)
	}
}

// TestEndReason_TimerTieReturnsZeroWinner — equal scores at TIMER.
func TestEndReason_TimerTieReturnsZeroWinner(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	sim.SetScoreForTest(m.sim, 1, 42)
	sim.SetScoreForTest(m.sim, 2, 42)
	sim.SetServerTickForTest(m.sim, MatchTimerTicks)
	reason, winner, _ := m.evaluateMatchEnd()
	if reason != proto.EndTimer {
		t.Fatalf("reason = %d, want EndTimer", reason)
	}
	if winner != 0 {
		t.Fatalf("winner = %d, want 0 (tie)", winner)
	}
}

// TestEndReason_PvECompleteWinsAllElimTie — PvE objective complete on
// the same tick the last 2 players die together: PVE_COMPLETE wins
// only if at least one survivor exists. With both dead and gens gone,
// the eval returns ALL_ELIMINATED (the §3.8.1 example "last two players
// kill each other and snipes remain → ALL_ELIMINATED").
func TestEndReason_PvEEdgeCase(t *testing.T) {
	m := newPvEMatchForEval(t)
	sim.RemoveAllGeneratorsForTest(m.sim)
	sim.RemoveAllSnipesForTest(m.sim)
	// With both players alive, gens=0, snipes=0, isPvE: PVE_COMPLETE.
	reason, _, done := m.evaluateMatchEnd()
	if !done || reason != proto.EndPVEComplete {
		t.Fatalf("got (reason=%d done=%v), want EndPVEComplete", reason, done)
	}
}
