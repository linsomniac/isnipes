package sim

// HasLOS reports whether a tile-grid raycast from (sx, sy) to (tx, ty)
// is unblocked by WALL tiles. The endpoints are excluded from the
// wall check (the snipe stands on a floor tile by construction, and a
// floor target is the player's tile).
//
// Chebyshev distance > maxRange short-circuits to false regardless of
// intervening tiles.
//
// PHASE3.md §9 — integer-only supercover Bresenham. A diagonal step
// across a "wall corner" (one of the two perpendicular neighbours is
// a wall) blocks LOS, matching the supercover variant.
func HasLOS(m *maze, sx, sy, tx, ty, maxRange int) bool {
	if sx == tx && sy == ty {
		return true
	}
	dx := tx - sx
	dy := ty - sy
	ax := dx
	if ax < 0 {
		ax = -ax
	}
	ay := dy
	if ay < 0 {
		ay = -ay
	}
	cheb := ax
	if ay > cheb {
		cheb = ay
	}
	if cheb > maxRange {
		return false
	}

	stepX := 1
	if dx < 0 {
		stepX = -1
	}
	if dx == 0 {
		stepX = 0
	}
	stepY := 1
	if dy < 0 {
		stepY = -1
	}
	if dy == 0 {
		stepY = 0
	}

	// Supercover walk. Standard Amanatides–Woo style without floats:
	// we step from (sx, sy) to (tx, ty), preferring the axis whose
	// accumulated error is smaller. The accumulators are tDeltaX = ay
	// and tDeltaY = ax (the perpendicular axis's step "rate").
	x, y := sx, sy
	tMaxX := ax
	tMaxY := ay
	for !(x == tx && y == ty) {
		if tMaxX < tMaxY {
			x += stepX
			tMaxX += ay << 1
		} else if tMaxY < tMaxX {
			y += stepY
			tMaxY += ax << 1
		} else {
			// Perfect diagonal — supercover check: both
			// perpendicular neighbours must be non-wall, else block.
			nbrA := m.at(x+stepX, y)
			nbrB := m.at(x, y+stepY)
			if nbrA == TileWall && nbrB == TileWall {
				return false
			}
			x += stepX
			y += stepY
			tMaxX += ay << 1
			tMaxY += ax << 1
		}
		// Don't check the endpoints; only intervening tiles.
		if x == tx && y == ty {
			break
		}
		if m.at(x, y) == TileWall {
			return false
		}
	}
	return true
}
