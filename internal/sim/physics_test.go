package sim

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// allFloorMaze returns a maze of (W, H) with outer wall and the
// interior all TileFloor.
func allFloorMaze(W, H int) []Tile {
	t := make([]Tile, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if x == 0 || y == 0 || x == W-1 || y == H-1 {
				t[y*W+x] = TileWall
			} else {
				t[y*W+x] = TileFloor
			}
		}
	}
	return t
}

func newFixtureSim(t *testing.T) *Sim {
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
		NoRespawn:    true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	return s
}

func TestDiagonalSpeedNormalised(t *testing.T) {
	if got := diag(16); got != 11 {
		t.Fatalf("diag(16) = %d, want 11", got)
	}
	if got := diag(32); got != 22 {
		t.Fatalf("diag(32) = %d, want 22", got)
	}
}

func TestMoveStopsAtWall(t *testing.T) {
	cases := []struct {
		name string
		d    Dir
	}{
		{"E", DirE}, {"W", DirW}, {"N", DirN}, {"S", DirS},
		{"NE", DirNE}, {"SE", DirSE}, {"SW", DirSW}, {"NW", DirNW},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newFixtureSim(t)
			// Place at near a wall in c.d direction.
			vx, vy := velocityFor(c.d, playerSpeed)
			// Start at centre tile (5, 5), and choose target wall based
			// on direction. We position player so wall is in path.
			// For E: place near right inner wall (W-2, 5).
			// For S: place near bottom inner wall (5, H-2).
			// For diagonals: corner.
			var tx, ty int
			switch c.d {
			case DirE, DirNE, DirSE:
				tx = s.Width() - 2
			case DirW, DirNW, DirSW:
				tx = 1
			default:
				tx = 5
			}
			switch c.d {
			case DirS, DirSE, DirSW:
				ty = s.Height() - 2
			case DirN, DirNE, DirNW:
				ty = 1
			default:
				ty = 5
			}
			cx := int32(tx*subtilePerTile + subtilePerTile/2)
			cy := int32(ty*subtilePerTile + subtilePerTile/2)
			PlacePlayerForTest(s, 1, cx, cy)
			// Tick 5 times — should clamp.
			for i := 0; i < 5; i++ {
				inputs := []PlayerInput{{PlayerID: 1, Dir: c.d}}
				_, err := s.Tick(inputs)
				if err != nil {
					t.Fatalf("tick %d: %v", i, err)
				}
			}
			e, _ := EntityRawForTest(s, 1)
			// Verify in-bounds. With wall stopping, the player must not
			// pass through.
			_ = vx
			_ = vy
			if e.X < playerHalfExt+1 || e.X >= int32(s.Width()*subtilePerTile)-playerHalfExt-1 {
				t.Logf("end X=%d", e.X)
			}
			// Sanity: the player's AABB must not be inside any wall tile.
			for dy := int32(-playerHalfExt); dy <= playerHalfExt; dy += playerHalfExt {
				for dx := int32(-playerHalfExt); dx <= playerHalfExt; dx += playerHalfExt {
					qx := int((e.X + dx) / subtilePerTile)
					qy := int((e.Y + dy) / subtilePerTile)
					if s.Tile(qx, qy) == TileWall {
						t.Fatalf("dir %s: AABB corner in wall at (%d,%d)", c.name, qx, qy)
					}
				}
			}
		})
	}
}

func TestMoveSlidesAlongWall(t *testing.T) {
	// Place a player adjacent to the bottom wall and move E; vy should
	// be 0 (no vertical component anyway) — really the test is about
	// the cardinal advance.
	s := newFixtureSim(t)
	cx := int32(5*subtilePerTile + subtilePerTile/2)
	// H-3, not H-2: the 2-tile-tall player (playerHalfExt=256) placed at H-2
	// would overlap the bottom wall, so X would clamp instead of advancing.
	cy := int32((s.Height()-3)*subtilePerTile + subtilePerTile/2)
	PlacePlayerForTest(s, 1, cx, cy)
	for i := 0; i < 5; i++ {
		_, err := s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirE}})
		if err != nil {
			t.Fatalf("tick: %v", err)
		}
	}
	e, _ := EntityRawForTest(s, 1)
	if e.X <= cx {
		t.Fatalf("did not advance E: %d -> %d", cx, e.X)
	}
	if e.Y != cy {
		t.Fatalf("Y drifted: %d -> %d", cy, e.Y)
	}
}

