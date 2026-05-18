package sim

import "testing"

// fixtureMaze builds a maze with outer wall and an optional row of
// interior walls described by a list of (x, y) positions.
func fixtureMaze(W, H int, walls []tilePos) *maze {
	m := newMaze(W, H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if x == 0 || y == 0 || x == W-1 || y == H-1 {
				m.set(x, y, TileWall)
			} else {
				m.set(x, y, TileFloor)
			}
		}
	}
	for _, p := range walls {
		m.set(p.X, p.Y, TileWall)
	}
	return m
}

func TestBFS_StraightLine(t *testing.T) {
	m := fixtureMaze(30, 30, nil)
	nx, ny, ok := PathNext(m, 1, 1, 20, 1, 40)
	if !ok {
		t.Fatalf("no path")
	}
	if nx != 2 || ny != 1 {
		t.Fatalf("next = (%d, %d), want (2, 1)", nx, ny)
	}
}

func TestBFS_AroundWall(t *testing.T) {
	// Vertical wall at x=5, y=1..3, gap at y=4.
	walls := []tilePos{{5, 1}, {5, 2}, {5, 3}}
	m := fixtureMaze(30, 30, walls)
	nx, ny, ok := PathNext(m, 3, 1, 7, 1, 40)
	if !ok {
		t.Fatalf("no path")
	}
	// Source (3, 1). Direct east is open (4, 1) — but (5, 1) is wall.
	// First step "E" is still optimal because (4, 1) is on the shortest
	// path going around the wall (E, then S to bypass, then E E).
	if nx != 4 || ny != 1 {
		t.Fatalf("next = (%d, %d), want (4, 1)", nx, ny)
	}
}

func TestBFS_NoPath(t *testing.T) {
	walls := []tilePos{{2, 1}, {1, 2}, {2, 2}}
	m := fixtureMaze(10, 10, walls)
	// (1, 1) is surrounded by walls and the outer wall.
	if _, _, ok := PathNext(m, 1, 1, 5, 5, 40); ok {
		t.Fatalf("expected no path, got one")
	}
}

func TestBFS_DepthCap(t *testing.T) {
	m := fixtureMaze(30, 30, nil)
	if _, _, ok := PathNext(m, 1, 1, 20, 1, 5); ok {
		t.Fatalf("expected depth-cap rejection, got success")
	}
}

func TestBFS_Determinism(t *testing.T) {
	m := fixtureMaze(20, 20, []tilePos{{5, 5}, {6, 5}, {7, 5}})
	for i := 0; i < 10; i++ {
		nx1, ny1, ok1 := PathNext(m, 1, 1, 15, 15, 40)
		nx2, ny2, ok2 := PathNext(m, 1, 1, 15, 15, 40)
		if ok1 != ok2 || nx1 != nx2 || ny1 != ny2 {
			t.Fatalf("non-deterministic: %v vs %v", []int{nx1, ny1}, []int{nx2, ny2})
		}
	}
}

func TestBFS_TargetWall(t *testing.T) {
	m := fixtureMaze(10, 10, []tilePos{{5, 5}})
	if _, _, ok := PathNext(m, 1, 1, 5, 5, 40); ok {
		t.Fatalf("path into wall accepted")
	}
}
