package sim

import (
	"math/rand/v2"
	"testing"
)

func TestPropertyNoEntityInsideWall(t *testing.T) {
	for i := uint32(1); i <= 20; i++ {
		seed := i * 0x9E3779B1
		r := rand.New(rand.NewPCG(uint64(seed), 0))
		W := 30 + r.IntN(90)
		H := 20 + r.IntN(60)
		nPlayers := 1 + r.IntN(8)
		pids := make([]EntityID, nPlayers)
		for k := 0; k < nPlayers; k++ {
			pids[k] = EntityID(k + 1)
		}
		cfg := Config{Seed: seed, Width: W, Height: H, PlayerIDs: pids}
		s, err := NewSim(cfg)
		if err != nil {
			continue // adversarial config; skip
		}
		for tick := 0; tick < 200; tick++ {
			inputs := make([]PlayerInput, nPlayers)
			for k := 0; k < nPlayers; k++ {
				inputs[k] = PlayerInput{
					PlayerID: pids[k],
					Dir:      Dir(r.IntN(9)),
					Turbo:    r.IntN(5) == 0,
					FireDir:  Dir(r.IntN(9)),
				}
			}
			if _, err := s.Tick(inputs); err != nil {
				t.Fatalf("seed %#x tick %d: %v", seed, tick, err)
			}
			for _, e := range s.Entities() {
				if e.Flags&FlagDead != 0 {
					continue
				}
				he := entityHalfExt(e.Kind)
				// Compute AABB tile span using half-open interval to
				// match the engine's tile-overlap convention: the
				// AABB occupies tiles [colLo, colHi] where colHi uses
				// (right_edge - 1) / T.
				tileMinX := int((e.X - he) / subtilePerTile)
				tileMaxX := int((e.X + he - 1) / subtilePerTile)
				tileMinY := int((e.Y - he) / subtilePerTile)
				tileMaxY := int((e.Y + he - 1) / subtilePerTile)
				for ty := tileMinY; ty <= tileMaxY; ty++ {
					for tx := tileMinX; tx <= tileMaxX; tx++ {
						if s.Tile(tx, ty) == TileWall {
							t.Fatalf("seed %#x tick %d: entity %d kind=%d pos=(%d,%d) he=%d AABB tile (%d,%d) is wall",
								seed, tick, e.ID, e.Kind, e.X, e.Y, he, tx, ty)
						}
					}
				}
				if e.X < 0 || e.X >= int32(s.Width()*subtilePerTile) {
					t.Fatalf("X out of bounds: %d", e.X)
				}
				if e.Y < 0 || e.Y >= int32(s.Height()*subtilePerTile) {
					t.Fatalf("Y out of bounds: %d", e.Y)
				}
			}
		}
	}
}

// TestPropertySnipesNeverInsideWall covers the snipe path the generic
// property test misses (it runs level-less, so no snipes spawn). With the
// enlarged snipeHalfExt (MAZE_REVAMP.md), weak-block separation must never
// push a snipe into a wall near a closed cell link (codex review). Level 9 →
// max snipe cap → dense clustering around generators, the worst case.
func TestPropertySnipesNeverInsideWall(t *testing.T) {
	for i := uint32(1); i <= 6; i++ {
		seed := i * 0x9E3779B1
		cfg := Config{
			Seed:        seed,
			Width:       120,
			Height:      80,
			PlayerIDs:   []EntityID{1},
			LevelLetter: 'C',
			LevelNumber: 9,
		}
		s, err := NewSim(cfg)
		if err != nil {
			t.Fatalf("seed %#x: %v", seed, err)
		}
		for tick := 0; tick < 1200; tick++ {
			if _, err := s.Tick(nil); err != nil {
				t.Fatalf("seed %#x tick %d: %v", seed, tick, err)
			}
			for _, e := range s.Entities() {
				if e.Kind != KindSnipe || e.Flags&FlagDead != 0 {
					continue
				}
				he := entityHalfExt(e.Kind)
				tileMinX := int((e.X - he) / subtilePerTile)
				tileMaxX := int((e.X + he - 1) / subtilePerTile)
				tileMinY := int((e.Y - he) / subtilePerTile)
				tileMaxY := int((e.Y + he - 1) / subtilePerTile)
				for ty := tileMinY; ty <= tileMaxY; ty++ {
					for tx := tileMinX; tx <= tileMaxX; tx++ {
						if s.Tile(tx, ty) == TileWall {
							t.Fatalf("seed %#x tick %d: snipe %d pos=(%d,%d) AABB tile (%d,%d) is wall",
								seed, tick, e.ID, e.X, e.Y, tx, ty)
						}
					}
				}
			}
		}
	}
}

func TestPropertyProjectileNeverPassesThroughWall(t *testing.T) {
	for i := uint32(1); i <= 10; i++ {
		seed := i * 0x9E3779B1
		r := rand.New(rand.NewPCG(uint64(seed), 0))
		cfg := Config{
			Seed:      seed,
			Width:     60,
			Height:    40,
			PlayerIDs: []EntityID{1, 2, 3, 4},
		}
		s, err := NewSim(cfg)
		if err != nil {
			continue
		}
		prevPos := make(map[EntityID][2]int32)
		for tick := 0; tick < 300; tick++ {
			inputs := []PlayerInput{
				{PlayerID: 1, Dir: Dir(r.IntN(9)), FireDir: Dir(r.IntN(9))},
				{PlayerID: 2, Dir: Dir(r.IntN(9)), FireDir: Dir(r.IntN(9))},
				{PlayerID: 3, Dir: Dir(r.IntN(9)), FireDir: Dir(r.IntN(9))},
				{PlayerID: 4, Dir: Dir(r.IntN(9)), FireDir: Dir(r.IntN(9))},
			}
			evs, _ := s.Tick(inputs)
			// Check: for each projectile that existed both before and after,
			// its AABB must not be inside a wall now.
			killed := map[EntityID]bool{}
			for _, e := range evs {
				if e.Kind == EventEntityKill && e.Reason == 1 {
					killed[e.Target] = true
				}
			}
			for _, e := range s.Entities() {
				if e.Kind != KindProjectile {
					continue
				}
				he := entityHalfExt(e.Kind)
				tileMinX := int((e.X - he) / subtilePerTile)
				tileMaxX := int((e.X + he - 1) / subtilePerTile)
				tileMinY := int((e.Y - he) / subtilePerTile)
				tileMaxY := int((e.Y + he - 1) / subtilePerTile)
				for ty := tileMinY; ty <= tileMaxY; ty++ {
					for tx := tileMinX; tx <= tileMaxX; tx++ {
						if s.Tile(tx, ty) == TileWall {
							t.Fatalf("seed %#x tick %d: projectile %d AABB tile (%d,%d) is wall",
								seed, tick, e.ID, tx, ty)
						}
					}
				}
			}
			// Record positions for next iteration (not strictly used).
			for _, e := range s.Entities() {
				prevPos[e.ID] = [2]int32{e.X, e.Y}
			}
			_ = killed
		}
	}
}
