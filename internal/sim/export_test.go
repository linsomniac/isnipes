package sim

// SpawnSnipeForTest spawns a snipe at the given tile centre, with the
// given parent generator ID (use 0 for "no parent" in test fixtures).
// Returns the snipe's EntityID.
func SpawnSnipeForTest(s *Sim, parentGen EntityID, tx, ty int) EntityID {
	id, ok := s.spawnSnipeAt(parentGen,
		int32(tx*subtilePerTile+subtilePerTile/2),
		int32(ty*subtilePerTile+subtilePerTile/2),
		tx, ty)
	if !ok {
		panic("SpawnSnipeForTest: alloc failed")
	}
	return id
}

// SnipeStateForTest returns the AI state of a snipe by ID.
func SnipeStateForTest(s *Sim, id EntityID) (SnipeStateInfo, bool) {
	return s.snipeStateInfoFor(id)
}

// LiveSnipeIDsForTest returns IDs of all live snipes.
func LiveSnipeIDsForTest(s *Sim) []EntityID {
	var out []EntityID
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID != 0 && e.Kind == KindSnipe && e.Flags&FlagDead == 0 {
			out = append(out, e.ID)
		}
	}
	return out
}

// GeneratorEmitCooldownForTest reads a generator's per-tick cooldown.
func GeneratorEmitCooldownForTest(s *Sim, id EntityID) (uint16, bool) {
	gs, ok := s.store.generators[id]
	if !ok {
		return 0, false
	}
	return gs.emitCooldown, true
}

// ForceExhaustedForTest puts the sim into a state where the next
// entity-ID allocation will fail. Used by TestTickReturnsErrIDExhausted.
//
// AIDEV-NOTE: implementation details (which knob we tweak) are
// intentionally hidden from spec — §8 only requires that exhaustion
// is detected before nextID would cross math.MaxUint32. We set
// nextID = math.MaxUint32, so allocID returns false immediately.
func ForceExhaustedForTest(s *Sim) {
	s.store.nextID = 0xFFFFFFFF
}

// MazeForTest exposes the underlying maze (read-only) for fixture-
// based tests that need to know tile contents.
func MazeForTest(s *Sim) (w, h int, at func(x, y int) Tile) {
	return s.maze.W, s.maze.H, s.maze.at
}

// OverrideMazeForTest replaces the sim's maze with an all-floor
// fixture (outer wall preserved) for combat/physics tests that need a
// known geometry. The original maze layout is discarded; the packed
// map bytes are recomputed.
func OverrideMazeForTest(s *Sim, w, h int, tiles []Tile) {
	if len(tiles) != w*h {
		panic("OverrideMazeForTest: bad tile slice")
	}
	s.maze = &maze{W: w, H: h, tiles: append([]Tile(nil), tiles...)}
	s.cfg.Width = w
	s.cfg.Height = h
	s.mapBytes = packTiles(s.maze)
}

// PlacePlayerForTest force-sets a player's subtile position.
func PlacePlayerForTest(s *Sim, id EntityID, x, y int32) {
	idx := s.store.findByID(id)
	if idx < 0 {
		panic("PlacePlayerForTest: unknown id")
	}
	s.store.slots[idx].X = x
	s.store.slots[idx].Y = y
}

// PlaceGeneratorForTest spawns a generator at a chosen tile centre.
func PlaceGeneratorForTest(s *Sim, x, y int32) EntityID {
	id, ok := s.store.allocID()
	if !ok {
		panic("PlaceGeneratorForTest: id exhausted")
	}
	idx := s.store.alloc()
	if idx < 0 {
		panic("PlaceGeneratorForTest: no slot")
	}
	s.store.slots[idx] = Entity{
		ID:     id,
		Kind:   KindGenerator,
		HP:     generatorHP,
		Facing: DirS,
		X:      x,
		Y:      y,
	}
	return id
}

// RemoveGeneratorsForTest deletes every generator entity.
func RemoveGeneratorsForTest(s *Sim) {
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID != 0 && e.Kind == KindGenerator {
			s.store.remove(e.ID)
		}
	}
}

// EntityRawForTest returns a copy of the slab entity by ID.
func EntityRawForTest(s *Sim, id EntityID) (Entity, bool) {
	idx := s.store.findByID(id)
	if idx < 0 {
		return Entity{}, false
	}
	return s.store.slots[idx], true
}

// PlayerStateForTest exposes the unexported player state fields.
type PlayerStateForTestT struct {
	FireCooldown   uint8
	LastDir        Dir
	LastInputTick  uint16
	RespawnAt      uint32
	HasRespawnAt   bool
	DeathX, DeathY int32
}

func PlayerStateForTest(s *Sim, id EntityID) (PlayerStateForTestT, bool) {
	ps, ok := s.store.players[id]
	if !ok {
		return PlayerStateForTestT{}, false
	}
	return PlayerStateForTestT{
		FireCooldown:  ps.fireCooldown,
		LastDir:       ps.lastDir,
		LastInputTick: ps.lastInputTick,
		RespawnAt:     ps.respawnAt,
		HasRespawnAt:  ps.hasRespawnAt,
		DeathX:        ps.deathX,
		DeathY:        ps.deathY,
	}, true
}

// LiveProjectileIDsForTest returns the IDs of every live projectile.
func LiveProjectileIDsForTest(s *Sim) []EntityID {
	var out []EntityID
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID != 0 && e.Kind == KindProjectile {
			out = append(out, e.ID)
		}
	}
	return out
}

// HistoryAtForTest exposes the per-entity history ring lookup. Used
// by Phase 4 §8 lag-comp tests. Returns (x, y, flags, tick, ok).
func HistoryAtForTest(s *Sim, id EntityID, t uint32) (int32, int32, uint8, uint32, bool) {
	h, ok := s.store.histories[id]
	if !ok {
		return 0, 0, 0, 0, false
	}
	smp, ok := h.at(t)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return smp.x, smp.y, smp.flags, smp.tick, true
}

// AllSpawnTilesForTest returns every TileSpawnPlayer tile.
func AllSpawnTilesForTest(s *Sim) [][2]int {
	var out [][2]int
	for y := 0; y < s.maze.H; y++ {
		for x := 0; x < s.maze.W; x++ {
			if s.maze.at(x, y) == TileSpawnPlayer {
				out = append(out, [2]int{x, y})
			}
		}
	}
	return out
}
