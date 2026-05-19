//go:build testhooks

package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/sim"
)

// TestScoreDelta_DetectsLivesChange — captureScores → killEntity →
// detectScoreChange returns true.
func TestScoreDelta_DetectsLivesChange(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	m.lastScores = newScoreSnapshot()
	m.lastScores.capture(m.sim)
	if m.detectScoreChange() {
		t.Fatal("change reported with no kill yet")
	}
	sim.ForceKillForTest(m.sim, 2, 1)
	if !m.detectScoreChange() {
		t.Fatal("kill not detected by score delta")
	}
}

// TestScoreDelta_DetectsScoreChange — killer score moves +25 even
// when lives stays the same after the kill.
func TestScoreDelta_DetectsScoreChange(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	m.lastScores = newScoreSnapshot()
	m.lastScores.capture(m.sim)
	sim.SetScoreForTest(m.sim, 1, 100)
	if !m.detectScoreChange() {
		t.Fatal("score bump not detected")
	}
}

// TestScoreDelta_NoChangeNoEmit — sequential maybeBroadcastScoreboard
// calls without state change are no-ops past the first.
func TestScoreDelta_NoChangeNoEmit(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	// Prime by capturing current state.
	m.lastScores = newScoreSnapshot()
	m.lastScores.capture(m.sim)
	m.maybeBroadcastScoreboard()
	if m.pendingScoreboard {
		t.Fatal("pending flag set despite no change")
	}
}

// TestScoreDelta_DiffsKeyswapSameCardinality — codex P5/iter5
// finding: prev has id X with zero score; cur has id Y (zero score)
// instead. Same map length but different keyset must count as a
// change.
func TestScoreDelta_DiffsKeyswapSameCardinality(t *testing.T) {
	prev := newScoreSnapshot()
	prev.score[1] = 0
	prev.lives[1] = 0
	cur := newScoreSnapshot()
	cur.score[2] = 0
	cur.lives[2] = 0
	if !cur.diffs(prev) {
		t.Fatal("same-cardinality keyset swap not detected")
	}
}

// TestScoreDelta_RateLimited — burst of kills inside the 6-tick window
// emits ONE Scoreboard, not many.
func TestScoreDelta_RateLimited(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2, 3, 4})
	m.lastScores = newScoreSnapshot()
	m.lastScores.capture(m.sim)
	sim.SetServerTickForTest(m.sim, 100)

	// First burst: change + broadcast (cooldown elapsed since lastScoreboardTick=0).
	sim.ForceKillForTest(m.sim, 2, 1)
	m.maybeBroadcastScoreboard()
	if m.pendingScoreboard {
		t.Fatal("pending after broadcast")
	}
	firstTick := m.lastScoreboardTick
	if firstTick != 100 {
		t.Fatalf("lastScoreboardTick = %d, want 100", firstTick)
	}

	// Second burst at tick 101 — cooldown NOT elapsed (need >= 106).
	sim.SetServerTickForTest(m.sim, 101)
	sim.ForceKillForTest(m.sim, 3, 1)
	m.maybeBroadcastScoreboard()
	if !m.pendingScoreboard {
		t.Fatal("pending NOT set despite change-during-cooldown")
	}
	if m.lastScoreboardTick != firstTick {
		t.Fatal("broadcast fired during cooldown")
	}

	// Advance past cooldown (>= firstTick + 6) → pending flushes.
	sim.SetServerTickForTest(m.sim, 106)
	m.maybeBroadcastScoreboard()
	if m.pendingScoreboard {
		t.Fatal("pending should have flushed after cooldown")
	}
	if m.lastScoreboardTick != 106 {
		t.Fatalf("lastScoreboardTick = %d, want 106", m.lastScoreboardTick)
	}
}
