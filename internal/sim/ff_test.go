package sim

import "testing"

// ffSetup creates a level-A1 sim with one player, an all-floor maze,
// and any number of snipes / generators placed by the test.
func ffSetup(t *testing.T) *Sim {
	t.Helper()
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
		NoRespawn:    true,
		LevelLetter:  'A',
		LevelNumber:  1,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	// Move the player out of the way.
	PlacePlayerForTest(s, 1,
		int32(50*subtilePerTile+subtilePerTile/2),
		int32(35*subtilePerTile+subtilePerTile/2))
	return s
}

func TestSnipeProjectileDoesNotHitOtherSnipes(t *testing.T) {
	s := ffSetup(t)
	// Two snipes at row 10, columns 6 and 10. Both will be spawn-
	// invuln initially; tick past the invuln window.
	sA := SpawnSnipeForTest(s, 0, 6, 10)
	sB := SpawnSnipeForTest(s, 0, 10, 10)
	for i := 0; i < 18; i++ {
		s.Tick(nil)
	}
	// Inject a snipe-fired projectile that originates at sA's
	// position heading E.
	eA, _ := EntityRawForTest(s, sA)
	pid, _ := s.store.allocID()
	pIdx := s.store.alloc()
	if pIdx < 0 {
		t.Fatal("no slot")
	}
	s.store.slots[pIdx] = Entity{
		ID: pid, Kind: KindProjectile, HP: 1, Facing: DirE,
		X: eA.X, Y: eA.Y, VX: projectileSpeed,
	}
	s.store.projectiles[pid] = &projectileState{lifetime: 90, shooterID: sA}
	// Tick enough for the projectile to traverse past sB.
	hit := false
	for i := 0; i < 30; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == sB {
				hit = true
			}
		}
	}
	if hit {
		t.Fatalf("snipe-fired projectile hit another snipe")
	}
}

func TestSnipeProjectileDoesNotDamageGenerator(t *testing.T) {
	s := ffSetup(t)
	sA := SpawnSnipeForTest(s, 0, 6, 10)
	// Place a generator at tile (10, 10).
	gID := PlaceGeneratorForTest(s,
		int32(10*subtilePerTile+subtilePerTile/2),
		int32(10*subtilePerTile+subtilePerTile/2))
	// Wait out invuln.
	for i := 0; i < 18; i++ {
		s.Tick(nil)
	}
	// Inject snipe-fired projectile.
	eA, _ := EntityRawForTest(s, sA)
	pid, _ := s.store.allocID()
	pIdx := s.store.alloc()
	s.store.slots[pIdx] = Entity{
		ID: pid, Kind: KindProjectile, HP: 1, Facing: DirE,
		X: eA.X, Y: eA.Y, VX: projectileSpeed,
	}
	s.store.projectiles[pid] = &projectileState{lifetime: 90, shooterID: sA}
	hit := false
	for i := 0; i < 30; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == gID {
				hit = true
			}
		}
	}
	if hit {
		t.Fatalf("snipe-fired projectile damaged a generator")
	}
}

func TestPlayerProjectileKillsSnipe(t *testing.T) {
	s := ffSetup(t)
	// Place player at (5, 20), snipe at (8, 20) — close enough that
	// even with patrol drift, the projectile reaches within ~13 ticks
	// after firing.
	PlacePlayerForTest(s, 1,
		int32(5*subtilePerTile+subtilePerTile/2),
		int32(20*subtilePerTile+subtilePerTile/2))
	sID := SpawnSnipeForTest(s, 0, 8, 20)
	// Wait out invuln (18 ticks).
	for i := 0; i < 18; i++ {
		s.Tick(nil)
	}
	// Player fires E.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	killed := false
	for i := 0; i < 30 && !killed; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == sID {
				killed = true
			}
		}
	}
	if !killed {
		t.Fatalf("player projectile never killed the snipe")
	}
}
