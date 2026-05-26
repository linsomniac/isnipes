package sim

import "testing"

func TestTurboStateClearedOnRespawn(t *testing.T) {
	W, H := 60, 40
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	// Need TileSpawnPlayer tiles for respawn. The fixture maze has no
	// spawn tiles after OverrideMazeForTest, so seed a few.
	seedSpawnTiles(s, []tilePos{{2, 2}, {W - 3, 2}, {2, H - 3}, {W - 3, H - 3}})
	// Place 1 to fire E, 2 to take the hit.
	PlacePlayerForTest(s, 1, int32(1*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(4*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	// Engage turbo + E on player 2.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 2, Dir: DirE, Turbo: true}})
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	// Wait for kill.
	for i := 0; i < 100; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == 2 {
				goto killed
			}
		}
	}
	t.Fatalf("p2 not killed")
killed:
	// Wait 90 ticks for respawn.
	for i := 0; i < 95; i++ {
		_, _ = s.Tick(nil)
		e, ok := EntityRawForTest(s, 2)
		if !ok {
			continue
		}
		if e.Flags&FlagDead == 0 {
			// Respawned.
			if e.Flags&FlagTurbo != 0 {
				t.Fatalf("FlagTurbo still set after respawn")
			}
			ps, _ := PlayerStateForTest(s, 2)
			if ps.LastDir != DirIdle {
				t.Fatalf("lastDir not Idle: %d", ps.LastDir)
			}
			// A fresh turbo+W must be honored (the lock cleared) — the player
			// moves west from its respawn tile. Compare to the respawn position
			// over a single tick: at the 3.5× turbo speed two consecutive ticks
			// can both pin against the left wall when the respawn spawn tile
			// sits near it, but one tick from any spawn tile (≥ tile 2) still
			// advances west.
			x0 := e.X
			_, _ = s.Tick([]PlayerInput{{PlayerID: 2, Dir: DirW, Turbo: true}})
			ex, _ := EntityRawForTest(s, 2)
			if ex.X >= x0 {
				t.Fatalf("post-respawn turbo+W did not move west: x %d -> %d", x0, ex.X)
			}
			return
		}
	}
	t.Fatalf("player 2 never respawned")
}

func seedSpawnTiles(s *Sim, tiles []tilePos) {
	W, H, _ := MazeForTest(s)
	cur := make([]Tile, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			cur[y*W+x] = s.Tile(x, y)
		}
	}
	for _, p := range tiles {
		cur[p.Y*W+p.X] = TileSpawnPlayer
	}
	OverrideMazeForTest(s, W, H, cur)
}

func TestRespawnSoloPlayerUsesDeathPosition(t *testing.T) {
	W, H := 60, 40
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	seedSpawnTiles(s, []tilePos{{2, 2}, {2, 20}, {W - 3, 2}, {W - 3, 20}, {W - 3, H - 3}})
	// Place player at (10, 10) and force-kill by manually setting HP=0
	// + invoking the kill pipeline. We can simulate by placing a
	// "phantom" generator on the same tile, then shooting it.
	// Simpler: directly set HP=0 and tick.
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	// We need to kill the player; the easiest path is two players with
	// one shooting the other. With a single player, we mark the player
	// dead by manipulation: drop HP to 0 via a generator.
	genID := PlaceGeneratorForTest(s, int32(15*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	_ = genID
	// We'll engineer: player at (10,10), generator at (15,10). Player
	// shoots E, generator dies. That doesn't kill player.
	//
	// Alternative: rather than test the actual kill path, exercise the
	// solo-player code path by manually triggering respawn with a known
	// deathPosition. We'll just check that selectRespawnTile chooses
	// one of the top-3 safest spawns.

	// To exercise no-live-hostile fallback, ensure no live entities
	// other than player 1 exist.
	RemoveGeneratorsForTest(s)

	// Manually fake the player as dead with deathPos at (10,10).
	idx := s.store.findByID(1)
	s.store.slots[idx].Flags |= FlagDead
	ps := s.store.players[1]
	ps.deathX = s.store.slots[idx].X
	ps.deathY = s.store.slots[idx].Y
	ps.respawnAt = s.serverTick + 1
	ps.hasRespawnAt = true

	// Tick once; respawn fires.
	_, _ = s.Tick(nil)
	e, _ := EntityRawForTest(s, 1)
	if e.Flags&FlagDead != 0 {
		t.Fatalf("player not respawned")
	}
	// Check chosen tile is one of the 3 farthest from death tile.
	deathT := tilePos{X: 10, Y: 10}
	type d struct {
		t    tilePos
		dist int
	}
	candidates := s.spawnTilesForPlayers()
	var ranked []d
	for _, c := range candidates {
		ranked = append(ranked, d{c, chebyshev(c, deathT)})
	}
	// Sort descending by dist.
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].dist > ranked[i].dist {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	chosenTile := tilePos{X: int(e.X / subtilePerTile), Y: int(e.Y / subtilePerTile)}
	top3 := ranked
	if len(top3) > 3 {
		top3 = top3[:3]
	}
	found := false
	for _, c := range top3 {
		if c.t == chosenTile {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("chosen %v not in top-3 by distance from death", chosenTile)
	}
}

func TestRespawnDeferredWhenAllBlocked(t *testing.T) {
	W, H := 60, 40
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1, 2, 3},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	// Seed exactly two spawn tiles.
	seedSpawnTiles(s, []tilePos{{5, 5}, {7, 5}})
	// Park players 2 and 3 directly on those spawn tiles to block.
	PlacePlayerForTest(s, 2, int32(5*subtilePerTile+subtilePerTile/2), int32(5*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 3, int32(7*subtilePerTile+subtilePerTile/2), int32(5*subtilePerTile+subtilePerTile/2))
	// Mark player 1 dead with respawn this tick.
	idx := s.store.findByID(1)
	s.store.slots[idx].Flags |= FlagDead
	ps := s.store.players[1]
	ps.respawnAt = s.serverTick + 1
	ps.hasRespawnAt = true
	ps.deathX = 0
	ps.deathY = 0
	startTick := s.serverTick
	for i := 0; i < 5; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntitySpawn && e.Target == 1 {
				t.Fatalf("unexpected respawn while blocked, tick %d", s.ServerTick())
			}
		}
		p, _ := EntityRawForTest(s, 1)
		if p.Flags&FlagDead == 0 {
			t.Fatalf("player 1 came alive during block tick %d", s.ServerTick())
		}
	}
	ps2 := s.store.players[1]
	// Each defer pushes respawnAt by 1; after 5 deferrals starting
	// from respawnAt=startTick+1, the next-scheduled value is
	// startTick+1+5 = startTick+6.
	if ps2.respawnAt != startTick+6 {
		t.Fatalf("respawnAt = %d, expected startTick+6 = %d", ps2.respawnAt, startTick+6)
	}
	// Move player 2 out of the way.
	PlacePlayerForTest(s, 2, int32(50*subtilePerTile+subtilePerTile/2), int32(30*subtilePerTile+subtilePerTile/2))
	// Next tick should respawn (one tile free).
	evs, _ := s.Tick(nil)
	respawned := false
	for _, e := range evs {
		if e.Kind == EventEntitySpawn && e.Target == 1 {
			respawned = true
		}
	}
	if !respawned {
		t.Fatalf("player 1 did not respawn after slot freed")
	}
}
