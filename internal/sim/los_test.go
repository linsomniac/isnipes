package sim

import "testing"

func TestLOS_ClearOpenRoom(t *testing.T) {
	m := fixtureMaze(30, 30, nil)
	if !HasLOS(m, 5, 5, 10, 5, 20) {
		t.Fatalf("expected LOS in open room")
	}
}

func TestLOS_BlockedByWall(t *testing.T) {
	m := fixtureMaze(30, 30, []tilePos{{7, 5}})
	if HasLOS(m, 5, 5, 10, 5, 20) {
		t.Fatalf("expected blocked LOS")
	}
}

func TestLOS_RespectsRange(t *testing.T) {
	m := fixtureMaze(30, 30, nil)
	if HasLOS(m, 5, 5, 10, 5, 4) {
		t.Fatalf("LOS should be range-rejected (dist=5 > range=4)")
	}
}

func TestLOS_EndpointsExcluded(t *testing.T) {
	m := fixtureMaze(30, 30, nil)
	// Place a wall at the *target* tile; supercover should still
	// consider this LOS true (endpoints are excluded). Per spec
	// §9.1: "exclusive of both endpoints".
	m.set(10, 5, TileWall)
	if !HasLOS(m, 5, 5, 10, 5, 20) {
		t.Fatalf("LOS should treat target endpoint as not-blocking-self")
	}
}

func TestLOS_DiagonalCornerBlock(t *testing.T) {
	// L-shaped wall corner: walls at (6, 5) and (5, 6). A diagonal
	// LOS from (5, 5) to (6, 6) must NOT pass through the corner.
	m := fixtureMaze(30, 30, []tilePos{{6, 5}, {5, 6}})
	if HasLOS(m, 5, 5, 6, 6, 20) {
		t.Fatalf("LOS should be blocked by diagonal corner")
	}
}

func TestLOS_DiagonalOpen(t *testing.T) {
	// Same diagonal but no wall corner: LOS should pass.
	m := fixtureMaze(30, 30, nil)
	if !HasLOS(m, 5, 5, 8, 8, 20) {
		t.Fatalf("LOS should be open along clear diagonal")
	}
}

func TestLOS_Determinism(t *testing.T) {
	m := fixtureMaze(20, 20, []tilePos{{10, 10}, {11, 11}})
	for i := 0; i < 100; i++ {
		a := HasLOS(m, 2, 2, 15, 15, 30)
		b := HasLOS(m, 2, 2, 15, 15, 30)
		if a != b {
			t.Fatalf("non-deterministic LOS")
		}
	}
}

func TestLOS_SamePoint(t *testing.T) {
	m := fixtureMaze(10, 10, nil)
	if !HasLOS(m, 5, 5, 5, 5, 10) {
		t.Fatalf("LOS to self should be trivially true")
	}
}

func TestLOS_OneTileApart(t *testing.T) {
	m := fixtureMaze(10, 10, nil)
	if !HasLOS(m, 5, 5, 6, 5, 1) {
		t.Fatalf("adjacent tiles should have LOS")
	}
}
