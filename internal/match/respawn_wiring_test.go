//go:build testhooks

package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/sim"
)

// TestStartOrAbortEnablesRespawn guards the Phase 5 wiring fix: a live
// match built through the real startOrAbort path must respawn a player
// who still has lives remaining, instead of removing them on first death
// (the Phase 2 NoRespawn:true behaviour). If startOrAbort regresses to
// NoRespawn:true, the entire lives / respawn / spawn-invuln / dead-cam
// lifecycle goes inert in production even though it is fully implemented.
func TestStartOrAbortEnablesRespawn(t *testing.T) {
	m, _, _, _, _ := twoPlayerSetup(t) // PvP (no level table) → 3 lives.
	m.slots[1].Joined = true
	m.slots[2].Joined = true

	m.startOrAbort()
	if m.State() != StateLive {
		t.Fatalf("state = %v, want LIVE", m.State())
	}
	s := m.simForTest()

	// Kill player 2 once. With 3 lives it must NOT be eliminated.
	sim.ForceKillForTest(s, 2, 1)
	if s.Eliminated(2) {
		t.Fatal("player 2 eliminated after a single death — match is single-life")
	}

	// Advance past the 90-tick respawn timer.
	for i := 0; i < 100; i++ {
		if _, err := s.Tick(nil); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}

	if !playerAlive(s, 2) {
		t.Fatal("player 2 never respawned — startOrAbort left NoRespawn enabled")
	}
	if got := s.LivesRemaining(2); got != 2 {
		t.Fatalf("player 2 lives = %d, want 2 (3 − 1 death)", got)
	}
}

func playerAlive(s *sim.Sim, id sim.EntityID) bool {
	for _, e := range s.Entities() {
		if e.ID == id && e.Kind == sim.KindPlayer && e.Flags&sim.FlagDead == 0 {
			return true
		}
	}
	return false
}
