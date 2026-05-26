package sim

import "testing"

// firstEventOfKind returns the first event matching kind/target, or
// (Event{}, false).
func firstEventOfKind(evs []Event, kind uint8, target EntityID) (Event, bool) {
	for _, e := range evs {
		if e.Kind == kind && (target == 0 || e.Target == target) {
			return e, true
		}
	}
	return Event{}, false
}

func TestProjectileTravelsAtProjectileSpeed(t *testing.T) {
	s := newFixtureSim(t)
	cx := int32(10*subtilePerTile + subtilePerTile/2)
	cy := int32(20*subtilePerTile + subtilePerTile/2)
	PlacePlayerForTest(s, 1, cx, cy)
	// Tick 1: fire E (also moves the player by 0 since Dir=Idle).
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	projs := LiveProjectileIDsForTest(s)
	if len(projs) != 1 {
		t.Fatalf("projectile count after fire: %d", len(projs))
	}
	projID := projs[0]
	startEnt, _ := EntityRawForTest(s, projID)
	// Tick 8 more ticks; projectile moves 8*projectileSpeed subtiles in X.
	for i := 0; i < 8; i++ {
		_, _ = s.Tick(nil)
	}
	endEnt, ok := EntityRawForTest(s, projID)
	if !ok {
		t.Fatalf("projectile despawned early")
	}
	dx := endEnt.X - startEnt.X
	want := int32(8 * projectileSpeed)
	if dx != want {
		t.Fatalf("dx = %d, want %d", dx, want)
	}
}

func TestProjectileHitsWallAtExpectedTick(t *testing.T) {
	// Set up: floor maze, but place a wall column at tile X = pX+5
	// (so the wall's near edge is 5 tiles east of player centre).
	W, H := 60, 40
	tiles := allFloorMaze(W, H)
	pX, pY := 10, 20
	wallX := pX + 5
	for y := 1; y < H-1; y++ {
		tiles[y*W+wallX] = TileWall
	}
	s := newFixtureSim(t)
	OverrideMazeForTest(s, W, H, tiles)
	cx := int32(pX*subtilePerTile + subtilePerTile/2)
	cy := int32(pY*subtilePerTile + subtilePerTile/2)
	PlacePlayerForTest(s, 1, cx, cy)
	// Fire E.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	startTick := s.ServerTick()
	projID := LiveProjectileIDsForTest(s)[0]
	// At projectileSpeed=64 the impact tick roughly halves vs. the old speed.
	var killTick int32 = -1
	for i := 0; i < 100; i++ {
		evs, _ := s.Tick(nil)
		if e, ok := firstEventOfKind(evs, EventEntityKill, projID); ok && e.Reason == 1 {
			killTick = int32(s.ServerTick())
			break
		}
	}
	if killTick < 0 {
		t.Fatalf("no kill event for projectile")
	}
	delta := killTick - int32(startTick)
	// projectileSpeed=224: the wall (5 tiles east of the centre-placed player)
	// is reached in ~5 motion ticks; allow a small band.
	if delta < 3 || delta > 8 {
		t.Fatalf("kill at startTick+%d, want 3..8", delta)
	}
}

func TestProjectileHitsEntityHeadOn(t *testing.T) {
	W, H := 60, 40
	tiles := allFloorMaze(W, H)
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
		NoRespawn:    true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, tiles)
	PlacePlayerForTest(s, 1, int32(1*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(6*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	// Fire E from player 1.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	fireTick := s.ServerTick()
	// At projectileSpeed=224 (and the 2-tile player-2 hitbox) the head-on
	// impact lands ~5 motion ticks out; allow a small band.
	var hitTick uint32 = 0
	for i := 0; i < 100; i++ {
		evs, _ := s.Tick(nil)
		if _, ok := firstEventOfKind(evs, EventEntityHit, 2); ok {
			hitTick = s.ServerTick()
			break
		}
	}
	if hitTick == 0 {
		t.Fatalf("no hit event for player 2")
	}
	delta := int(hitTick) - int(fireTick)
	if delta < 3 || delta > 8 {
		t.Fatalf("hit at fire+%d, want ~5", delta)
	}
}

func TestProjectileExpiresAfter90Ticks(t *testing.T) {
	s := newFixtureSim(t)
	// projectileSpeed=224 → a 90-tick flight covers ~78.75 tiles, so the
	// projectile needs the max 120-wide runway (the 60-wide fixture would
	// wall-hit first). N/S would hit the 40-tall map's wall first, so fire E.
	W, H := 120, 40
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	fireTick := s.ServerTick()
	projID := LiveProjectileIDsForTest(s)[0]
	var expiryTick uint32
	for i := 0; i < 200; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == projID && e.Reason == 2 {
				expiryTick = s.ServerTick()
			}
		}
		if expiryTick != 0 {
			break
		}
	}
	if expiryTick == 0 {
		t.Fatalf("no expiry event")
	}
	delta := int(expiryTick) - int(fireTick)
	if delta != 90 {
		t.Fatalf("expiry at fire+%d, want 90", delta)
	}
	if ids := LiveProjectileIDsForTest(s); len(ids) != 0 {
		t.Fatalf("projectile still alive after expiry: %v", ids)
	}
}

