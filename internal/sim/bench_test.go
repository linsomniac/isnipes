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
