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

// resolveProjectile performs the §10.2 unified swept-AABB pass for one
// projectile against the maze walls and against every candidate entity
// (already filtered by caller to exclude the shooter, the projectile
// itself, other projectiles, and dead entities). Returns the outcome.
//
// AIDEV-NOTE: the wall sweep here reuses moveAndSlide for the *end*
// position; the entry-time fraction we compare against entity hits is
// approximated as ((clamped_pos - origin) / velocity) on whichever
// axis got clamped. Per-tick motion is ≤ 32 subtiles and the dominant
// axis ordering is X-then-Y; this is sufficient for the §10.2 "earliest
// impact wins, wall preferred on tie" rule within ±1 subtile.
func resolveProjectile(m *maze, proj *Entity, shooterID EntityID, candidates []*Entity) projHit {
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
	for _, e := range candidates {
		if e.ID == 0 || e.ID == proj.ID || e.ID == shooterID {
			continue
		}
		if e.Kind == KindProjectile {
			continue
		}
		if e.Flags&FlagDead != 0 {
			continue
		}
		eHe := entityHalfExt(e.Kind)
		ax := e.X - eHe - he
		bx := e.X + eHe + he
		ay := e.Y - eHe - he
		by := e.Y + eHe + he
		n, d, ok := segmentVsBox(x0, y0, vx, vy, ax, bx, ay, by)
		if !ok {
			continue
		}
		if !bestSet || n*bestDen < bestNum*d ||
			(n*bestDen == bestNum*d && e.ID < bestID) {
			bestNum, bestDen, bestID, bestSet = n, d, e.ID, true
		}
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

func entityHalfExt(k EntityKind) int32 {
	switch k {
	case KindPlayer:
		return playerHalfExt
	case KindGenerator:
		return generatorHalfExt
	case KindProjectile:
		return projectileHalfExt
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
