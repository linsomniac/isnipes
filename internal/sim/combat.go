package sim

// projHitKind tags the outcome of resolveProjectile.
type projHitKind uint8

const (
	projHitNone projHitKind = iota
	projHitWall
	projHitEntity
)

// projHit describes one projectile's resolution this tick.
type projHit struct {
	kind     projHitKind
	targetID EntityID
	endX     int32
	endY     int32
}

// lagCompContext supplies the inputs needed to rewind candidate
// targets to their position at T_view per PHASE4.md §8. The zero
// value disables lag-comp (present-time hit detection).
type lagCompContext struct {
	currentTick uint32
	owtTicks    uint8
	// getHist returns the history ring for one entity ID, or nil.
	getHist func(EntityID) *entityHistory
	// getAllHistories returns the full per-entity history map for
	// the §8.6 ghost-candidate pass. May be nil — in which case the
	// ghost pass is a no-op (PHASE4.md §8.6 edge case).
	getAllHistories func() map[EntityID]*entityHistory
}

func (lc lagCompContext) enabled(shooterKind EntityKind) bool {
	return shooterKind == KindPlayer && lc.owtTicks > 0 && lc.getHist != nil
}

func (lc lagCompContext) tView() uint32 {
	// T_view = T_now - owt_ticks - interp_ticks, clamped to
	// [T_now - LagCompTicks, T_now].
	rewind := uint32(lc.owtTicks) + InterpTicks
	if rewind > LagCompTicks {
		rewind = LagCompTicks
	}
	if rewind > lc.currentTick {
		return 0
	}
	return lc.currentTick - rewind
}

