package sim

// diag returns the diagonal component for cardinal speed s, per §9.2.
// Hard-coded equivalent of (s*181)/256.
func diag(s int32) int32 {
	return (s * 181) / 256
}

// velocityFor returns the (vx, vy) for direction d at cardinal speed s.
func velocityFor(d Dir, s int32) (int32, int32) {
	switch d {
	case DirN:
		return 0, -s
	case DirNE:
		return diag(s), -diag(s)
	case DirE:
		return s, 0
	case DirSE:
		return diag(s), diag(s)
	case DirS:
		return 0, s
	case DirSW:
		return -diag(s), diag(s)
	case DirW:
		return -s, 0
	case DirNW:
		return -diag(s), -diag(s)
	default:
		return 0, 0
	}
}

// aabb represents a generic axis-aligned bounding box centred on
// (cx, cy) with half-extent he.
type aabb struct {
	cx, cy int32
	he     int32
}

func (a aabb) overlaps(b aabb) bool {
	if a.cx+a.he <= b.cx-b.he {
		return false
	}
	if b.cx+b.he <= a.cx-a.he {
		return false
	}
	if a.cy+a.he <= b.cy-b.he {
		return false
	}
	if b.cy+b.he <= a.cy-a.he {
		return false
	}
	return true
}

// moveAndSlide applies axis-separated swept-AABB collision against
// walls and an optional list of solid AABBs (generators in §9.4).
// Returns the new (x, y, vx, vy).
//
// The wall check uses the maze's tile grid. The "solids" check is the
// generator-as-pseudo-tile path in §9.4: after the per-axis wall sweep,
// test the entity's new AABB against each solid; on overlap, clamp on
// the axis being swept and zero its velocity.
func moveAndSlide(m *maze, x, y, vx, vy, halfExt int32, solids []aabb) (int32, int32, int32, int32) {
	// X axis.
	if vx > 0 {
		leadX := x + halfExt + vx
		col := leadX / subtilePerTile
		rowLo := (y - halfExt) / subtilePerTile
		rowHi := (y + halfExt - 1) / subtilePerTile
		hit := false
		for r := rowLo; r <= rowHi; r++ {
			if m.at(int(col), int(r)) == TileWall {
				hit = true
				break
			}
		}
		if hit {
			x = col*subtilePerTile - halfExt - 1
			vx = 0
		} else {
			x += vx
		}
		// Solid collision on X-axis.
		if len(solids) > 0 {
			ent := aabb{cx: x, cy: y, he: halfExt}
			for _, s := range solids {
				if !ent.overlaps(s) {
					continue
				}
				// Clamp to the left side of the solid.
				x = s.cx - s.he - halfExt - 1
				vx = 0
				ent = aabb{cx: x, cy: y, he: halfExt}
			}
		}
	} else if vx < 0 {
		leadX := x - halfExt + vx
		col := leadX / subtilePerTile
		rowLo := (y - halfExt) / subtilePerTile
		rowHi := (y + halfExt - 1) / subtilePerTile
		hit := false
		for r := rowLo; r <= rowHi; r++ {
			if m.at(int(col), int(r)) == TileWall {
				hit = true
				break
			}
		}
		if hit {
			x = (col+1)*subtilePerTile + halfExt
			vx = 0
		} else {
			x += vx
		}
		if len(solids) > 0 {
			ent := aabb{cx: x, cy: y, he: halfExt}
			for _, s := range solids {
				if !ent.overlaps(s) {
					continue
				}
				x = s.cx + s.he + halfExt + 1
				vx = 0
				ent = aabb{cx: x, cy: y, he: halfExt}
			}
		}
	}

	// Y axis.
	if vy > 0 {
		leadY := y + halfExt + vy
		row := leadY / subtilePerTile
		colLo := (x - halfExt) / subtilePerTile
		colHi := (x + halfExt - 1) / subtilePerTile
		hit := false
		for c := colLo; c <= colHi; c++ {
			if m.at(int(c), int(row)) == TileWall {
				hit = true
				break
			}
		}
		if hit {
			y = row*subtilePerTile - halfExt - 1
			vy = 0
		} else {
			y += vy
		}
		if len(solids) > 0 {
			ent := aabb{cx: x, cy: y, he: halfExt}
			for _, s := range solids {
				if !ent.overlaps(s) {
					continue
				}
				y = s.cy - s.he - halfExt - 1
				vy = 0
				ent = aabb{cx: x, cy: y, he: halfExt}
			}
		}
	} else if vy < 0 {
		leadY := y - halfExt + vy
		row := leadY / subtilePerTile
		colLo := (x - halfExt) / subtilePerTile
		colHi := (x + halfExt - 1) / subtilePerTile
		hit := false
		for c := colLo; c <= colHi; c++ {
			if m.at(int(c), int(row)) == TileWall {
				hit = true
				break
			}
		}
		if hit {
			y = (row+1)*subtilePerTile + halfExt
			vy = 0
		} else {
			y += vy
		}
		if len(solids) > 0 {
			ent := aabb{cx: x, cy: y, he: halfExt}
			for _, s := range solids {
				if !ent.overlaps(s) {
					continue
				}
				y = s.cy + s.he + halfExt + 1
				vy = 0
				ent = aabb{cx: x, cy: y, he: halfExt}
			}
		}
	}

	return x, y, vx, vy
}
