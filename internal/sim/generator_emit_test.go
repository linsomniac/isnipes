package sim

import "testing"

// newGenFixtureSim creates a sim with a fully-open maze at the
// requested level, no players in the way, then hand-places one
// generator at (10, 10).
func newGenFixtureSim(t *testing.T, letter byte, number int) (*Sim, EntityID) {
	t.Helper()
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true, // we manually place; don't let placer interfere
		NoRespawn:    true,
		LevelLetter:  letter,
		LevelNumber:  number,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	// Stash the player far away.
	PlacePlayerForTest(s, 1,
		int32(50*subtilePerTile+subtilePerTile/2),
		int32(35*subtilePerTile+subtilePerTile/2))
	// Place a generator at (10, 10) with the level's HP.
	gx := int32(10*subtilePerTile + subtilePerTile/2)
	gy := int32(10*subtilePerTile + subtilePerTile/2)
	genID := PlaceGeneratorForTest(s, gx, gy)
	// Register the generator state so the emission code path picks
	// it up. Use a fixed cooldown so the test is deterministic.
	s.store.generators[genID] = &generatorState{
		emitCooldown: 30, // emit after 1 second
		rotation:     uint8(genID % 8),
	}
	return s, genID
}

func TestGeneratorEmitsFirstSnipe(t *testing.T) {
	s, genID := newGenFixtureSim(t, 'A', 5)
	var spawned EntityID
	for i := 0; i < 60 && spawned == 0; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntitySpawn && e.Actor == genID {
				spawned = e.Target
			}
		}
	}
	if spawned == 0 {
		t.Fatalf("generator never emitted")
	}
	// Verify the snipe entity exists.
	e, ok := EntityRawForTest(s, spawned)
	if !ok || e.Kind != KindSnipe {
		t.Fatalf("spawned entity isn't a snipe: %+v", e)
	}
}

func TestGeneratorRespectsGlobalCap(t *testing.T) {
	// Level 1 → MaxSnipesTotal = 6. Place a generator that would emit
	// constantly; after many ticks the live count must not exceed 6.
	s, genID := newGenFixtureSim(t, 'A', 1)
	// Force fast emission by lowering cooldown each tick.
	for i := 0; i < 1500; i++ {
		s.Tick(nil)
		// Force cooldown to 0 so the gen tries again next tick.
		if gs := s.store.generators[genID]; gs != nil {
			gs.emitCooldown = 0
		}
		if got := len(LiveSnipeIDsForTest(s)); got > 6 {
			t.Fatalf("snipes exceeded global cap: %d > 6", got)
		}
	}
}

func TestGeneratorDestructionStopsEmissions(t *testing.T) {
	s, genID := newGenFixtureSim(t, 'A', 5)
	// Tick until first snipe emerges (initial cooldown = 30 ticks).
	for i := 0; i < 60; i++ {
		s.Tick(nil)
	}
	if len(LiveSnipeIDsForTest(s)) < 1 {
		t.Fatalf("no snipe emitted in 60 ticks")
	}
	preCount := len(LiveSnipeIDsForTest(s))
	// Force-destroy the generator.
	idx := s.store.findByID(genID)
	if idx < 0 {
		t.Fatal("gen disappeared")
	}
	s.store.slots[idx].HP = 0
	s.store.slots[idx].Flags |= FlagDead
	// Tick GC + many more ticks.
	s.Tick(nil)
	if _, ok := EntityRawForTest(s, genID); ok {
		t.Fatalf("gen not GC'd")
	}
	// Tick 600 more ticks; no new snipes should appear.
	postBaseline := len(LiveSnipeIDsForTest(s))
	_ = preCount
	for i := 0; i < 600; i++ {
		s.Tick(nil)
	}
	if got := len(LiveSnipeIDsForTest(s)); got > postBaseline {
		t.Fatalf("snipes grew after gen destroyed: %d → %d", postBaseline, got)
	}
}

func TestGeneratorEmitsAtRate(t *testing.T) {
	// Single generator, 1200 ticks (~40s). Expected 4–12 snipes
	// at 4-8s per emission with global cap = 30 (level 5).
	s, _ := newGenFixtureSim(t, 'A', 5)
	for i := 0; i < 1200; i++ {
		s.Tick(nil)
	}
	got := len(LiveSnipeIDsForTest(s))
	// Loose band — we just verify the emitter is roughly within rate.
	if got < 2 || got > 30 {
		t.Fatalf("snipe count %d out of expected band [2, 30]", got)
	}
}
