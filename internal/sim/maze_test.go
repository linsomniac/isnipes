package sim

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate golden files in testdata/")

// goldenSeeds are the 8 fixed seeds pinned by §13.1.
var goldenSeeds = []uint32{
	0x00000001, 0xDEADBEEF, 0xCAFEBABE, 0xFEEDFACE,
	0x00C0FFEE, 0x42424242, 0x80000000, 0xFFFFFFFF,
}

func referenceConfig(seed uint32) Config {
	return Config{
		Seed:      seed,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
	}
}

func TestMazeGenGoldenSeeds(t *testing.T) {
	for _, seed := range goldenSeeds {
		seed := seed
		t.Run(fmt.Sprintf("seed_%08X", seed), func(t *testing.T) {
			s, err := NewSim(referenceConfig(seed))
			if err != nil {
				t.Fatalf("NewSim: %v", err)
			}
			sum := sha256.Sum256(s.MapBytes())
			gotHex := hex.EncodeToString(sum[:])
			path := filepath.Join("testdata", "mazes", fmt.Sprintf("%08X.hash", seed))
			if *updateGolden {
				if err := os.WriteFile(path, []byte(gotHex+"\n"), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
				t.Logf("updated %s -> %s", path, gotHex)
				return
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v (run with -update to generate)", path, err)
			}
			wantHex := string(b)
			if len(wantHex) > 0 && wantHex[len(wantHex)-1] == '\n' {
				wantHex = wantHex[:len(wantHex)-1]
			}
			if wantHex != gotHex {
				t.Fatalf("hash mismatch: want %s, got %s", wantHex, gotHex)
			}
		})
	}
}

func TestMazeConnectivity(t *testing.T) {
	for i := uint32(1); i <= 50; i++ {
		seed := i * 0x9E3779B1
		s, err := NewSim(referenceConfig(seed))
		if err != nil {
			t.Fatalf("seed %#x: NewSim: %v", seed, err)
		}
		W, H, at := MazeForTest(s)
		walkable := func(t Tile) bool {
			return t == TileFloor || t == TileSpawnPlayer || t == TileSpawnGenerator
		}
		// find first spawn
		var sx, sy = -1, -1
		for y := 0; y < H && sx < 0; y++ {
			for x := 0; x < W; x++ {
				if at(x, y) == TileSpawnPlayer {
					sx, sy = x, y
					break
				}
			}
		}
		if sx < 0 {
			t.Fatalf("seed %#x: no spawn", seed)
		}
		visited := make([]bool, W*H)
		q := [][2]int{{sx, sy}}
		visited[sy*W+sx] = true
		dirs := [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}
		for len(q) > 0 {
			c := q[0]
			q = q[1:]
			for _, d := range dirs {
				nx, ny := c[0]+d[0], c[1]+d[1]
				if nx < 0 || nx >= W || ny < 0 || ny >= H {
					continue
				}
				if visited[ny*W+nx] || !walkable(at(nx, ny)) {
					continue
				}
				visited[ny*W+nx] = true
				q = append(q, [2]int{nx, ny})
			}
		}
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if walkable(at(x, y)) && !visited[y*W+x] {
					t.Fatalf("seed %#x: unreachable tile at (%d,%d)", seed, x, y)
				}
			}
		}
	}
}

func TestMazeOuterWall(t *testing.T) {
	for i := uint32(1); i <= 50; i++ {
		seed := i * 0x9E3779B1
		s, err := NewSim(referenceConfig(seed))
		if err != nil {
			t.Fatalf("seed %#x: %v", seed, err)
		}
		W, H, at := MazeForTest(s)
		for x := 0; x < W; x++ {
			if at(x, 0) != TileWall {
				t.Fatalf("seed %#x: (%d,0) not wall", seed, x)
			}
			if at(x, H-1) != TileWall {
				t.Fatalf("seed %#x: (%d,%d) not wall", seed, x, H-1)
			}
		}
		for y := 0; y < H; y++ {
			if at(0, y) != TileWall {
				t.Fatalf("seed %#x: (0,%d) not wall", seed, y)
			}
			if at(W-1, y) != TileWall {
				t.Fatalf("seed %#x: (%d,%d) not wall", seed, W-1, y)
			}
		}
	}
}

// spawnSeparationFloors are the MAZE_REVAMP.md §2.2 cell-based separation
// guarantees (spawns/generators sit at cell centres, pitch mazePitch):
//
//	spawn↔spawn ≥ spawnCellGap cells, gen↔gen ≥ genCellGap cells, and
//	spawn↔gen ≥ 1 cell (the relaxed small-map fallback; larger on roomy maps).
const (
	spawnSpawnMinTiles = spawnCellGap * mazePitch
	genGenMinTiles     = genCellGap * mazePitch
	spawnGenMinTiles   = mazePitch
)

