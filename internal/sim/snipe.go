package sim

// AIState is the per-snipe state-machine label (PHASE3.md §7.2).
type AIState uint8

const (
	AIStateIdle   AIState = 0
	AIStatePatrol AIState = 1
	AIStateChase  AIState = 2
	AIStateAttack AIState = 3
	AIStateDead   AIState = 4
)

// snipeState is the per-snipe sidecar (PHASE3.md §7.2).
type snipeState struct {
	parentGen     EntityID
	spawnTick     uint32 // serverTick at which this snipe was allocated
	aiState       AIState
	aiTimer       uint16
	patrolDir     Dir       // direction the snipe is currently patrolling in
	lastBFSTick   uint32    // last serverTick when we recomputed the BFS
	bfsPath       []tilePos // up to 3 cached next tiles
	chaseTarget   EntityID
	losLostTicks  uint16
	fireCooldown  uint16
	wallBlockedAt bool // set if the last move was clamped by a wall this tick
}

// generatorState carries the per-generator emission timer plus the
// per-generator rotation offset for slot search (PHASE3.md §11.1).
type generatorState struct {
	emitCooldown uint16
	rotation     uint8 // 0..7; the per-generator EntityID-derived neighbour-order rotation
}

// SnipeStateInfo is the exported test-side view of a snipe's AI fields
// (export_test.go exposes a helper that returns this).
type SnipeStateInfo struct {
	AIState      AIState
	AITimer      uint16
	PatrolDir    Dir
	ChaseTarget  EntityID
	LOSLostTicks uint16
	FireCooldown uint16
	ParentGen    EntityID
	SpawnTick    uint32
}

// spawnSnipeAt allocates a KindSnipe entity at the centre of (tx, ty)
// with facing pointing away from the parent generator. Used by the
// generator-emission code path. Caller MUST have verified the slot is
// clear and the AABB fits the floor.
func (s *Sim) spawnSnipeAt(parentGen EntityID, genX, genY int32, tx, ty int) (EntityID, bool) {
	id, ok := s.store.allocID()
	if !ok {
		return 0, false
	}
	idx := s.store.alloc()
	if idx < 0 {
		return 0, false
	}
	cx := int32(tx*subtilePerTile + subtilePerTile/2)
	cy := int32(ty*subtilePerTile + subtilePerTile/2)
	facing := dir8Toward(genX, genY, cx, cy) // *away* from gen = toward (cx,cy) from gen
	s.store.slots[idx] = Entity{
		ID:     id,
		Kind:   KindSnipe,
		HP:     1,
		Facing: facing,
		Flags:  FlagSpawnInvuln,
		X:      cx,
		Y:      cy,
	}
	s.store.snipes[id] = &snipeState{
		parentGen: parentGen,
		spawnTick: s.serverTick,
		aiState:   AIStateIdle,
	}
	return id, true
}

// dir8Toward returns the 8-direction Dir best matching the vector from
// (fromX, fromY) to (toX, toY). Returns DirS if the points coincide
// (matches P1 §8's initial-facing default).
func dir8Toward(fromX, fromY, toX, toY int32) Dir {
	dx := toX - fromX
	dy := toY - fromY
	if dx == 0 && dy == 0 {
		return DirS
	}
	ax := dx
	if ax < 0 {
		ax = -ax
	}
	ay := dy
	if ay < 0 {
		ay = -ay
	}
	// "Cardinal" if one axis dominates by 2× or more; else diagonal.
	if ax > 2*ay {
		if dx > 0 {
			return DirE
		}
		return DirW
	}
	if ay > 2*ax {
		if dy > 0 {
			return DirS
		}
		return DirN
	}
	if dx > 0 && dy > 0 {
		return DirSE
	}
	if dx > 0 && dy < 0 {
		return DirNE
	}
	if dx < 0 && dy > 0 {
		return DirSW
	}
	return DirNW
}

