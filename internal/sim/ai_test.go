package sim

import "testing"

// newAIFixtureSim creates a 60×40 all-floor sim with one player at
// (px, py) tile coords, level T1 (Brutal letter, n=1), NoGenerators
// (we hand-spawn snipes via the export hook).
func newAIFixtureSim(t *testing.T, px, py int) *Sim {
	t.Helper()
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
		NoRespawn:    true,
		LevelLetter:  'C', // Easy bucket, LOSRadius=6
		LevelNumber:  1,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	PlacePlayerForTest(s, 1,
		int32(px*subtilePerTile+subtilePerTile/2),
		int32(py*subtilePerTile+subtilePerTile/2))
	return s
}

func TestSnipeIdleToPatrolAfter15Ticks(t *testing.T) {
	s := newAIFixtureSim(t, 30, 20)
	snipeID := SpawnSnipeForTest(s, 0, 10, 10)
	// Ticks 1..15: should remain IDLE with FlagSpawnInvuln (§7.1
	// "cleared by AI step on tick 16+").
	for i := 0; i < 15; i++ {
		s.Tick(nil)
	}
	info, _ := SnipeStateForTest(s, snipeID)
	if info.AIState != AIStateIdle {
		t.Fatalf("after 15 ticks: state=%d, want Idle", info.AIState)
	}
	e, _ := EntityRawForTest(s, snipeID)
	if e.Flags&FlagSpawnInvuln == 0 {
		t.Fatalf("FlagSpawnInvuln cleared early")
	}
	// Tick 16: should transition to PATROL.
	s.Tick(nil)
	info, _ = SnipeStateForTest(s, snipeID)
	if info.AIState != AIStatePatrol {
		t.Fatalf("after 16 ticks: state=%d, want Patrol", info.AIState)
	}
	e, _ = EntityRawForTest(s, snipeID)
	if e.Flags&FlagSpawnInvuln != 0 {
		t.Fatalf("FlagSpawnInvuln still set")
	}
}

func TestSnipePatrolToChase(t *testing.T) {
	// Player at (12, 10); snipe at (10, 10) — Chebyshev=2, LOSRadius=6
	// after Easy bucket.
	s := newAIFixtureSim(t, 12, 10)
	snipeID := SpawnSnipeForTest(s, 0, 10, 10)
	// Drain spawn-invuln (16 ticks) then run a few patrol ticks so
	// the LOS scan engages.
	for i := 0; i < 20; i++ {
		s.Tick(nil)
	}
	info, _ := SnipeStateForTest(s, snipeID)
	if info.AIState != AIStateChase && info.AIState != AIStateAttack {
		t.Fatalf("snipe never entered chase/attack: state=%d", info.AIState)
	}
	if info.ChaseTarget != 1 {
		t.Fatalf("chaseTarget=%d, want 1", info.ChaseTarget)
	}
}

func TestSnipeChaseToPatrolOnLOSLost(t *testing.T) {
	s := newAIFixtureSim(t, 12, 10)
	snipeID := SpawnSnipeForTest(s, 0, 10, 10)
	// Drain spawn-invuln + enter chase.
	for i := 0; i < 20; i++ {
		s.Tick(nil)
	}
	info, _ := SnipeStateForTest(s, snipeID)
	if info.AIState != AIStateChase && info.AIState != AIStateAttack {
		t.Fatalf("snipe never entered chase: state=%d", info.AIState)
	}
	// Move player far away (out of LOSRadius=6). Move 10 tiles east.
	PlacePlayerForTest(s, 1,
		int32(30*subtilePerTile+subtilePerTile/2),
		int32(10*subtilePerTile+subtilePerTile/2))
	// Tick 65 more ticks (>60 ticks of LOS-lost).
	for i := 0; i < 65; i++ {
		s.Tick(nil)
	}
	info, _ = SnipeStateForTest(s, snipeID)
	if info.AIState != AIStatePatrol {
		t.Fatalf("snipe didn't fall back to patrol: state=%d", info.AIState)
	}
}

func TestSnipeAttackFiresProjectile(t *testing.T) {
	// Use Hard bucket (LOSRadius=10, lead=full, faster fire).
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
		NoRespawn:    true,
		LevelLetter:  'N',
		LevelNumber:  1,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	PlacePlayerForTest(s, 1,
		int32(15*subtilePerTile+subtilePerTile/2),
		int32(10*subtilePerTile+subtilePerTile/2))
	snipeID := SpawnSnipeForTest(s, 0, 10, 10)
	// Drain spawn-invuln + chase + attack. Snipe should fire within
	// ~30 ticks of engagement.
	var firedID EntityID
	for i := 0; i < 60 && firedID == 0; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntitySpawn && e.Actor == snipeID {
				firedID = e.Target
			}
		}
	}
	if firedID == 0 {
		t.Fatalf("snipe never fired in 60 ticks")
	}
}

func TestSnipeSpawnInvulnFiltersProjectile(t *testing.T) {
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
	PlacePlayerForTest(s, 1,
		int32(5*subtilePerTile+subtilePerTile/2),
		int32(20*subtilePerTile+subtilePerTile/2))
	// Place snipe close enough that the projectile reaches it during
	// the spawn-invuln window (15 ticks).
	snipeID := SpawnSnipeForTest(s, 0, 7, 20)
	// Player fires E at the spawn-invuln snipe.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	// Tick projectile motion until it reaches the snipe.
	hit := false
	for i := 0; i < 14; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == snipeID {
				hit = true
			}
		}
		if hit {
			break
		}
	}
	if hit {
		t.Fatalf("invulnerable snipe was hit")
	}
}

func TestSnipeWeakBlockHigherIDClamped(t *testing.T) {
	s := newAIFixtureSim(t, 30, 30)
	sA := SpawnSnipeForTest(s, 0, 10, 10)
	sB := SpawnSnipeForTest(s, 0, 10, 10) // same tile centre on purpose
	// Both are placed at the same position; the weak-block logic
	// runs on the *next* tick.
	_, _ = s.Tick(nil)
	eA, _ := EntityRawForTest(s, sA)
	eB, _ := EntityRawForTest(s, sB)
	if eA.X == eB.X && eA.Y == eB.Y {
		t.Fatalf("snipes still overlap after weak block")
	}
}
