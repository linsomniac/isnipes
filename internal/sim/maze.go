package sim

import (
	"math/rand/v2"
)

// maze is the dense 2-D tile array. Indexed [y*W + x].
type maze struct {
	W, H  int
	tiles []Tile
}

func newMaze(w, h int) *maze {
	return &maze{W: w, H: h, tiles: make([]Tile, w*h)}
}

func (m *maze) at(x, y int) Tile {
	if x < 0 || x >= m.W || y < 0 || y >= m.H {
		return TileWall
	}
	return m.tiles[y*m.W+x]
}

func (m *maze) set(x, y int, t Tile) {
	m.tiles[y*m.W+x] = t
}

// generateResult holds the outputs of generateMaze.
type generateResult struct {
	m              *maze
	playerSpawns   []tilePos // ordered: kth playerID lives at acceptedSpawns[k]
	extraSpawns    []tilePos // extras beyond len(PlayerIDs), for respawn pool
	generatorTiles []tilePos // every TileSpawnGenerator location
}

type tilePos struct{ X, Y int }

// cellPos is a coarse-cell coordinate in the wide-corridor maze grid.
type cellPos struct{ CX, CY int }

// Wide-corridor maze parameters (MAZE_REVAMP.md §2). The maze is carved on a
// coarse cell grid: each cell is a corridorWidth×corridorWidth floor block and
// adjacent cells are separated by a 1-tile wall, so the cell pitch is
// corridorWidth+1. A recursive-backtracker spanning carve is then braided
// (loops) and a few 2×2 chambers are merged for size variety.
const (
	mazePitch     = corridorWidth + 1 // corridor floor + one thin wall
	mazeBraidPct  = 18                // % of closed inter-cell walls reopened as loops
	mazeRoomCount = 5                 // 2×2 cell chambers merged for size variety
	genCellGap    = 2                 // min Chebyshev cell distance between generators
	spawnCellGap  = 2                 // min Chebyshev cell distance: spawn↔spawn and spawn↔generator
)

// generateMaze runs the pipeline with up to 3 retry attempts using the §7.6
// seed-perturbation scheme.
func generateMaze(cfg Config) (*generateResult, error) {
	const maxAttempts = 4 // 1 initial + 3 retries
	var seed uint32 = cfg.Seed
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			seed = cfg.Seed ^ uint32(0xDEADBEEF*uint64(attempt))
		}
		res, err := generateMazeOnce(seed, cfg)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// generateMazeOnce runs one full attempt of the wide-corridor pipeline. PRNG
// consumption order is fixed for determinism: carve → braid → chambers →
// generators → spawns.
func generateMazeOnce(seed uint32, cfg Config) (*generateResult, error) {
	pcg := newMazePCG(seed)
	r := rand.New(pcg)
	W, H := cfg.Width, cfg.Height
	m := newMaze(W, H)
	// All tiles begin as TileWall by zero value (incl. the outer ring and any
	// leftover tiles past the last cell on the far edges).

	cellsX := (W - 1) / mazePitch
	cellsY := (H - 1) / mazePitch
	if cellsX < 2 || cellsY < 2 {
		return nil, errMazeRetry
	}

	carveWideMaze(m, r, cellsX, cellsY)

	playerCount := len(cfg.PlayerIDs)
	generatorCount := computeGeneratorCount(playerCount, W, H, cfg.NoGenerators)

	gens, ok := placeGenerators(r, cellsX, cellsY, generatorCount)
	if !ok {
		return nil, errMazeRetry
	}
	for _, g := range gens {
		m.set(g.X, g.Y, TileSpawnGenerator)
	}

	playerSpawns, extras, ok := placePlayerSpawns(r, cellsX, cellsY, playerCount, gens)
	if !ok {
		return nil, errMazeRetry
	}
	for _, s := range playerSpawns {
		m.set(s.X, s.Y, TileSpawnPlayer)
	}
	for _, s := range extras {
		m.set(s.X, s.Y, TileSpawnPlayer)
	}

	if !connectivityOK(m, playerSpawns) {
		return nil, errMazeRetry
	}

	return &generateResult{
		m:              m,
		playerSpawns:   playerSpawns,
		extraSpawns:    extras,
		generatorTiles: gens,
	}, nil
}

