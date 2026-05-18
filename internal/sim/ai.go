package sim

// stepSnipeAI advances one snipe by one tick (PHASE3.md §10).
// Returns any events generated this tick (notably entity_spawn for
// snipe-fired projectiles); the caller appends them.
//
// Iteration order (ascending EntityID) is the caller's responsibility
// (sim.go §11 step 4.5).
func (s *Sim) stepSnipeAI(snipe *Entity, ss *snipeState) []Event {
	// Resolve level params (the §6 cfg fields are zero in Phase-1
	// compat mode; we skip AI entirely there).
	if s.cfg.LevelLetter == 0 {
		return nil
	}
	lp := LookupLevel(s.cfg.LevelLetter, s.cfg.LevelNumber)
	if ss.fireCooldown > 0 {
		ss.fireCooldown--
	}
	switch ss.aiState {
	case AIStateIdle:
		s.aiStepIdle(snipe, ss)
		return nil
	case AIStatePatrol:
		s.aiStepPatrol(snipe, ss, lp)
		return nil
	case AIStateChase:
		return s.aiStepChase(snipe, ss, lp)
	case AIStateAttack:
		return s.aiStepAttack(snipe, ss, lp)
	}
	return nil
}

// aiStepIdle keeps velocity at zero and counts down spawn-invuln.
func (s *Sim) aiStepIdle(snipe *Entity, ss *snipeState) {
	snipe.VX, snipe.VY = 0, 0
	if s.serverTick-ss.spawnTick >= uint32(spawnInvulnTicks) {
		snipe.Flags &^= FlagSpawnInvuln
		ss.aiState = AIStatePatrol
		ss.aiTimer = 0
	}
}

// aiStepPatrol picks a cardinal direction every aiTimer ticks, moves
// in it, and watches for LOS to a player.
func (s *Sim) aiStepPatrol(snipe *Entity, ss *snipeState, lp LevelParams) {
	if ss.aiTimer == 0 || ss.wallBlockedAt {
		// Pick a new direction.
		r := s.entityPRNGs.rand(snipe.ID)
		// 4 cardinal directions in fixed N/E/S/W order.
		idx := r.IntN(4)
		switch idx {
		case 0:
			ss.patrolDir = DirN
		case 1:
			ss.patrolDir = DirE
		case 2:
			ss.patrolDir = DirS
		case 3:
			ss.patrolDir = DirW
		}
		ss.aiTimer = snipePatrolMinTimer + uint16(r.IntN(int(snipePatrolMaxTimer-snipePatrolMinTimer+1)))
		ss.wallBlockedAt = false
	}
	ss.aiTimer--
	snipe.Facing = ss.patrolDir
	vx, vy := velocityFor(ss.patrolDir, lp.SnipeSpeed)
	snipe.VX, snipe.VY = int16(vx), int16(vy)

	// LOS check (ascending EntityID for tie-determinism).
	if target, ok := s.findFirstVisiblePlayer(snipe, lp.LOSRadius); ok {
		ss.aiState = AIStateChase
		ss.chaseTarget = target
		ss.losLostTicks = 0
		ss.bfsPath = ss.bfsPath[:0]
		ss.lastBFSTick = 0
		snipe.VX, snipe.VY = 0, 0 // reassessed next tick
	}
}