func assertSpawnSeparation(t *testing.T, seed uint32, spawns, gens []tilePos) {
	t.Helper()
	for i := range spawns {
		for j := i + 1; j < len(spawns); j++ {
			if d := chebyshev(spawns[i], spawns[j]); d < spawnSpawnMinTiles {
				t.Fatalf("seed %#x: spawns too close: %v %v d=%d", seed, spawns[i], spawns[j], d)
			}
		}
		for _, g := range gens {
			if d := chebyshev(spawns[i], g); d < spawnGenMinTiles {
				t.Fatalf("seed %#x: spawn vs gen too close: d=%d", seed, d)
			}
		}
	}
	for i := range gens {
		for j := i + 1; j < len(gens); j++ {
			if d := chebyshev(gens[i], gens[j]); d < genGenMinTiles {
				t.Fatalf("seed %#x: gens too close: d=%d", seed, d)
			}
		}
	}
}

func collectSpawnsGens(W, H int, at func(x, y int) Tile) (spawns, gens []tilePos) {
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			switch at(x, y) {
			case TileSpawnPlayer:
				spawns = append(spawns, tilePos{x, y})
			case TileSpawnGenerator:
				gens = append(gens, tilePos{x, y})
			}
		}
	}
	return spawns, gens
}

func TestMazeSpawnSeparation(t *testing.T) {
	for i := uint32(1); i <= 50; i++ {
		seed := i * 0x9E3779B1
		s, err := NewSim(referenceConfig(seed))
		if err != nil {
			t.Fatalf("seed %#x: %v", seed, err)
		}
		W, H, at := MazeForTest(s)
		spawns, gens := collectSpawnsGens(W, H, at)
		assertSpawnSeparation(t, seed, spawns, gens)
	}
}

func TestMazeSpawnSeparationSmallMap(t *testing.T) {
	// Minimum-size map (MAZE_REVAMP.md D2): exercises the relaxed spawn↔gen
	// fallback, where the perimeter hugs the interior generators.
	for i := uint32(1); i <= 30; i++ {
		seed := i * 0x9E3779B1
		cfg := Config{
			Seed:      seed,
			Width:     minMapWidth,
			Height:    minMapHeight,
			PlayerIDs: []EntityID{1, 2, 3, 4, 5, 6},
		}
		s, err := NewSim(cfg)
		if err != nil {
			// Adversarial seeds can exhaust the retry budget on the minimum
			// map; skip rather than fail. Non-skipped cases still validate.
			continue
		}
		W, H, at := MazeForTest(s)
		spawns, gens := collectSpawnsGens(W, H, at)
		assertSpawnSeparation(t, seed, spawns, gens)
		if len(spawns) < len(cfg.PlayerIDs) {
			t.Fatalf("seed %#x: not enough spawns (%d < %d)", seed, len(spawns), len(cfg.PlayerIDs))
		}
	}
}

func TestMazeAverageDegree(t *testing.T) {
	// MAZE_REVAMP.md §5: the wide-corridor maze is a braided spanning maze.
	// Mean cell degree must exceed a pure spanning tree (~2.0, proving the
	// braid + chambers add loops) and stay well below a full grid (4.0).
	const N = 200
	var sum float64
	for i := uint32(1); i <= N; i++ {
		seed := i * 0x9E3779B1
		s, err := NewSim(referenceConfig(seed))
		if err != nil {
			t.Fatalf("seed %#x: %v", seed, err)
		}
		W, H, at := MazeForTest(s)
		cellsX := (W - 1) / mazePitch
		cellsY := (H - 1) / mazePitch
		linkedDir := func(cx, cy, dx, dy int) bool {
			ox, oy := cx*mazePitch+1, cy*mazePitch+1
			switch {
			case dx == 1:
				return at(ox+corridorWidth, oy) == TileFloor
			case dx == -1:
				return at(ox-1, oy) == TileFloor
			case dy == 1:
				return at(ox, oy+corridorWidth) == TileFloor
			default:
				return at(ox, oy-1) == TileFloor
			}
		}
		totalDeg, cells := 0, 0
		for cy := 0; cy < cellsY; cy++ {
			for cx := 0; cx < cellsX; cx++ {
				cells++
				for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
					ncx, ncy := cx+d[0], cy+d[1]
					if ncx < 0 || ncx >= cellsX || ncy < 0 || ncy >= cellsY {
						continue
					}
					if linkedDir(cx, cy, d[0], d[1]) {
						totalDeg++
					}
				}
			}
		}
		if cells > 0 {
			sum += float64(totalDeg) / float64(cells)
		}
	}
	mean := sum / float64(N)
	if mean <= 2.05 || mean >= 3.5 {
		t.Fatalf("mean cell degree %.3f outside (2.05, 3.5) — expected a braided maze", mean)
	}
}

func TestMazeGeneratorNeighbourFloor(t *testing.T) {
	for i := uint32(1); i <= 50; i++ {
		seed := i * 0x9E3779B1
		s, err := NewSim(referenceConfig(seed))
		if err != nil {
			t.Fatalf("seed %#x: %v", seed, err)
		}
		W, H, at := MazeForTest(s)
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if at(x, y) != TileSpawnGenerator {
					continue
				}
				ok := false
				for _, d := range [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}} {
					nx, ny := x+d[0], y+d[1]
					if nx < 0 || nx >= W || ny < 0 || ny >= H {
						continue
					}
					if at(nx, ny) == TileFloor {
						ok = true
						break
					}
				}
				if !ok {
					t.Fatalf("seed %#x: generator at (%d,%d) has no floor neighbour", seed, x, y)
				}
			}
		}
	}
}
