package sim

// pathDir is the first-step direction recorded for each frontier
// entry. Values mirror the §7.3 fixed N/E/S/W order.
type pathDir uint8

const (
	pathDirNone pathDir = 0
	pathDirN    pathDir = 1
	pathDirE    pathDir = 2
	pathDirS    pathDir = 3
	pathDirW    pathDir = 4
)

// PathNext returns the next tile in the BFS-optimal 4-neighborhood
// path from (sx, sy) to (tx, ty), treating WALL tiles as blocked
// and SpawnPlayer/SpawnGenerator/Floor tiles as walkable.
// Returns (nx, ny, true) on success or (0, 0, false) if no path
// exists within maxDepth tiles.
//
// PHASE3.md §8 — neighbour iteration order is fixed [N, E, S, W] per
// P1 §7.3 for cross-replay determinism. Ties in BFS first-discover
// time break consistent with that order.
func PathNext(m *maze, sx, sy, tx, ty, maxDepth int) (int, int, bool) {
	if sx == tx && sy == ty {
		return sx, sy, true
	}
	W, H := m.W, m.H
	if sx < 0 || sx >= W || sy < 0 || sy >= H {
		return 0, 0, false
	}
	if tx < 0 || tx >= W || ty < 0 || ty >= H {
		return 0, 0, false
	}
	if m.at(tx, ty) == TileWall {
		return 0, 0, false
	}

	// Visited + first-step direction tracking.
	visited := make([]pathDir, W*H)
	// Mark source as visited (any non-None value works; we use a sentinel).
	visited[sy*W+sx] = pathDirNone + 1

	type frontierEntry struct {
		x, y, depth int
		first       pathDir
	}
	queue := make([]frontierEntry, 0, 256)

	// Seed: enumerate N/E/S/W from source. The first step *is* the
	// direction we record on the seeded entry.
	dirs := [4]struct {
		dx, dy int
		first  pathDir
	}{
		{0, -1, pathDirN},
		{1, 0, pathDirE},
		{0, 1, pathDirS},
		{-1, 0, pathDirW},
	}
	for _, d := range dirs {
		nx, ny := sx+d.dx, sy+d.dy
		if nx < 0 || nx >= W || ny < 0 || ny >= H {
			continue
		}
		if m.at(nx, ny) == TileWall {
			continue
		}
		if visited[ny*W+nx] != pathDirNone {
			continue
		}
		visited[ny*W+nx] = d.first
		if nx == tx && ny == ty {
			return nx, ny, true
		}
		queue = append(queue, frontierEntry{nx, ny, 1, d.first})
	}

	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if f.depth >= maxDepth {
			continue
		}
		for _, d := range dirs {
			nx, ny := f.x+d.dx, f.y+d.dy
			if nx < 0 || nx >= W || ny < 0 || ny >= H {
				continue
			}
			if m.at(nx, ny) == TileWall {
				continue
			}
			if visited[ny*W+nx] != pathDirNone {
				continue
			}
			visited[ny*W+nx] = f.first
			if nx == tx && ny == ty {
				// Return the FIRST step from the source; the caller
				// turns the dir into the next-tile coordinate.
				return firstStepCoords(sx, sy, f.first)
			}
			queue = append(queue, frontierEntry{nx, ny, f.depth + 1, f.first})
		}
	}
	return 0, 0, false
}

func firstStepCoords(sx, sy int, d pathDir) (int, int, bool) {
	switch d {
	case pathDirN:
		return sx, sy - 1, true
	case pathDirE:
		return sx + 1, sy, true
	case pathDirS:
		return sx, sy + 1, true
	case pathDirW:
		return sx - 1, sy, true
	}
	return 0, 0, false
}
