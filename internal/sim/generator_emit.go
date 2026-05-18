package sim

// runGeneratorEmissions runs each live generator's emission timer for
// one tick, per PHASE3.md §11.
//
// Iteration order: ascending EntityID (P1 §12).
func (s *Sim) runGeneratorEmissions() []Event {
	if s.cfg.LevelLetter == 0 {
		return nil
	}
	lp := LookupLevel(s.cfg.LevelLetter, s.cfg.LevelNumber)
	var events []Event

	// Live snipe count for the global cap check.
	liveSnipes := 0
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 {
			continue
		}
		if e.Kind == KindSnipe && e.Flags&FlagDead == 0 {
			liveSnipes++
		}
	}

	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		gen := &s.store.slots[idx]
		if gen.Kind != KindGenerator || gen.Flags&FlagDead != 0 {
			continue
		}
		gs := s.store.generators[id]
		if gs == nil {
			continue
		}
		if gs.emitCooldown > 0 {
			gs.emitCooldown--
			continue
		}
		// Global cap — defer without resetting cooldown.
		if liveSnipes >= lp.MaxSnipesTotal {
			continue
		}
		slot, ok := s.findEmissionSlot(gen, gs)
		if !ok {
			// Defer; cooldown stays at zero per §11.3 step 4.
			continue
		}
		// Spawn the snipe.
		newID, ok := s.spawnSnipeAt(gen.ID, gen.X, gen.Y, slot.X, slot.Y)
		if !ok {
			s.quiesced = true
			return events
		}
		liveSnipes++
		// Reset cooldown via per-gen PRNG.
		r := s.entityPRNGs.rand(gen.ID)
		gs.emitCooldown = emitCooldownMin + uint16(r.IntN(int(emitCooldownMax-emitCooldownMin+1)))
		events = append(events, Event{
			Kind:   EventEntitySpawn,
			Actor:  gen.ID,
			Target: newID,
			Reason: 0,
		})
	}
	return events
}

// findEmissionSlot walks the 8 neighbouring tiles in the per-generator
// rotation order and returns the first that satisfies §11.3 step 3
// conditions.
func (s *Sim) findEmissionSlot(gen *Entity, gs *generatorState) (tilePos, bool) {
	gtx := int(gen.X / subtilePerTile)
	gty := int(gen.Y / subtilePerTile)
	// Fixed neighbour order per §3.6: N, E, S, W, NE, SE, SW, NW.
	base := [8]tilePos{
		{0, -1}, {1, 0}, {0, 1}, {-1, 0}, // N E S W
		{1, -1}, {1, 1}, {-1, 1}, {-1, -1}, // NE SE SW NW
	}
	for i := 0; i < 8; i++ {
		off := base[(int(gs.rotation)+i)%8]
		ntx := gtx + off.X
		nty := gty + off.Y
		if s.maze.at(ntx, nty) != TileFloor {
			continue
		}
		cx := int32(ntx*subtilePerTile + subtilePerTile/2)
		cy := int32(nty*subtilePerTile + subtilePerTile/2)
		// Check no entity overlaps the snipe-AABB at (cx, cy).
		candidate := aabb{cx: cx, cy: cy, he: snipeHalfExt}
		bad := false
		for j := range s.store.slots {
			e := &s.store.slots[j]
			if e.ID == 0 || e.Flags&FlagDead != 0 {
				continue
			}
			other := aabb{cx: e.X, cy: e.Y, he: entityHalfExt(e.Kind)}
			if candidate.overlaps(other) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		// Snipe AABB must not overlap a wall (we check the 4 corner
		// tiles).
		if s.maze.at(int((cx-snipeHalfExt)/subtilePerTile), int((cy-snipeHalfExt)/subtilePerTile)) == TileWall ||
			s.maze.at(int((cx+snipeHalfExt-1)/subtilePerTile), int((cy-snipeHalfExt)/subtilePerTile)) == TileWall ||
			s.maze.at(int((cx-snipeHalfExt)/subtilePerTile), int((cy+snipeHalfExt-1)/subtilePerTile)) == TileWall ||
			s.maze.at(int((cx+snipeHalfExt-1)/subtilePerTile), int((cy+snipeHalfExt-1)/subtilePerTile)) == TileWall {
			continue
		}
		return tilePos{ntx, nty}, true
	}
	return tilePos{}, false
}
