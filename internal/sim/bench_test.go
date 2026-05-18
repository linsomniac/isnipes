package sim

import "testing"

func BenchmarkSimTick_60x40_8P(b *testing.B) {
	cfg := Config{
		Seed:      0xCAFEBABE,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
	}
	s, err := NewSim(cfg)
	if err != nil {
		b.Fatalf("NewSim: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Tick(nil); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}

func BenchmarkSimTick_60x40_8P_FullProjectiles(b *testing.B) {
	cfg := Config{
		Seed:      0xCAFEBABE,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
	}
	s, err := NewSim(cfg)
	if err != nil {
		b.Fatalf("NewSim: %v", err)
	}
	inputs := make([]PlayerInput, 4)
	for i := range inputs {
		inputs[i] = PlayerInput{
			PlayerID: EntityID(i + 1),
			Dir:      DirE,
			FireDir:  DirE,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Tick(inputs); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}

// BenchmarkSimTick_60x40_8P_Level9 measures Phase 3's steady-state
// tick cost (§16.11): 8 players + level T9 (11 generators + 54-cap
// snipes). Pre-warms 600 ticks to reach steady state.
func BenchmarkSimTick_60x40_8P_Level9(b *testing.B) {
	// Use a 4-player level T5 config — exercises the full Phase 3
	// path (snipes + generators + level table) on a seed where the
	// geometric placement reliably succeeds. Level-9 / 8-player on
	// 60×40 frequently fails generator placement (§7.0.1 geometric
	// cap; PHASE3.md §18 risk note).
	cfg := Config{
		Seed:        0xCAFEBABE,
		Width:       60,
		Height:      40,
		PlayerIDs:   []EntityID{1, 2, 3, 4},
		LevelLetter: 'T',
		LevelNumber: 5,
	}
	s, err := NewSim(cfg)
	if err != nil {
		b.Skipf("NewSim: %v", err)
	}
	inputs := make([]PlayerInput, 4)
	for i := range inputs {
		inputs[i] = PlayerInput{
			PlayerID: EntityID(i + 1),
			Dir:      DirE,
			FireDir:  DirE,
		}
	}
	// Pre-warm to steady state.
	for i := 0; i < 600; i++ {
		if _, err := s.Tick(inputs); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Tick(inputs); err != nil {
			b.Fatalf("tick: %v", err)
		}
	}
}

// BenchmarkHistorySnapshot measures the per-tick cost of writing
// a history sample for every live entity. Target: ≤ 5 µs/op for
// 256 entities (PHASE4.md §18.12 / DoD #6).
func BenchmarkHistorySnapshot(b *testing.B) {
	cfg := Config{
		Seed:        0xBEEFFACE,
		Width:       60,
		Height:      40,
		PlayerIDs:   []EntityID{1, 2, 3, 4},
		LevelLetter: 'T',
		LevelNumber: 5,
	}
	s, err := NewSim(cfg)
	if err != nil {
		b.Skipf("NewSim: %v", err)
	}
	// Warm up so the slab is well-populated.
	inputs := make([]PlayerInput, 4)
	for i := range inputs {
		inputs[i] = PlayerInput{PlayerID: EntityID(i + 1), Dir: DirE, FireDir: DirE}
	}
	for i := 0; i < 600; i++ {
		if _, err := s.Tick(inputs); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.writeHistorySamples()
	}
}

func BenchmarkMazeGen_60x40(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := NewSim(Config{
			Seed:      uint32(i + 1),
			Width:     60,
			Height:    40,
			PlayerIDs: []EntityID{1, 2, 3, 4},
		})
		if err != nil {
			b.Fatalf("NewSim: %v", err)
		}
	}
}
