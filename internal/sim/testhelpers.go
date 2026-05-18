//go:build testhooks

package sim

// This file collects test-only helpers exported from internal/sim so
// cross-package tests (internal/match, internal/net) can drive the
// sim into specific states without re-deriving the work. They are
// fenced behind `//go:build testhooks` so production binaries never
// link them — any test that uses these must be built with
// `-tags=testhooks` (see CI workflow / Makefile).

// SetLivesForTest overrides a player's livesRemaining counter.
func SetLivesForTest(s *Sim, id EntityID, n uint8) {
	if ps, ok := s.store.players[id]; ok {
		ps.livesRemaining = n
		if n == 0 {
			ps.eliminated = true
		}
	}
}

// SetScoreForTest overrides a player's score.
func SetScoreForTest(s *Sim, id EntityID, score int32) {
	if ps, ok := s.store.players[id]; ok {
		ps.score = score
	}
}

// SetServerTickForTest jumps the sim's serverTick. Used by TIMER
// end-reason tests so they don't have to spin 18 000 Tick calls.
func SetServerTickForTest(s *Sim, t uint32) {
	s.serverTick = t
}

// ForceKillForTest invokes killEntity directly with a synthesised
// shooter. Used by match-side end-reason tests to drain a player's
// lives without staging real combat.
func ForceKillForTest(s *Sim, victim, killer EntityID) {
	idx := s.store.findByID(victim)
	if idx < 0 {
		return
	}
	e := &s.store.slots[idx]
	if e.Flags&FlagDead != 0 {
		return
	}
	_ = s.killEntity(nil, e, killer)
}

// RemoveAllGeneratorsForTest deletes every generator entity. Used to
// fake a "PVE_COMPLETE objective complete" condition without staging
// combat against generators.
func RemoveAllGeneratorsForTest(s *Sim) {
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID != 0 && e.Kind == KindGenerator {
			s.store.remove(e.ID)
		}
	}
}

// RemoveAllSnipesForTest deletes every snipe entity.
func RemoveAllSnipesForTest(s *Sim) {
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID != 0 && e.Kind == KindSnipe {
			s.store.remove(e.ID)
		}
	}
}