// resolveProjectile performs the §10.2 unified swept-AABB pass for one
// projectile against the maze walls and against every candidate entity
// (already filtered by caller to exclude the shooter, the projectile
// itself, other projectiles, and dead entities). Returns the outcome.
//
// AIDEV-NOTE: wall *end position* comes from moveAndSlide (axis-
// separated; correct under the §9.3 contract). The wall *time
// fraction* we compare against entity-hit fractions is approximated
// from the clamped-axis displacement — exact would require duplicating
// the full axis-separated sweep with fraction tracking, but the
// approximation is within 1 subtile and the §10.2 "wall preferred on
// tie" rule still applies via the ≤ comparison below.
//
// Phase 4 §8: when lc.enabled(shooterKind) is true, each candidate's
// per-frame position and flags are sourced from the entity-history
// ring at T_view rather than from the live Entity.
func resolveProjectile(m *maze, proj *Entity, shooterID EntityID, shooterKind EntityKind, candidates []*Entity, lc lagCompContext) projHit {
	x0, y0 := proj.X, proj.Y
	vx, vy := int32(proj.VX), int32(proj.VY)
	he := int32(projectileHalfExt)

	// Wall sweep.
	wx, wy, wvx, wvy := moveAndSlide(m, x0, y0, vx, vy, he, nil)
	wallHit := (vx != 0 && wvx == 0) || (vy != 0 && wvy == 0)

	// Wall hit time fraction (num/den, den > 0).
	var wallNum, wallDen int64 = 1, 1
	if wallHit {
		if vx != 0 && wvx == 0 {
			wallNum = int64(wx - x0)
			wallDen = int64(vx)
		} else {
			wallNum = int64(wy - y0)
			wallDen = int64(vy)
		}
		if wallDen < 0 {
			wallNum, wallDen = -wallNum, -wallDen
		}
		if wallNum < 0 {
			wallNum = 0
		}
	}

	// Entity sweep.
	var bestNum, bestDen int64 = 0, 1
	bestSet := false
	var bestID EntityID
	useLagComp := lc.enabled(shooterKind)
	var tView uint32
	if useLagComp {
		tView = lc.tView()
	}
	// Pre-collect candidate IDs so lag-comp's ghost pass (§8.6) can
	// skip entities already present in the live candidate list.
	live := make(map[EntityID]struct{}, len(candidates))
	for _, e := range candidates {
		live[e.ID] = struct{}{}
	}
	for _, e := range candidates {
		if e.ID == 0 || e.ID == proj.ID || e.ID == shooterID {
			continue
		}
		if e.Kind == KindProjectile {
			continue
		}
		// Phase 3 §12.1: a snipe-fired projectile does not damage
		// other snipes or generators (its own lineage). We detect
		// "snipe-fired" by the projectileState lookup; resolveProjectile
		// receives shooterKind as a hint.
		if shooterKind == KindSnipe && (e.Kind == KindSnipe || e.Kind == KindGenerator) {
			continue
		}
		// Phase 4 §8.3: rewind position/flags if lag-comp engaged
		// and the candidate has a history sample at T_view; else
		// fall back to live state.
		ex, ey, eflags := e.X, e.Y, e.Flags
		if useLagComp {
			if h := lc.getHist(e.ID); h != nil {
				if s, ok := h.at(tView); ok {
					ex, ey, eflags = s.x, s.y, s.flags
				}
			}
		}
		if eflags&FlagDead != 0 {
			continue
		}
		// Spawn-invuln targets cannot be hit (Phase 3 §12.2);
		// Phase 4 §8.3 evaluates the flag at the rewound tick.
		if eflags&FlagSpawnInvuln != 0 {
			continue
		}
		eHe := entityHalfExt(e.Kind)
		ax := ex - eHe - he
		bx := ex + eHe + he
		ay := ey - eHe - he
		by := ey + eHe + he
		n, d, ok := segmentVsBox(x0, y0, vx, vy, ax, bx, ay, by)
		if !ok {
			continue
		}
		if !bestSet || n*bestDen < bestNum*d ||
			(n*bestDen == bestNum*d && e.ID < bestID) {
			bestNum, bestDen, bestID, bestSet = n, d, e.ID, true
		}
	}

	// Phase 4 §8.6: lag-comp ghost candidates — entities that are no
	// longer live (FlagDead in slab, or removed entirely) but whose
	// history sample at T_view is alive. The "shot around a corner"
	// case. Skipped for snipe shooters (gate) and for OWTTicks == 0.
	if useLagComp {
		bestNum, bestDen, bestID, bestSet = lagCompGhostPass(
			x0, y0, vx, vy, he, shooterID, tView, live, lc,
			bestNum, bestDen, bestID, bestSet,
		)
	}

	if !wallHit && !bestSet {
		return projHit{kind: projHitNone, endX: x0 + vx, endY: y0 + vy}
	}
	if !bestSet {
		return projHit{kind: projHitWall, endX: wx, endY: wy}
	}
	if !wallHit {
		ex := x0 + int32(int64(vx)*bestNum/bestDen)
		ey := y0 + int32(int64(vy)*bestNum/bestDen)
		return projHit{kind: projHitEntity, targetID: bestID, endX: ex, endY: ey}
	}
	// Both hit; wall preferred on tie.
	if wallNum*bestDen <= bestNum*wallDen {
		return projHit{kind: projHitWall, endX: wx, endY: wy}
	}
	ex := x0 + int32(int64(vx)*bestNum/bestDen)
	ey := y0 + int32(int64(vy)*bestNum/bestDen)
	return projHit{kind: projHitEntity, targetID: bestID, endX: ex, endY: ey}
}

// lagCompGhostPass tests projectile sweep against entities that are
// not in the live candidate list but whose history sample at T_view
// was alive (FlagDead/SpawnInvuln clear). Implements PHASE4.md §8.6
// "shot around a corner" semantics.
func lagCompGhostPass(
	x0, y0, vx, vy, he int32,
	shooterID EntityID,
	tView uint32,
	live map[EntityID]struct{},
	lc lagCompContext,
	bestNum, bestDen int64,
	bestID EntityID,
	bestSet bool,
) (int64, int64, EntityID, bool) {
	// Enumerate history-only IDs (live entities are tested in the
	// main loop). `live` is the SKIP set.
	for id, h := range lcAllHistories(lc) {
		if id == shooterID {
			continue
		}
		if _, isLive := live[id]; isLive {
			continue
		}
		s, ok := h.at(tView)
		if !ok {
			continue
		}
		if s.flags&FlagDead != 0 {
			continue
		}
		if s.flags&FlagSpawnInvuln != 0 {
			continue
		}
		// Projectiles never lag-comp-hit each other (mirror the live
		// candidate filter at collectHitCandidates). PHASE4 codex #4.
		if s.kind == KindProjectile {
			continue
		}
		// Ghost candidate is only valid for kinds with a real AABB.
		eHe := entityHalfExt(s.kind)
		if eHe == 0 {
			continue
		}
		ax := s.x - eHe - he
		bx := s.x + eHe + he
		ay := s.y - eHe - he
		by := s.y + eHe + he
		n, d, ok := segmentVsBox(x0, y0, vx, vy, ax, bx, ay, by)
		if !ok {
			continue
		}
		if !bestSet || n*bestDen < bestNum*d ||
			(n*bestDen == bestNum*d && id < bestID) {
			bestNum, bestDen, bestID, bestSet = n, d, id, true
		}
	}
	return bestNum, bestDen, bestID, bestSet
}

