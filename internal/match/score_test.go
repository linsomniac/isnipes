//go:build testhooks

package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// TestInitialScoreboardLives — Phase 5 §12.3. The Scoreboard sent on
// MatchJoin carries Lives = LevelParams.PlayerLives for every joined
// slot, and Score = 0.
func TestInitialScoreboardLives(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	entries := m.buildScoreboardEntries()
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(entries))
	}
	for _, e := range entries {
		// PvP-only default = 3 lives (sim.defaultPvPLives).
		if e.Lives != 3 {
			t.Errorf("p%d lives=%d, want 3", e.PlayerID, e.Lives)
		}
		if e.Score != 0 {
			t.Errorf("p%d score=%d, want 0", e.PlayerID, e.Score)
		}
	}
}

// TestInitialScoreboardLives_PvE — A1 → 9 lives.
func TestInitialScoreboardLives_PvE(t *testing.T) {
	m := newPvEMatchForEval(t)
	entries := m.buildScoreboardEntries()
	for _, e := range entries {
		if e.Lives != 9 {
			t.Errorf("p%d lives=%d, want 9 (level A1)", e.PlayerID, e.Lives)
		}
	}
}

// TestMatchOverEntriesIncludeScoreAndLives — after a player-on-player
// kill, the MatchOver entry for killer has Score=25 and victim has
// Score=-5 with lives decremented.
func TestMatchOverEntriesIncludeScoreAndLives(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	sim.ForceKillForTest(m.sim, 2, 1)
	entries := m.buildMatchOverEntries(1, proto.EndLastStanding)
	byID := map[uint32]proto.MatchOverEntry{}
	for _, e := range entries {
		byID[e.PlayerID] = e
	}
	if byID[1].Score != 25 {
		t.Errorf("killer score=%d, want 25", byID[1].Score)
	}
	if byID[2].Score != -5 {
		t.Errorf("victim score=%d, want -5", byID[2].Score)
	}
	// Victim had 3 lives, lost 1 → 2.
	if byID[2].LivesRemaining != 2 {
		t.Errorf("victim lives=%d, want 2", byID[2].LivesRemaining)
	}
	if byID[1].LivesRemaining != 3 {
		t.Errorf("killer lives=%d, want 3", byID[1].LivesRemaining)
	}
}

// TestScoreboardReflectsLivesDecrement — after one kill, the live
// scoreboard reports the new lives count for the victim.
func TestScoreboardReflectsLivesDecrement(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	sim.ForceKillForTest(m.sim, 2, 1)
	entries := m.buildScoreboardEntries()
	for _, e := range entries {
		if e.PlayerID == 2 && e.Lives != 2 {
			t.Errorf("victim lives=%d, want 2 after one kill", e.Lives)
		}
		if e.PlayerID == 1 && e.Score != 25 {
			t.Errorf("killer score=%d, want 25", e.Score)
		}
	}
}
