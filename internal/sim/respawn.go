package sim

import "sort"

// selectRespawnTile picks a respawn tile per §10.5. Returns the chosen
// tile and ok=true; ok=false means "no candidate exists this tick —
// defer".
func (s *Sim) selectRespawnTile(playerID EntityID) (tilePos, bool) {
	// Step 1: candidate list = every TileSpawnPlayer whose AABB is
	// clear of every non-DEAD entity AABB.
	candidates := s.spawnTilesForPlayers()
	free := make([]tilePos, 0, len(candidates))
	for _, c := range candidates {
		if s.tileFreeForPlayerAABB(c) {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		return tilePos{}, false
	}

	// Step 3: live hostiles list.
	hostiles := s.liveHostilesForRespawn(playerID)

	// Step 4: safety scores.
	type scored struct {
		t     tilePos
		score int
	}
	scoredList := make([]scored, len(free))
	deathX, deathY := s.playerDeathTile(playerID)
	for i, c := range free {
		var safety int
		if len(hostiles) > 0 {
			safety = chebyshev(c, hostiles[0])
			for _, h := range hostiles[1:] {
				d := chebyshev(c, h)
				if d < safety {
					safety = d
				}
			}
		} else {
			safety = chebyshev(c, tilePos{X: deathX, Y: deathY})
		}
		scoredList[i] = scored{c, safety}
	}

	// Step 5: sort by descending safety; tie-break ascending (ty*W + tx).
	W := s.maze.W
	sort.Slice(scoredList, func(i, j int) bool {
		if scoredList[i].score != scoredList[j].score {
			return scoredList[i].score > scoredList[j].score
		}
		ai := scoredList[i].t.Y*W + scoredList[i].t.X
		bi := scoredList[j].t.Y*W + scoredList[j].t.X
		return ai < bi
	})

	// Step 6: random pick from top min(3, len).
	bound := len(scoredList)
	if bound > 3 {
		bound = 3
	}
	r := s.entityPRNGs.rand(playerID)
	idx := r.IntN(bound)
	return scoredList[idx].t, true
}

// spawnTilesForPlayers returns every TileSpawnPlayer in the maze.
func (s *Sim) spawnTilesForPlayers() []tilePos {
	out := make([]tilePos, 0, 8)
	for y := 0; y < s.maze.H; y++ {
		for x := 0; x < s.maze.W; x++ {
			if s.maze.at(x, y) == TileSpawnPlayer {
				out = append(out, tilePos{x, y})
			}
		}
	}
	return out
}

// tileFreeForPlayerAABB reports true iff a player AABB centred on
// tile `t`'s centre does not overlap any non-DEAD entity AABB.
func (s *Sim) tileFreeForPlayerAABB(t tilePos) bool {
	cx := int32(t.X*subtilePerTile + subtilePerTile/2)
	cy := int32(t.Y*subtilePerTile + subtilePerTile/2)
	candidate := aabb{cx: cx, cy: cy, he: playerHalfExt}
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 {
			continue
		}
		if e.Flags&FlagDead != 0 {
			continue
		}
		other := aabb{cx: e.X, cy: e.Y, he: entityHalfExt(e.Kind)}
		if candidate.overlaps(other) {
			return false
		}
	}
	return true
}

// liveHostilesForRespawn collects the (tile-position) of every live
// player (other than `playerID`) and every live generator.
func (s *Sim) liveHostilesForRespawn(playerID EntityID) []tilePos {
	out := make([]tilePos, 0, 16)
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 || e.ID == playerID {
			continue
		}
		if e.Flags&FlagDead != 0 {
			continue
		}
		if e.Kind != KindPlayer && e.Kind != KindGenerator {
			continue
		}
		out = append(out, tilePos{X: int(e.X / subtilePerTile), Y: int(e.Y / subtilePerTile)})
	}
	return out
}

func (s *Sim) playerDeathTile(playerID EntityID) (int, int) {
	if ps, ok := s.store.players[playerID]; ok {
		return int(ps.deathX / subtilePerTile), int(ps.deathY / subtilePerTile)
	}
	return 0, 0
}
