package sim

// Phase 5 §3.8 — kill-reward and death-penalty scalars.
const (
	killRewardSnipe     int32 = 1
	killRewardGenerator int32 = 10
	killRewardPlayer    int32 = 25
	deathPenaltyOwn     int32 = -5
)

// Phase 5 §6.2 — default starting lives when no level table is active
// (Config.LevelLetter == 0). Matches the original Snipes free-for-all
// expectation; §22 open question #1 pins this value via TestPvPDefaultLives.
const defaultPvPLives uint8 = 3

// PlayerScore is one player's externally-visible scoring state. Returned
// by (*Sim).Scores. Eliminated == true means the player has reached zero
// lives and the slab entry has been GC'd; their record persists in the
// player map for fingerprint stability (§16.1).
type PlayerScore struct {
	Lives      uint8
	Score      int32
	Eliminated bool
}

// LivesRemaining returns the player's remaining lives. Returns 0 for
// non-player IDs and for eliminated players.
func (s *Sim) LivesRemaining(id EntityID) uint8 {
	if ps, ok := s.store.players[id]; ok {
		return ps.livesRemaining
	}
	return 0
}

// Score returns the player's current score. May be negative due to the
// §3.8 death penalty. Returns 0 for non-player IDs.
func (s *Sim) Score(id EntityID) int32 {
	if ps, ok := s.store.players[id]; ok {
		return ps.score
	}
	return 0
}

// Eliminated reports whether the player has reached zero lives. Once
// true the value never returns to false even after the slab entry is
// GC'd; the underlying playerState record is retained.
func (s *Sim) Eliminated(id EntityID) bool {
	if ps, ok := s.store.players[id]; ok {
		return ps.eliminated
	}
	return false
}

// SpawnInvulnUntil returns the server tick at which the player's
// FlagSpawnInvuln will be cleared. Returns 0 if the player is not
// currently invulnerable (or unknown).
func (s *Sim) SpawnInvulnUntil(id EntityID) uint32 {
	if ps, ok := s.store.players[id]; ok {
		return ps.spawnInvulnUntil
	}
	return 0
}

// Scores returns a snapshot of every player's scoring state, including
// eliminated players whose slab entry has been GC'd. The returned map
// is freshly allocated; safe to retain. Iteration order is not stable
// — callers needing a fixed order MUST sort by EntityID.
func (s *Sim) Scores() map[EntityID]PlayerScore {
	out := make(map[EntityID]PlayerScore, len(s.store.players))
	for id, ps := range s.store.players {
		out[id] = PlayerScore{
			Lives:      ps.livesRemaining,
			Score:      ps.score,
			Eliminated: ps.eliminated,
		}
	}
	return out
}

// awardKill credits the killer's score per §3.8 based on the victim's
// kind. killer == 0 (environment / timeout) awards nothing. A player
// killing their own entity (killer == victim) also awards nothing —
// the −5 death penalty applies independently in killEntity.
func (s *Sim) awardKill(killer EntityID, victimKind EntityKind) {
	if killer == 0 {
		return
	}
	ps, ok := s.store.players[killer]
	if !ok {
		return
	}
	switch victimKind {
	case KindSnipe:
		ps.score += killRewardSnipe
	case KindGenerator:
		ps.score += killRewardGenerator
	case KindPlayer:
		ps.score += killRewardPlayer
	}
}

// FreezePlayer zeroes the velocity for the given player's slab entry
// (if present) and clears any pending fire intent. Used by the match
// actor when a connection drops into DC-grace per §4.7.1; the entity
// remains targetable but stops applying inputs. Idempotent and safe
// when the entity does not exist.
func (s *Sim) FreezePlayer(id EntityID) {
	idx := s.store.findByID(id)
	if idx >= 0 {
		e := &s.store.slots[idx]
		if e.Kind == KindPlayer {
			e.VX, e.VY = 0, 0
		}
	}
	if ps, ok := s.store.players[id]; ok {
		ps.fireCooldown = 0
		ps.lastDir = DirIdle
	}
}

// RemovePlayer fully removes a player from the sim — slab entry,
// playerState, history, and per-entity PRNG state. Used at the §4.7.1
// 30-second DC-grace deadline for slot termination (the "slot is
// terminated" path). Distinct from elimination, which retains the
// playerState record for the §16.1 fingerprint tail.
func (s *Sim) RemovePlayer(id EntityID) {
	s.store.remove(id)
	if s.entityPRNGs != nil {
		delete(s.entityPRNGs.cache, id)
	}
}

// startingLivesFor returns the per-player starting-lives count given
// the (possibly absent) level table configuration. §6.2.
func startingLivesFor(cfg Config) uint8 {
	if cfg.LevelLetter == 0 {
		return defaultPvPLives
	}
	lp := LookupLevel(cfg.LevelLetter, cfg.LevelNumber)
	if lp.PlayerLives < 1 {
		return 1
	}
	if lp.PlayerLives > 255 {
		return 255
	}
	return uint8(lp.PlayerLives)
}