func TestNoFriendlyFireSelf(t *testing.T) {
	s := newFixtureSim(t)
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	// 5 ticks of motion - shooter shouldn't be hit.
	for i := 0; i < 5; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == 1 {
				t.Fatalf("shooter self-hit at tick %d", s.ServerTick())
			}
		}
	}
}

func TestProjectilesDoNotCollide(t *testing.T) {
	W, H := 60, 40
	tiles := allFloorMaze(W, H)
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
		NoRespawn:    true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, tiles)
	// A at (5, 10) firing E; B at (10, 5) firing S. Cross at (10, 10).
	PlacePlayerForTest(s, 1, int32(5*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(10*subtilePerTile+subtilePerTile/2), int32(5*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{
		{PlayerID: 1, FireDir: DirE},
		{PlayerID: 2, FireDir: DirS},
	})
	proj := LiveProjectileIDsForTest(s)
	if len(proj) != 2 {
		t.Fatalf("expected 2 projectiles, got %d", len(proj))
	}
	// Tick 45 more — past the 40-tick cross.
	for i := 0; i < 45; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && (e.Target == proj[0] || e.Target == proj[1]) {
				t.Fatalf("unexpected projectile-to-projectile hit: %+v", e)
			}
			if e.Kind == EventEntityKill && e.Reason == 0 && (e.Target == proj[0] || e.Target == proj[1]) {
				t.Fatalf("unexpected projectile-as-target kill: %+v", e)
			}
		}
	}
}

func TestGeneratorTakesThreeShots(t *testing.T) {
	s := newFixtureSim(t)
	W, H := s.Width(), s.Height()
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(5*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	genID := PlaceGeneratorForTest(s, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	hits := 0
	kills := 0
	destroyed := 0
	for i := 0; i < 400; i++ {
		// Want to fire every tick the cooldown allows. We send fire
		// every tick; the sim drops the request if cooldown isn't 0.
		evs, _ := s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == genID {
				hits++
			}
			if e.Kind == EventEntityKill && e.Target == genID && e.Reason == 0 {
				kills++
			}
			if e.Kind == EventGeneratorDestroyed && e.Target == genID {
				destroyed++
			}
		}
		if destroyed > 0 {
			break
		}
	}
	if hits != 3 || kills != 1 || destroyed != 1 {
		t.Fatalf("hits=%d kills=%d destroyed=%d", hits, kills, destroyed)
	}
	if _, ok := EntityRawForTest(s, genID); ok {
		t.Fatalf("generator should be GC'd")
	}
}

func TestCannotFireWhileTurbo(t *testing.T) {
	s := newFixtureSim(t)
	W, H := s.Width(), s.Height()
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	// Hold turbo + E + fire E.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirE, Turbo: true, FireDir: DirE}})
	if ids := LiveProjectileIDsForTest(s); len(ids) != 0 {
		t.Fatalf("projectile spawned while turboing: %v", ids)
	}
	// Release turbo, fire E.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	if ids := LiveProjectileIDsForTest(s); len(ids) != 1 {
		t.Fatalf("projectile not spawned after turbo release: %d", len(ids))
	}
}

func TestCanFireDuringTurboIdleCancel(t *testing.T) {
	s := newFixtureSim(t)
	W, H := s.Width(), s.Height()
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	// turbo held but Dir == Idle: turbo cancel.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, Dir: DirIdle, Turbo: true, FireDir: DirE}})
	if ids := LiveProjectileIDsForTest(s); len(ids) != 1 {
		t.Fatalf("expected projectile during turbo-idle-cancel + fire, got %d", len(ids))
	}
}

func TestDeadPlayerCannotFire(t *testing.T) {
	W, H := 60, 40
	cfg := Config{
		Seed:         1,
		Width:        W,
		Height:       H,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
		NoRespawn:    false,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(1*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(4*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	// Fire E from 1 to kill 2.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	for i := 0; i < 60 && true; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == 2 {
				goto killed
			}
		}
	}
	t.Fatalf("player 2 not killed in time")
killed:
	// Player 2 is now DEAD. Try to fire from 2.
	_, _ = s.Tick([]PlayerInput{{PlayerID: 2, FireDir: DirE}})
	for _, id := range LiveProjectileIDsForTest(s) {
		e, _ := EntityRawForTest(s, id)
		if e.X < int32(W*subtilePerTile/2) && id != 1 {
			// any projectile from player 2 starts near (4,10)
			t.Fatalf("DEAD player fired: projectile id=%d", id)
		}
	}
}

func TestProjectileGetsFull90MotionTicks(t *testing.T) {
	s := newFixtureSim(t)
	// projectileSpeed=224 → a 90-tick flight covers ~78.75 tiles; use the max
	// 120-wide runway so the projectile expires before any wall hit. Fire E
	// (N/S would hit the 40-tall map's wall first).
	W, H := 120, 40
	OverrideMazeForTest(s, W, H, allFloorMaze(W, H))
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	fireTick := s.ServerTick()
	projID := LiveProjectileIDsForTest(s)[0]
	for i := 0; i < 95; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == projID && e.Reason == 2 {
				if int(s.ServerTick())-int(fireTick) != 90 {
					t.Fatalf("expiry at fire+%d, want fire+90", int(s.ServerTick())-int(fireTick))
				}
				return
			}
		}
	}
	t.Fatalf("projectile did not expire within 95 ticks")
}