// lcAllHistories returns every entity-history entry visible to the
// lag-comp context. The lagCompContext.getHist callback is keyed on
// ID, so iteration requires a side-channel; the sim wires this via
// lc.getAllHistories (PHASE4.md §8.6 implementation note).
func lcAllHistories(lc lagCompContext) map[EntityID]*entityHistory {
	if lc.getAllHistories == nil {
		return nil
	}
	return lc.getAllHistories()
}

func entityHalfExt(k EntityKind) int32 {
	switch k {
	case KindPlayer:
		return playerHalfExt
	case KindGenerator:
		return generatorHalfExt
	case KindProjectile:
		return projectileHalfExt
	case KindSnipe:
		return snipeHalfExt
	}
	return 0
}

// segmentVsBox: point at (x0,y0) moving by (vx,vy) over one tick, vs
// axis-aligned box [ax, bx] × [ay, by]. Returns entry-time fraction
// tNum/tDen (with tDen > 0) if entry occurs in [0, 1], else ok=false.
func segmentVsBox(x0, y0, vx, vy, ax, bx, ay, by int32) (int64, int64, bool) {
	var tInNum, tInDen int64 = 0, 1
	var tOutNum, tOutDen int64 = 1, 1

	// X slab.
	if vx == 0 {
		if x0 < ax || x0 >= bx {
			return 0, 0, false
		}
	} else {
		var en, ed, xn, xd int64
		if vx > 0 {
			en = int64(ax - x0)
			ed = int64(vx)
			xn = int64(bx - x0)
			xd = int64(vx)
		} else {
			en = int64(bx - x0)
			ed = int64(vx)
			xn = int64(ax - x0)
			xd = int64(vx)
		}
		if ed < 0 {
			en, ed = -en, -ed
		}
		if xd < 0 {
			xn, xd = -xn, -xd
		}
		if en*tInDen > tInNum*ed {
			tInNum, tInDen = en, ed
		}
		if xn*tOutDen < tOutNum*xd {
			tOutNum, tOutDen = xn, xd
		}
	}

	// Y slab.
	if vy == 0 {
		if y0 < ay || y0 >= by {
			return 0, 0, false
		}
	} else {
		var en, ed, xn, xd int64
		if vy > 0 {
			en = int64(ay - y0)
			ed = int64(vy)
			xn = int64(by - y0)
			xd = int64(vy)
		} else {
			en = int64(by - y0)
			ed = int64(vy)
			xn = int64(ay - y0)
			xd = int64(vy)
		}
		if ed < 0 {
			en, ed = -en, -ed
		}
		if xd < 0 {
			xn, xd = -xn, -xd
		}
		if en*tInDen > tInNum*ed {
			tInNum, tInDen = en, ed
		}
		if xn*tOutDen < tOutNum*xd {
			tOutNum, tOutDen = xn, xd
		}
	}

	// tIn ≤ tOut?
	if tInNum*tOutDen > tOutNum*tInDen {
		return 0, 0, false
	}
	// Entry within this tick.
	if tInNum > tInDen {
		return 0, 0, false
	}
	if tInNum < 0 {
		tInNum = 0
	}
	return tInNum, tInDen, true
}