// aiStepChase moves the snipe along its cached BFS path toward the
// chase target, recomputing every 15 ticks per the §8.2 stagger.
func (s *Sim) aiStepChase(snipe *Entity, ss *snipeState, lp LevelParams) []Event {
	target, ok := s.findEntityByID(ss.chaseTarget)
	if !ok || target.Flags&FlagDead != 0 {
		ss.chaseTarget = 0
		ss.aiState = AIStatePatrol
		ss.aiTimer = 0
		return nil
	}

	// LOS gate.
	hasLOS := s.snipeHasLOSToPlayer(snipe, target, lp.LOSRadius)
	if hasLOS {
		ss.losLostTicks = 0
	} else {
		ss.losLostTicks++
		if ss.losLostTicks >= snipeChaseLOSLostCD {
			ss.aiState = AIStatePatrol
			ss.aiTimer = 0
			ss.chaseTarget = 0
			snipe.VX, snipe.VY = 0, 0
			return nil
		}
	}

	// Attack gate: LOS + clear projectile path → transition to attack.
	if hasLOS && s.projectilePathClear(snipe, target, lp.LOSRadius) {
		ss.aiState = AIStateAttack
		snipe.VX, snipe.VY = 0, 0
		return nil
	}

	// Recompute BFS at the staggered cadence.
	stx := int(target.X / subtilePerTile)
	sty := int(target.Y / subtilePerTile)
	mtx := int(snipe.X / subtilePerTile)
	mty := int(snipe.Y / subtilePerTile)
	if len(ss.bfsPath) == 0 || (uint32(snipe.ID)+s.serverTick)%bfsRecomputeEvery == 0 {
		ss.bfsPath = ss.bfsPath[:0]
		// Walk up to 3 tiles ahead via repeated PathNext from the
		// last cached tile (or the snipe's tile) toward the player.
		cx, cy := mtx, mty
		maxDepth := lp.LOSRadius + 4
		for i := 0; i < 3; i++ {
			nx, ny, ok := PathNext(s.maze, cx, cy, stx, sty, maxDepth)
			if !ok || (nx == cx && ny == cy) {
				break
			}
			ss.bfsPath = append(ss.bfsPath, tilePos{nx, ny})
			cx, cy = nx, ny
			if cx == stx && cy == sty {
				break
			}
		}
		ss.lastBFSTick = s.serverTick
	}

	// Pop the head of bfsPath if the snipe is centred on it.
	for len(ss.bfsPath) > 0 {
		head := ss.bfsPath[0]
		hx := int32(head.X*subtilePerTile + subtilePerTile/2)
		hy := int32(head.Y*subtilePerTile + subtilePerTile/2)
		if abs32(snipe.X-hx) <= 16 && abs32(snipe.Y-hy) <= 16 {
			ss.bfsPath = ss.bfsPath[1:]
			continue
		}
		// Move toward head.
		dir := dir8Toward(snipe.X, snipe.Y, hx, hy)
		vx, vy := velocityFor(dir, lp.SnipeSpeed)
		snipe.VX, snipe.VY = int16(vx), int16(vy)
		snipe.Facing = dir
		return nil
	}
	// Out of path tiles; idle this tick — recompute next.
	snipe.VX, snipe.VY = 0, 0
	return nil
}

// aiStepAttack fires when cooldown allows; otherwise idles.
func (s *Sim) aiStepAttack(snipe *Entity, ss *snipeState, lp LevelParams) []Event {
	target, ok := s.findEntityByID(ss.chaseTarget)
	if !ok || target.Flags&FlagDead != 0 {
		ss.aiState = AIStatePatrol
		ss.aiTimer = 0
		ss.chaseTarget = 0
		return nil
	}
	hasLOS := s.snipeHasLOSToPlayer(snipe, target, lp.LOSRadius)
	if !hasLOS {
		// Drop back to chase next tick.
		ss.aiState = AIStateChase
		ss.bfsPath = ss.bfsPath[:0]
		return nil
	}
	snipe.VX, snipe.VY = 0, 0
	if ss.fireCooldown > 0 {
		return nil
	}
	// Compute lead-adjusted aim.
	fireDir := s.computeSnipeFireDir(snipe, target, lp.SnipeLeadFactor)
	if fireDir == DirIdle {
		fireDir = dir8Toward(snipe.X, snipe.Y, target.X, target.Y)
	}
	snipe.Facing = fireDir

	// Check 64-projectile cap.
	liveProj := 0
	for j := range s.store.slots {
		if s.store.slots[j].ID != 0 && s.store.slots[j].Kind == KindProjectile {
			liveProj++
		}
	}
	if liveProj >= maxInFlightProjectiles {
		return nil
	}

	pid, ok := s.store.allocID()
	if !ok {
		s.quiesced = true
		return nil
	}
	ss.fireCooldown = lp.SnipeFireCooldown
	pIdx := s.store.alloc()
	if pIdx < 0 {
		return nil
	}
	vx, vy := velocityFor(fireDir, projectileSpeed)
	s.store.slots[pIdx] = Entity{
		ID:     pid,
		Kind:   KindProjectile,
		HP:     1,
		Facing: fireDir,
		X:      snipe.X,
		Y:      snipe.Y,
		VX:     int16(vx),
		VY:     int16(vy),
	}
	s.store.projectiles[pid] = &projectileState{
		lifetime:  projectileLifetime,
		shooterID: snipe.ID,
	}
	return []Event{{
		Kind:   EventEntitySpawn,
		Actor:  snipe.ID,
		Target: pid,
		Reason: 0,
	}}
}