// chebyshevTiles returns the tile-grid Chebyshev distance between two
// subtile positions.
func chebyshevTiles(ax, ay, bx, by int32) int {
	atx := int(ax / subtilePerTile)
	aty := int(ay / subtilePerTile)
	btx := int(bx / subtilePerTile)
	bty := int(by / subtilePerTile)
	dx := atx - btx
	if dx < 0 {
		dx = -dx
	}
	dy := aty - bty
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

// applySnipeWeakBlock implements §10.7: when two snipes overlap, the
// higher-EntityID snipe is clamped on the dominant axis by 1 subtile,
// and its velocity component pointing into the overlap is zeroed.
func (s *Sim) applySnipeWeakBlock() {
	ids := s.store.liveIDsSorted()
	// Collect snipe IDs in order.
	snipes := make([]EntityID, 0, len(ids))
	for _, id := range ids {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		if s.store.slots[idx].Kind == KindSnipe && s.store.slots[idx].Flags&FlagDead == 0 {
			snipes = append(snipes, id)
		}
	}
	for i, hi := range snipes {
		iHi := s.store.findByID(hi)
		if iHi < 0 {
			continue
		}
		eHi := &s.store.slots[iHi]
		for j := 0; j < i; j++ {
			lo := snipes[j]
			iLo := s.store.findByID(lo)
			if iLo < 0 {
				continue
			}
			eLo := &s.store.slots[iLo]
			aLo := aabb{cx: eLo.X, cy: eLo.Y, he: snipeHalfExt}
			aHi := aabb{cx: eHi.X, cy: eHi.Y, he: snipeHalfExt}
			if !aHi.overlaps(aLo) {
				continue
			}
			// Dominant axis clamp.
			dx := eHi.X - eLo.X
			dy := eHi.Y - eLo.Y
			absdx := dx
			if absdx < 0 {
				absdx = -absdx
			}
			absdy := dy
			if absdy < 0 {
				absdy = -absdy
			}
			// Displace the higher-ID snipe off the dominant axis, but only if
			// the resulting AABB stays clear of walls — otherwise the enlarged
			// (MAZE_REVAMP.md) snipe could be shoved into a wall near a closed
			// cell link, violating the no-entity-inside-wall invariant. Weak
			// separation is cosmetic, so skipping a blocked push (snipes stay
			// briefly overlapped) is harmless.
			if absdx >= absdy {
				var nx int32
				if dx > 0 {
					nx = eLo.X + 2*snipeHalfExt + 1
				} else {
					nx = eLo.X - 2*snipeHalfExt - 1
				}
				if !s.aabbHitsWall(nx, eHi.Y, snipeHalfExt) {
					eHi.X = nx
					eHi.VX = 0
				}
			} else {
				var ny int32
				if dy > 0 {
					ny = eLo.Y + 2*snipeHalfExt + 1
				} else {
					ny = eLo.Y - 2*snipeHalfExt - 1
				}
				if !s.aabbHitsWall(eHi.X, ny, snipeHalfExt) {
					eHi.Y = ny
					eHi.VY = 0
				}
			}
		}
	}
}

// aabbHitsWall reports whether an axis-aligned box centred at (cx, cy) with
// half-extent he overlaps any WALL tile. It checks the four corners, which
// covers the ≤2×2 tile span of an entity with he ≤ subtilePerTile (snipes).
func (s *Sim) aabbHitsWall(cx, cy, he int32) bool {
	return s.maze.at(int((cx-he)/subtilePerTile), int((cy-he)/subtilePerTile)) == TileWall ||
		s.maze.at(int((cx+he-1)/subtilePerTile), int((cy-he)/subtilePerTile)) == TileWall ||
		s.maze.at(int((cx-he)/subtilePerTile), int((cy+he-1)/subtilePerTile)) == TileWall ||
		s.maze.at(int((cx+he-1)/subtilePerTile), int((cy+he-1)/subtilePerTile)) == TileWall
}

// stepGeneratorsEmission is the per-tick generator emission pass; the
// body is fleshed out in iter 4 (PHASE3.md §11). Stub returns no
// events while levels are inactive.
func (s *Sim) stepGeneratorsEmission() []Event {
	if s.cfg.LevelLetter == 0 {
		return nil
	}
	return s.runGeneratorEmissions()
}

// snipeStateInfoFor reads the SnipeStateInfo for tests.
func (s *Sim) snipeStateInfoFor(id EntityID) (SnipeStateInfo, bool) {
	ss, ok := s.store.snipes[id]
	if !ok {
		return SnipeStateInfo{}, false
	}
	return SnipeStateInfo{
		AIState:      ss.aiState,
		AITimer:      ss.aiTimer,
		PatrolDir:    ss.patrolDir,
		ChaseTarget:  ss.chaseTarget,
		LOSLostTicks: ss.losLostTicks,
		FireCooldown: ss.fireCooldown,
		ParentGen:    ss.parentGen,
		SpawnTick:    ss.spawnTick,
	}, true
}
