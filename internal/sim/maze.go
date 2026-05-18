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

// generateMaze runs the whole §7 pipeline with up to 3 retry attempts
// using the §7.6 seed-perturbation scheme. The retry budget covers
// §7.4, §7.6 and §7.8 failure paths collectively per §7.4 step 3.
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

// generateMazeOnce runs one full attempt of the §7 pipeline.
func generateMazeOnce(seed uint32, cfg Config) (*generateResult, error) {
	pcg := newMazePCG(seed)
	r := rand.New(pcg)
	W, H := cfg.Width, cfg.Height
	m := newMaze(W, H)
	// All tiles begin as TileWall by zero value.

	// 7.3 growing-tree carving (consumes PRNG first).
	carveGrowingTree(m, r)

	// 7.4 room carving.
	if !carveRooms(m, r) {
		return nil, errMazeRetry
	}

	// 7.5 doorway insertion.
	insertDoorways(m, r)

	// 7.7's perimeter band depends on H. §7.0.1 genMargin = perimeterBand+1.
	perimeterBand := maxInt(3, H/8)
	genMargin := perimeterBand + 1

	// Generator count formula (§7.0.1).
	playerCount := len(cfg.PlayerIDs)
	generatorCount := computeGeneratorCount(playerCount, W, H, perimeterBand, cfg.NoGenerators)

	// 7.6 generator placement.
	gens, ok := placeGenerators(m, r, genMargin, generatorCount)
	if !ok {
		return nil, errMazeRetry
	}
	for _, g := range gens {
		m.set(g.X, g.Y, TileSpawnGenerator)
	}

	// 7.7 player spawn placement.
	playerSpawns, extras, ok := placePlayerSpawns(m, r, perimeterBand, playerCount, gens)
	if !ok {
		return nil, errMazeRetry
	}
	for _, s := range playerSpawns {
		m.set(s.X, s.Y, TileSpawnPlayer)
	}
	for _, s := range extras {
		m.set(s.X, s.Y, TileSpawnPlayer)
	}

	// 7.8 connectivity validation.
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

// computeGeneratorCount implements the §7.0.1 formula.
func computeGeneratorCount(playerCount, W, H, perimeterBand int, noGen bool) int {
	if noGen {
		return 0
	}
	genMargin := perimeterBand + 1
	gInteriorW := maxInt(0, W-2*genMargin)
	gInteriorH := maxInt(0, H-2*genMargin)
	genInterior := gInteriorW * gInteriorH
	generatorCap := maxInt(2, genInterior/144)
	naive := maxInt(2, playerCount) + 1
	return maxInt(2, minInt(naive, generatorCap))
}

func carveGrowingTree(m *maze, r *rand.Rand) {
	W, H := m.W, m.H
	cellCols := (W - 1) / 2
	cellRows := (H - 1) / 2

	// Mark the start cell as floor; push onto frontier.
	startX, startY := 1, 1
	m.set(startX, startY, TileFloor)
	type cell struct{ cx, cy int }
	frontier := make([]cell, 0, cellCols*cellRows)
	frontier = append(frontier, cell{0, 0})

	// Neighbor offsets fixed as [N, E, S, W] per §7.3.
	type dxy struct{ dx, dy int }
	nbrs := [4]dxy{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

	for len(frontier) > 0 {
		fr := r.Float64()
		var idx int
		if fr < 0.6 {
			idx = len(frontier) - 1
		} else {
			idx = r.IntN(len(frontier))
		}
		c := frontier[idx]
		// Filter cells still walled in.
		var candidates [4]int
		nc := 0
		for i, n := range nbrs {
			ncx := c.cx + n.dx
			ncy := c.cy + n.dy
			if ncx < 0 || ncx >= cellCols || ncy < 0 || ncy >= cellRows {
				continue
			}
			tx := 2*ncx + 1
			ty := 2*ncy + 1
			if m.at(tx, ty) == TileWall {
				candidates[nc] = i
				nc++
			}
		}
		if nc == 0 {
			// Swap-with-last and pop.
			frontier[idx] = frontier[len(frontier)-1]
			frontier = frontier[:len(frontier)-1]
			continue
		}
		pick := candidates[r.IntN(nc)]
		n := nbrs[pick]
		ncx := c.cx + n.dx
		ncy := c.cy + n.dy
		ntx := 2*ncx + 1
		nty := 2*ncy + 1
		wallTx := 2*c.cx + 1 + n.dx
		wallTy := 2*c.cy + 1 + n.dy
		m.set(ntx, nty, TileFloor)
		m.set(wallTx, wallTy, TileFloor)
		frontier = append(frontier, cell{ncx, ncy})
	}
}

func carveRooms(m *maze, r *rand.Rand) bool {
	W, H := m.W, m.H
	roomCount := 5 + r.IntN(6) // 5..10
	type rect struct{ x, y, w, h int }
	accepted := make([]rect, 0, roomCount)
	attemptBudget := 64 * roomCount
	for attempt := 0; attempt < attemptBudget && len(accepted) < roomCount; attempt++ {
		w := 4 + r.IntN(5) // 4..8
		h := 4 + r.IntN(5)
		rx := 2 + r.IntN(W-2-w-1) // [2, W-2-w]
		ry := 2 + r.IntN(H-2-h-1)
		cand := rect{rx, ry, w, h}
		// Reject if overlaps any accepted room (1-tile margin).
		bad := false
		for _, ar := range accepted {
			if rectsOverlapWithMargin(cand, ar, 1) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		// Carve.
		for yy := cand.y; yy < cand.y+cand.h; yy++ {
			for xx := cand.x; xx < cand.x+cand.w; xx++ {
				m.set(xx, yy, TileFloor)
			}
		}
		accepted = append(accepted, cand)
	}
	return len(accepted) >= 5
}

func rectsOverlapWithMargin(a, b struct{ x, y, w, h int }, margin int) bool {
	if a.x+a.w+margin <= b.x {
		return false
	}
	if b.x+b.w+margin <= a.x {
		return false
	}
	if a.y+a.h+margin <= b.y {
		return false
	}
	if b.y+b.h+margin <= a.y {
		return false
	}
	return true
}

func insertDoorways(m *maze, r *rand.Rand) {
	const pDoor = 0.75
	W, H := m.W, m.H
	for ty := 1; ty < H-1; ty++ {
		for tx := 1; tx < W-1; tx++ {
			// We need this tile to be a wall on an even coordinate
			// (horizontal even, or vertical even — at most one of
			// (tx, ty) is even for a "wall between cells" tile).
			if m.at(tx, ty) != TileWall {
				continue
			}
			even := false
			if tx%2 == 0 && ty%2 == 1 {
				// Vertical wall between (tx-1, ty) and (tx+1, ty).
				if m.at(tx-1, ty) == TileFloor && m.at(tx+1, ty) == TileFloor {
					even = true
				}
			} else if ty%2 == 0 && tx%2 == 1 {
				// Horizontal wall between (tx, ty-1) and (tx, ty+1).
				if m.at(tx, ty-1) == TileFloor && m.at(tx, ty+1) == TileFloor {
					even = true
				}
			}
			if !even {
				continue
			}
			if r.Float64() < pDoor {
				m.set(tx, ty, TileFloor)
			}
		}
	}
}

// placeGenerators implements §7.6 with the Chebyshev ≥ 12 rule.
func placeGenerators(m *maze, r *rand.Rand, genMargin, generatorCount int) ([]tilePos, bool) {
	if generatorCount == 0 {
		return nil, true
	}
	W, H := m.W, m.H
	candidates := make([]tilePos, 0, 256)
	for ty := genMargin; ty <= H-1-genMargin; ty++ {
		for tx := genMargin; tx <= W-1-genMargin; tx++ {
			if m.at(tx, ty) == TileFloor {
				candidates = append(candidates, tilePos{tx, ty})
			}
		}
	}
	shuffleTilePos(candidates, r)

	accepted := make([]tilePos, 0, generatorCount)
	for _, c := range candidates {
		ok := true
		for _, a := range accepted {
			if chebyshev(c, a) < 12 {
				ok = false
				break
			}
		}
		if ok {
			accepted = append(accepted, c)
			if len(accepted) == generatorCount {
				break
			}
		}
	}
	if len(accepted) < generatorCount {
		return nil, false
	}
	return accepted, true
}

// placePlayerSpawns implements §7.7.
func placePlayerSpawns(m *maze, r *rand.Rand, perimeterBand, playerCount int, gens []tilePos) ([]tilePos, []tilePos, bool) {
	W, H := m.W, m.H
	candidates := make([]tilePos, 0, 512)
	for ty := 1; ty < H-1; ty++ {
		for tx := 1; tx < W-1; tx++ {
			if m.at(tx, ty) != TileFloor {
				continue
			}
			// Chebyshev distance from outer wall.
			d := minInt(minInt(tx, ty), minInt(W-1-tx, H-1-ty))
			if d <= perimeterBand {
				candidates = append(candidates, tilePos{tx, ty})
			}
		}
	}
	shuffleTilePos(candidates, r)

	target := playerCount + 2
	tryWithDistance := func(minDist int) []tilePos {
		accepted := make([]tilePos, 0, target)
		for _, c := range candidates {
			ok := true
			for _, a := range accepted {
				if chebyshev(c, a) < minDist {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			for _, g := range gens {
				if chebyshev(c, g) < minDist {
					ok = false
					break
				}
			}
			if ok {
				accepted = append(accepted, c)
				if len(accepted) == target {
					break
				}
			}
		}
		return accepted
	}

	// Strict ≥ 15.
	spawns := tryWithDistance(15)
	if len(spawns) >= playerCount {
		players := spawns[:playerCount]
		extras := append([]tilePos(nil), spawns[playerCount:]...)
		return players, extras, true
	}
	// Fallback ≥ 10.
	spawns = tryWithDistance(10)
	if len(spawns) < playerCount {
		return nil, nil, false
	}
	if len(spawns) > playerCount {
		players := spawns[:playerCount]
		extras := append([]tilePos(nil), spawns[playerCount:]...)
		return players, extras, true
	}
	return spawns, nil, true
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

func shuffleTilePos(s []tilePos, r *rand.Rand) {
	// Fisher–Yates using r.IntN.
	for i := len(s) - 1; i > 0; i-- {
		j := r.IntN(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

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