// computeSnipeFireDir returns the Dir8 best matching a lead-adjusted
// shot at the target. SnipeLeadFactor 0 → no lead, 1 → half, 2 → full.
func (s *Sim) computeSnipeFireDir(snipe, target *Entity, leadFactor uint8) Dir {
	if leadFactor == 0 {
		return dir8Toward(snipe.X, snipe.Y, target.X, target.Y)
	}
	dx := int64(target.X - snipe.X)
	dy := int64(target.Y - snipe.Y)
	// distance in subtiles (Chebyshev approximation is fine for an aim
	// hint).
	dist := dx
	if dist < 0 {
		dist = -dist
	}
	dy2 := dy
	if dy2 < 0 {
		dy2 = -dy2
	}
	if dy2 > dist {
		dist = dy2
	}
	// ticks to reach = dist / projectileSpeed.
	ticks := dist / int64(projectileSpeed)
	if ticks <= 0 {
		return dir8Toward(snipe.X, snipe.Y, target.X, target.Y)
	}
	// lead vector = velocity * ticks, scaled by leadFactor/2.
	lx := int64(target.VX) * ticks
	ly := int64(target.VY) * ticks
	if leadFactor == 1 {
		lx /= 2
		ly /= 2
	}
	tx := target.X + int32(lx)
	ty := target.Y + int32(ly)
	return dir8Toward(snipe.X, snipe.Y, tx, ty)
}

// findFirstVisiblePlayer scans live players in ascending EntityID
// order and returns the first whose tile-Chebyshev distance ≤ losRadius
// AND HasLOS returns true.
func (s *Sim) findFirstVisiblePlayer(snipe *Entity, losRadius int) (EntityID, bool) {
	ids := s.store.liveIDsSorted()
	for _, id := range ids {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind != KindPlayer || e.Flags&FlagDead != 0 {
			continue
		}
		if chebyshevTiles(snipe.X, snipe.Y, e.X, e.Y) > losRadius {
			continue
		}
		sx := int(snipe.X / subtilePerTile)
		sy := int(snipe.Y / subtilePerTile)
		px := int(e.X / subtilePerTile)
		py := int(e.Y / subtilePerTile)
		if HasLOS(s.maze, sx, sy, px, py, losRadius) {
			return id, true
		}
	}
	return 0, false
}

// snipeHasLOSToPlayer reuses HasLOS with the snipe's and player's tile
// positions.
func (s *Sim) snipeHasLOSToPlayer(snipe, target *Entity, losRadius int) bool {
	if chebyshevTiles(snipe.X, snipe.Y, target.X, target.Y) > losRadius {
		return false
	}
	sx := int(snipe.X / subtilePerTile)
	sy := int(snipe.Y / subtilePerTile)
	px := int(target.X / subtilePerTile)
	py := int(target.Y / subtilePerTile)
	return HasLOS(s.maze, sx, sy, px, py, losRadius)
}

// projectilePathClear is a coarse approximation of "projectile path is
// clear" — Phase 3 §10.3 says raycast along the Dir8 best-matching
// the vector to the player. We reuse HasLOS as a tile-grid raycast
// from snipe to target; finer-grained checks (against entity AABBs
// in the path) are deferred.
func (s *Sim) projectilePathClear(snipe, target *Entity, losRadius int) bool {
	return s.snipeHasLOSToPlayer(snipe, target, losRadius)
}

func (s *Sim) findEntityByID(id EntityID) (*Entity, bool) {
	idx := s.store.findByID(id)
	if idx < 0 {
		return nil, false
	}
	return &s.store.slots[idx], true
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}