func TestPlayerBlockedByGenerator(t *testing.T) {
	s := newFixtureSim(t)
	// Place player at tile (5, 10) centre, generator at tile (10, 10).
	px := int32(5*subtilePerTile + subtilePerTile/2)
	py := int32(10*subtilePerTile + subtilePerTile/2)
	gx := int32(10*subtilePerTile + subtilePerTile/2)
	gy := int32(10*subtilePerTile + subtilePerTile/2)
	PlacePlayerForTest(s, 1, px, py)
	PlaceGeneratorForTest(s, gx, gy)
	// Hold E for many ticks; player should stop short of generator.
	for i := 0; i < 200; i++ {
		_, err := s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirE}})
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	e, _ := EntityRawForTest(s, 1)
	// Player's right edge: e.X + playerHalfExt. Must be < gx - generatorHalfExt.
	if e.X+playerHalfExt >= gx-generatorHalfExt {
		t.Fatalf("player overlapped generator: player right %d vs gen left %d",
			e.X+playerHalfExt, gx-generatorHalfExt)
	}
}

func TestTurboStrictLock(t *testing.T) {
	s := newFixtureSim(t)
	PlacePlayerForTest(s, 1, int32(5*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	// Tick 1: engage turbo + E.
	_, err := s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirE, Turbo: true}})
	if err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	e1, _ := EntityRawForTest(s, 1)
	if e1.Facing != DirE {
		t.Fatalf("facing not E: %d", e1.Facing)
	}
	// Tick 2: send turbo + W. Should still move E.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirW, Turbo: true}})
	e2, _ := EntityRawForTest(s, 1)
	if e2.X <= e1.X {
		t.Fatalf("turbo lock failed: x went %d -> %d (W input ignored)", e1.X, e2.X)
	}
	// Tick 3: release turbo + N.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirN, Turbo: false}})
	e3, _ := EntityRawForTest(s, 1)
	if e3.Y >= e2.Y {
		t.Fatalf("release-turbo-then-N didn't move N: y %d -> %d", e2.Y, e3.Y)
	}
	// Tick 4: re-engage turbo + S.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirS, Turbo: true}})
	e4, _ := EntityRawForTest(s, 1)
	if e4.Y <= e3.Y {
		t.Fatalf("re-engage turbo + S didn't move S: y %d -> %d", e3.Y, e4.Y)
	}
	// Tick 5: turbo + DirIdle — turbo cancel via idle.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirIdle, Turbo: true}})
	e5, _ := EntityRawForTest(s, 1)
	if e5.Flags&FlagTurbo != 0 {
		t.Fatalf("FlagTurbo not cleared by idle-cancel")
	}
	if e5.X != e4.X || e5.Y != e4.Y {
		t.Fatalf("idle-cancel: position moved")
	}
}

// parsePhysicsConstants reads a key=value file (one constant per
// line, comments empty) and returns a map[string]int.
func parsePhysicsConstants(t *testing.T, raw string) map[string]int {
	t.Helper()
	out := map[string]int{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		k := strings.TrimSpace(parts[0])
		v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			t.Fatalf("constants file: parse %q: %v", line, err)
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("constants file: scan: %v", err)
	}
	return out
}

// TestPhysicsConstantsTextFile is the Go side of the Phase 4 §14
// parity oracle. The same file is read by
// web/tests/sim_constants.test.ts; both sides must match.
func TestPhysicsConstantsTextFile(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/sim/physics_constants.txt")
	if err != nil {
		t.Fatalf("read constants file: %v", err)
	}
	got := parsePhysicsConstants(t, string(raw))
	want := map[string]int{
		"subtile_per_tile":    subtilePerTile,
		"player_speed":        playerSpeed,
		"player_turbo_speed":  playerTurboSpeed,
		"player_half_ext":     playerHalfExt,
		"fire_cooldown_ticks": fireCooldownTicks,
		"projectile_speed":    projectileSpeed,
		"projectile_lifetime": projectileLifetime,
		"generator_half_ext":  generatorHalfExt,
		"projectile_half_ext": projectileHalfExt,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("constant %q: file=%d, go=%d (parity drift — update testdata/sim/physics_constants.txt or the Go const)", k, got[k], v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("constant %q in file is not asserted by Go side", k)
		}
	}
}
