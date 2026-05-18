package sim

import (
	"errors"
	"testing"
)

func TestLevelParams_AtoF(t *testing.T) {
	for _, letter := range []byte{'A', 'B', 'C', 'D', 'E', 'F'} {
		p := LookupLevel(letter, 1)
		if p.LOSRadius != 6 {
			t.Fatalf("letter=%c LOSRadius=%d, want 6", letter, p.LOSRadius)
		}
		if p.SnipeLeadFactor != 0 {
			t.Fatalf("letter=%c lead=%d, want 0", letter, p.SnipeLeadFactor)
		}
		if p.SnipeFireCooldown != 20 {
			t.Fatalf("letter=%c fireCD=%d, want 20", letter, p.SnipeFireCooldown)
		}
		if p.SnipeSpeed != 12 {
			t.Fatalf("letter=%c speed=%d, want 12", letter, p.SnipeSpeed)
		}
		if p.GeneratorHP != 3 {
			t.Fatalf("letter=%c genHP=%d, want 3", letter, p.GeneratorHP)
		}
	}
}

func TestLevelParams_GtoM(t *testing.T) {
	for _, letter := range []byte{'G', 'H', 'I', 'J', 'K', 'L', 'M'} {
		p := LookupLevel(letter, 1)
		if p.LOSRadius != 8 {
			t.Fatalf("letter=%c LOSRadius=%d, want 8", letter, p.LOSRadius)
		}
		if p.SnipeLeadFactor != 1 {
			t.Fatalf("letter=%c lead=%d, want 1", letter, p.SnipeLeadFactor)
		}
		if p.SnipeFireCooldown != 20 {
			t.Fatalf("letter=%c fireCD=%d, want 20", letter, p.SnipeFireCooldown)
		}
		if p.SnipeSpeed != 12 {
			t.Fatalf("letter=%c speed=%d, want 12", letter, p.SnipeSpeed)
		}
	}
}

func TestLevelParams_NtoS(t *testing.T) {
	for _, letter := range []byte{'N', 'O', 'P', 'Q', 'R', 'S'} {
		p := LookupLevel(letter, 1)
		if p.LOSRadius != 10 {
			t.Fatalf("letter=%c LOSRadius=%d, want 10", letter, p.LOSRadius)
		}
		if p.SnipeLeadFactor != 2 {
			t.Fatalf("letter=%c lead=%d, want 2", letter, p.SnipeLeadFactor)
		}
		if p.SnipeFireCooldown != 15 {
			t.Fatalf("letter=%c fireCD=%d, want 15", letter, p.SnipeFireCooldown)
		}
		if p.SnipeSpeed != 12 {
			t.Fatalf("letter=%c speed=%d, want 12", letter, p.SnipeSpeed)
		}
		if p.GeneratorHP != 3 {
			t.Fatalf("letter=%c genHP=%d, want 3", letter, p.GeneratorHP)
		}
	}
}

func TestLevelParams_TtoZ(t *testing.T) {
	for _, letter := range []byte{'T', 'U', 'V', 'W', 'X', 'Y', 'Z'} {
		p := LookupLevel(letter, 1)
		if p.LOSRadius != 14 {
			t.Fatalf("letter=%c LOSRadius=%d, want 14", letter, p.LOSRadius)
		}
		if p.SnipeLeadFactor != 2 {
			t.Fatalf("letter=%c lead=%d, want 2", letter, p.SnipeLeadFactor)
		}
		if p.SnipeFireCooldown != 15 {
			t.Fatalf("letter=%c fireCD=%d, want 15", letter, p.SnipeFireCooldown)
		}
		if p.SnipeSpeed != 15 {
			t.Fatalf("letter=%c speed=%d, want 15", letter, p.SnipeSpeed)
		}
		if p.GeneratorHP != 5 {
			t.Fatalf("letter=%c genHP=%d, want 5", letter, p.GeneratorHP)
		}
	}
}

func TestLevelNumberScaling(t *testing.T) {
	for n := 1; n <= 9; n++ {
		p := LookupLevel('A', n)
		if p.MaxSnipesTotal != n*6 {
			t.Fatalf("n=%d MaxSnipesTotal=%d, want %d", n, p.MaxSnipesTotal, n*6)
		}
		if p.InitialGenerators != n+2 {
			t.Fatalf("n=%d InitialGenerators=%d, want %d", n, p.InitialGenerators, n+2)
		}
		wantLives := 10 - n
		if wantLives < 1 {
			wantLives = 1
		}
		if p.PlayerLives != wantLives {
			t.Fatalf("n=%d PlayerLives=%d, want %d", n, p.PlayerLives, wantLives)
		}
	}
}

func TestLookupLevelCanonicalisesLowercase(t *testing.T) {
	upper := LookupLevel('A', 1)
	lower := LookupLevel('a', 1)
	if upper != lower {
		t.Fatalf("lowercase 'a' didn't canonicalise: %+v vs %+v", upper, lower)
	}
}

func TestLookupLevelPanicsOnOutOfRange(t *testing.T) {
	for _, badLetter := range []byte{0, '@', '[', 'a' - 1} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("letter=%d didn't panic", badLetter)
				}
			}()
			LookupLevel(badLetter, 1)
		}()
	}
	for _, badNumber := range []int{0, -1, 10, 99} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("n=%d didn't panic", badNumber)
				}
			}()
			LookupLevel('A', badNumber)
		}()
	}
}

func TestNewSimRejectsInvalidLevel(t *testing.T) {
	base := Config{Seed: 1, Width: 60, Height: 40, PlayerIDs: []EntityID{1}}
	cases := []struct {
		name   string
		letter byte
		number int
	}{
		{"letter-only", 'A', 0},
		{"number-only", 0, 5},
		{"letter-out-of-range-low", '@', 5},
		{"letter-out-of-range-high", '[', 5},
		{"number-zero-with-letter", 'C', 0},
		{"number-ten", 'C', 10},
		{"number-neg", 'C', -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			cfg.LevelLetter = c.letter
			cfg.LevelNumber = c.number
			_, err := NewSim(cfg)
			if !errors.Is(err, ErrInvalidLevel) {
				t.Fatalf("err=%v, want ErrInvalidLevel", err)
			}
		})
	}
}

func TestNewSimAcceptsZeroLevel(t *testing.T) {
	// Phase 1/2 default: 0/0 is valid and means "no level table".
	cfg := Config{
		Seed: 1, Width: 60, Height: 40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim with zero level: %v", err)
	}
	_ = s
}

func TestNewSimAcceptsValidLevel(t *testing.T) {
	cfg := Config{
		Seed: 1, Width: 60, Height: 40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
		LevelLetter:  'a', // lowercase — should canonicalise
		LevelNumber:  3,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	_ = s
}