// errMazeRetry is the internal sentinel asking generateMaze() to retry.
type retryErr struct{}

func (retryErr) Error() string { return "isnipes/sim: maze retry" }

var errMazeRetry error = retryErr{}

// computeGeneratorCount derives the generator count from the interior cell
// count (MAZE_REVAMP.md §2.1), capped so Poisson-disk placement at genCellGap
// can satisfy it.
func computeGeneratorCount(playerCount, W, H int, noGen bool) int {
	if noGen {
		return 0
	}
	cellsX := (W - 1) / mazePitch
	cellsY := (H - 1) / mazePitch
	interiorCells := maxInt(0, cellsX-2) * maxInt(0, cellsY-2)
	genCap := maxInt(2, interiorCells/4)
	naive := maxInt(2, playerCount) + 1
	return maxInt(2, minInt(naive, genCap))
}

// --- cell ↔ tile helpers ---

// cellOrigin returns the top-left FLOOR tile of a cell's corridor block.
func cellOrigin(c cellPos) (int, int) {
	return c.CX*mazePitch + 1, c.CY*mazePitch + 1
}

// cellCenter returns the tile at the centre of a cell (always FLOOR after
// carving) — where a generator/spawn marker is placed.
func cellCenter(c cellPos) tilePos {
	ox, oy := cellOrigin(c)
	return tilePos{ox + corridorWidth/2, oy + corridorWidth/2}
}

// tileToCell maps a cell-centre tile back to its cell coordinate.
func tileToCell(t tilePos) cellPos {
	return cellPos{(t.X - 1) / mazePitch, (t.Y - 1) / mazePitch}
}

// carveCell fills a cell's corridorWidth×corridorWidth block with floor.
func carveCell(m *maze, c cellPos) {
	ox, oy := cellOrigin(c)
	for yy := 0; yy < corridorWidth; yy++ {
		for xx := 0; xx < corridorWidth; xx++ {
			m.set(ox+xx, oy+yy, TileFloor)
		}
	}
}

// carveLink opens the 1-tile wall strip between two orthogonally adjacent
// cells (a is the reference; b is N/E/S/W of it).
func carveLink(m *maze, a, b cellPos) {
	ax, ay := cellOrigin(a)
	switch {
	case b.CX == a.CX+1:
		for k := 0; k < corridorWidth; k++ {
			m.set(ax+corridorWidth, ay+k, TileFloor)
		}
	case b.CX == a.CX-1:
		for k := 0; k < corridorWidth; k++ {
			m.set(ax-1, ay+k, TileFloor)
		}
	case b.CY == a.CY+1:
		for k := 0; k < corridorWidth; k++ {
			m.set(ax+k, ay+corridorWidth, TileFloor)
		}
	case b.CY == a.CY-1:
		for k := 0; k < corridorWidth; k++ {
			m.set(ax+k, ay-1, TileFloor)
		}
	}
}

// linked reports whether the wall strip between a and adjacent b is open.
func linked(m *maze, a, b cellPos) bool {
	ax, ay := cellOrigin(a)
	switch {
	case b.CX == a.CX+1:
		return m.at(ax+corridorWidth, ay) == TileFloor
	case b.CX == a.CX-1:
		return m.at(ax-1, ay) == TileFloor
	case b.CY == a.CY+1:
		return m.at(ax, ay+corridorWidth) == TileFloor
	default:
		return m.at(ax, ay-1) == TileFloor
	}
}

var cellDirs = [4]cellPos{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// carveWideMaze carves a recursive-backtracker spanning maze over the cell
// grid, then braids in loops and merges a few chambers. The outer ring and
// every inter-cell wall stay exactly 1 tile thick.
func carveWideMaze(m *maze, r *rand.Rand, cellsX, cellsY int) {
	inb := func(c cellPos) bool { return c.CX >= 0 && c.CY >= 0 && c.CX < cellsX && c.CY < cellsY }
	visited := make([]bool, cellsX*cellsY)
	idx := func(c cellPos) int { return c.CY*cellsX + c.CX }

	// Recursive backtracker (explicit stack) from a PRNG-chosen start cell.
	start := cellPos{r.IntN(cellsX), r.IntN(cellsY)}
	visited[idx(start)] = true
	carveCell(m, start)
	stack := []cellPos{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		var nbrs []cellPos
		for _, d := range cellDirs {
			n := cellPos{cur.CX + d.CX, cur.CY + d.CY}
			if inb(n) && !visited[idx(n)] {
				nbrs = append(nbrs, n)
			}
		}
		if len(nbrs) == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		pick := nbrs[r.IntN(len(nbrs))]
		visited[idx(pick)] = true
		carveCell(m, pick)
		carveLink(m, cur, pick)
		stack = append(stack, pick)
	}

	// Braid: reopen a fraction of still-closed E/S walls to create loops so
	// the maze is multiply-connected (escape routes), not a pure tree.
	for cy := 0; cy < cellsY; cy++ {
		for cx := 0; cx < cellsX; cx++ {
			a := cellPos{cx, cy}
			for _, b := range []cellPos{{cx + 1, cy}, {cx, cy + 1}} {
				if inb(b) && !linked(m, a, b) && r.IntN(100) < mazeBraidPct {
					carveLink(m, a, b)
				}
			}
		}
	}

	// Chambers: merge a few 2×2 cell blocks into open rooms / nests.
	for i := 0; i < mazeRoomCount; i++ {
		cx := r.IntN(cellsX - 1)
		cy := r.IntN(cellsY - 1)
		tl := cellPos{cx, cy}
		tr := cellPos{cx + 1, cy}
		bl := cellPos{cx, cy + 1}
		br := cellPos{cx + 1, cy + 1}
		for _, c := range []cellPos{tl, tr, bl, br} {
			carveCell(m, c)
		}
		carveLink(m, tl, tr)
		carveLink(m, bl, br)
		carveLink(m, tl, bl)
		carveLink(m, tr, br)
	}
}

// placeGenerators chooses interior cells ≥ genCellGap apart (Chebyshev in cell
// coords) and returns their centre tiles (MAZE_REVAMP.md §2.1).
func placeGenerators(r *rand.Rand, cellsX, cellsY, count int) ([]tilePos, bool) {
	if count == 0 {
		return nil, true
	}
	// Interior cells (skip the outer ring) so generators sit in the maze body,
	// clear of perimeter spawns. Fall back to all cells on tiny grids.
	loX, hiX := 1, cellsX-2
	loY, hiY := 1, cellsY-2
	if hiX < loX || hiY < loY {
		loX, hiX, loY, hiY = 0, cellsX-1, 0, cellsY-1
	}
	var cands []cellPos
	for cy := loY; cy <= hiY; cy++ {
		for cx := loX; cx <= hiX; cx++ {
			cands = append(cands, cellPos{cx, cy})
		}
	}
	shuffleCells(cands, r)

	var accepted []cellPos
	out := make([]tilePos, 0, count)
	for _, c := range cands {
		ok := true
		for _, a := range accepted {
			if chebyshevCell(c, a) < genCellGap {
				ok = false
				break
			}
		}
		if ok {
			accepted = append(accepted, c)
			out = append(out, cellCenter(c))
			if len(out) == count {
				return out, true
			}
		}
	}
	return nil, false
}

// placePlayerSpawns chooses spawn cells ≥ spawnCellGap apart, preferring the
// perimeter ring and keeping ≥ spawnCellGap from generators where the map
// allows. On small/crowded maps (where the perimeter hugs the interior
// generators) it relaxes the spawn↔generator gap to ≥1 cell and may place
// inward. Returns the first playerCount as ordered spawns; any extras feed the
// respawn pool (MAZE_REVAMP.md §2.2).
func placePlayerSpawns(r *rand.Rand, cellsX, cellsY, playerCount int, gens []tilePos) ([]tilePos, []tilePos, bool) {
	genCells := make([]cellPos, len(gens))
	for i, g := range gens {
		genCells[i] = tileToCell(g)
	}
	// Candidates: perimeter ring first (preferred, §3.1), then interior; each
	// shuffled, so greedy placement favours the perimeter but can fall inward.
	var ring, inner []cellPos
	for cy := 0; cy < cellsY; cy++ {
		for cx := 0; cx < cellsX; cx++ {
			c := cellPos{cx, cy}
			if cx == 0 || cy == 0 || cx == cellsX-1 || cy == cellsY-1 {
				ring = append(ring, c)
			} else {
				inner = append(inner, c)
			}
		}
	}
	shuffleCells(ring, r)
	shuffleCells(inner, r)
	cands := make([]cellPos, 0, len(ring)+len(inner))
	cands = append(cands, ring...)
	cands = append(cands, inner...)

	target := playerCount + 2 // +2 extras for the respawn pool
	greedy := func(genGap int) []tilePos {
		var accepted []cellPos
		out := make([]tilePos, 0, target)
		for _, c := range cands {
			ok := true
			for _, a := range accepted {
				if chebyshevCell(c, a) < spawnCellGap {
					ok = false
					break
				}
			}
			if ok {
				for _, g := range genCells {
					if chebyshevCell(c, g) < genGap {
						ok = false
						break
					}
				}
			}
			if ok {
				accepted = append(accepted, c)
				out = append(out, cellCenter(c))
				if len(out) == target {
					break
				}
			}
		}
		return out
	}

	// Strict (spawn↔gen ≥ spawnCellGap) → relaxed (≥1 cell, i.e. never the
	// same cell as a generator). spawn↔spawn stays ≥ spawnCellGap throughout.
	out := greedy(spawnCellGap)
	if len(out) < playerCount {
		out = greedy(1)
	}
	if len(out) < playerCount {
		return nil, nil, false
	}
	if len(out) > playerCount {
		return out[:playerCount], append([]tilePos(nil), out[playerCount:]...), true
	}
	return out, nil, true
}

func connectivityOK(m *maze, spawns []tilePos) bool {
	if len(spawns) == 0 {
		return false
	}
	W, H := m.W, m.H
	walkable := func(t Tile) bool {
		return t == TileFloor || t == TileSpawnPlayer || t == TileSpawnGenerator
	}
	visited := make([]bool, W*H)
	queue := make([]tilePos, 0, W*H)
	start := spawns[0]
	queue = append(queue, start)
	visited[start.Y*W+start.X] = true
	dirs := [4]tilePos{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		for _, d := range dirs {
			nx, ny := c.X+d.X, c.Y+d.Y
			if nx < 0 || nx >= W || ny < 0 || ny >= H {
				continue
			}
			if visited[ny*W+nx] {
				continue
			}
			if !walkable(m.at(nx, ny)) {
				continue
			}
			visited[ny*W+nx] = true
			queue = append(queue, tilePos{nx, ny})
		}
	}
	for ty := 0; ty < H; ty++ {
		for tx := 0; tx < W; tx++ {
			t := m.at(tx, ty)
			if walkable(t) && !visited[ty*W+tx] {
				return false
			}
		}
	}
	return true
}

func shuffleCells(s []cellPos, r *rand.Rand) {
	// Fisher–Yates using r.IntN.
	for i := len(s) - 1; i > 0; i-- {
		j := r.IntN(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

// chebyshev is the tile-space Chebyshev distance (used by maze tests).
func chebyshev(a, b tilePos) int {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

func chebyshevCell(a, b cellPos) int {
	dx := a.CX - b.CX
	if dx < 0 {
		dx = -dx
	}
	dy := a.CY - b.CY
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
