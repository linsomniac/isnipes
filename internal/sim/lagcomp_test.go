package sim

import "testing"

// Phase 4 §8 — lag-compensated combat tests.
//
// These tests drive resolveProjectile directly with synthesized
// candidates and a hand-built entityHistory. This isolates the
// lag-comp gate, rewind math, and flag-at-T_view filter from the
// surrounding tick-loop timing.

// tile returns the subtile centre coords of tile (x, y).
func tile(x, y int) (int32, int32) {
	return int32(x*subtilePerTile + subtilePerTile/2),
		int32(y*subtilePerTile + subtilePerTile/2)
}

// mkHist constructs an entityHistory with one sample per tick in
// [startTick, endTick] all at the same (kind, x, y, flags).
func mkHist(startTick, endTick uint32, x, y int32, flags uint8) *entityHistory {
	h := &entityHistory{}
	for t := startTick; t <= endTick; t++ {
		h.write(t, x, y, KindPlayer, flags)
	}
	return h
}

// snapshotMaze returns a tiny stub maze the size of a 60x40 all-floor
// fixture for combat tests.
func snapshotMaze() *maze {
	tiles := allFloorMaze(60, 40)
	return &maze{W: 60, H: 40, tiles: tiles}
}

// TestLagCompShotHitsAtRewoundPosition: target currently far away
// (would miss present-time) but had a history sample on the projectile
// line at T_view → lag-comp registers the hit.
func TestLagCompShotHitsAtRewoundPosition(t *testing.T) {
	m := snapshotMaze()
	// Projectile owned by player ID 1, fired from (10, 10) east.
	// On its motion step the projectile centre is at (10, 10) and
	// (10, 10)+(32, 0) → segment x ∈ [10, 42] subtiles.
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10) // 1 tile east, on the line
	// Present-time target: far off the line.
	offx, offy := tile(11, 15)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: offx, Y: offy}
	candidates := []*Entity{target}
	// Construct a projectile literally at the target's "past" position
	// minus halfExt so first motion crosses into the rewound AABB.
	// Projectile starts at origin (ax, ay) − 8 subtiles in X so AABB
	// entry happens within the 32-subtile motion step.
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X:  bx - 96 - 24 - 8, // 8 subtiles before AABB left edge
		Y:  ay,
		VX: 32, VY: 0,
	}
	_ = ax // (unused but kept for clarity)
	// History: target was at (bx, by) for many ticks before now.
	hist := mkHist(1, 20, bx, by, 0)
	lc := lagCompContext{
		currentTick: 22,
		owtTicks:    2,
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
	}
	// T_view = 22 - 2 - 2 = 18. History.at(18) = (bx, by). Hit.
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind != projHitEntity {
		t.Fatalf("expected entity hit; got kind=%d", res.kind)
	}
	if res.targetID != 2 {
		t.Fatalf("targetID=%d, want 2", res.targetID)
	}
}

// TestLagCompPresentTimeMisses: identical fixture, owtTicks=0 → no
// lag-comp; the present-time AABB is far from the projectile → miss.
func TestLagCompPresentTimeMisses(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, _ := tile(11, 10)
	offx, offy := tile(11, 15)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: offx, Y: offy}
	candidates := []*Entity{target}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	_ = ax
	hist := mkHist(1, 20, bx, ay, 0)
	lc := lagCompContext{
		currentTick: 22,
		owtTicks:    0, // present-time
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind == projHitEntity {
		t.Fatalf("present-time path should miss off-line target; got hit on %d", res.targetID)
	}
}

// TestLagCompClampedToOldestSample: owt much larger than the 9-entry
// ring clamps to the oldest available sample.
func TestLagCompClampedToOldestSample(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	offx, offy := tile(11, 15)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: offx, Y: offy}
	candidates := []*Entity{target}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	_ = ax
	// History samples 12..20 with target on-line; T_view will be
	// clamped to currentTick - LagCompTicks = 14, which exists in
	// the ring and is on-line.
	hist := mkHist(12, 20, bx, by, 0)
	lc := lagCompContext{
		currentTick: 22,
		owtTicks:    15, // > LagCompTicks; clamped to 8
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind != projHitEntity {
		t.Fatalf("clamp-to-oldest should hit; got kind=%d", res.kind)
	}
}

// TestLagCompSnipeFireUnaffected: the gate disables lag-comp for
// shooterKind == KindSnipe regardless of owtTicks.
func TestLagCompSnipeFireUnaffected(t *testing.T) {
	lc := lagCompContext{
		currentTick: 22,
		owtTicks:    4,
		getHist:     func(EntityID) *entityHistory { return nil },
	}
	if lc.enabled(KindSnipe) {
		t.Fatal("lc.enabled(KindSnipe) must be false")
	}
	if !lc.enabled(KindPlayer) {
		t.Fatal("lc.enabled(KindPlayer) must be true when owt>0 and getHist!=nil")
	}
}

// TestLagCompSpawnInvulnAtRewoundTick: target had FlagSpawnInvuln at
// T_view → skipped by the lag-comp filter, no hit even if present-
// time it would have hit.
func TestLagCompSpawnInvulnAtRewoundTick(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: bx, Y: by} // present-time on-line
	candidates := []*Entity{target}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	_ = ax
	hist := mkHist(1, 20, bx, by, FlagSpawnInvuln)
	lc := lagCompContext{
		currentTick: 22, owtTicks: 2,
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind == projHitEntity {
		t.Fatalf("invuln-at-Tview should skip target; hit %d", res.targetID)
	}
}

// TestLagCompDeadFlagAtRewoundTick: target was FlagDead at T_view
// (e.g. an earlier death sample); lag-comp must skip.
func TestLagCompDeadFlagAtRewoundTick(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: bx, Y: by}
	candidates := []*Entity{target}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	_ = ax
	hist := mkHist(1, 20, bx, by, FlagDead)
	lc := lagCompContext{
		currentTick: 22, owtTicks: 2,
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind == projHitEntity {
		t.Fatalf("dead-at-Tview should skip target")
	}
}

// TestLagCompGhostCandidateHitsRemovedEntity: PHASE4 §8.6 — a target
// that has been removed (no longer in the candidate list) but whose
// history sample at T_view was alive registers a lag-comp hit.
func TestLagCompGhostCandidateHitsRemovedEntity(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	_ = ax
	// Candidate list deliberately empty — target was removed.
	candidates := []*Entity{}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	hist := mkHist(1, 20, bx, by, 0) // alive at all sampled ticks
	allHist := map[EntityID]*entityHistory{2: hist}
	lc := lagCompContext{
		currentTick: 22, owtTicks: 2,
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
		getAllHistories: func() map[EntityID]*entityHistory {
			return allHist
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind != projHitEntity {
		t.Fatalf("ghost-candidate should hit; kind=%d", res.kind)
	}
	if res.targetID != 2 {
		t.Fatalf("targetID=%d, want 2 (ghost)", res.targetID)
	}
}

// TestLagCompGhostCandidateSkipsDeadAtTview: ghost-candidate path
// honors the FlagDead/FlagSpawnInvuln filter at T_view.
func TestLagCompGhostCandidateSkipsDeadAtTview(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	_ = ax
	candidates := []*Entity{}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	hist := mkHist(1, 20, bx, by, FlagDead)
	allHist := map[EntityID]*entityHistory{2: hist}
	lc := lagCompContext{
		currentTick: 22, owtTicks: 2,
		getHist: func(id EntityID) *entityHistory {
			if id == 2 {
				return hist
			}
			return nil
		},
		getAllHistories: func() map[EntityID]*entityHistory {
			return allHist
		},
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind == projHitEntity {
		t.Fatalf("ghost-candidate dead-at-Tview should not hit")
	}
}

// TestLagCompNoHistoryFallsBackToPresent: candidate with no history
// uses the present (X, Y) — newly-spawned entity case (§8.6).
func TestLagCompNoHistoryFallsBackToPresent(t *testing.T) {
	m := snapshotMaze()
	ax, ay := tile(10, 10)
	bx, by := tile(11, 10)
	target := &Entity{ID: 2, Kind: KindPlayer, HP: 1, X: bx, Y: by} // on-line at present
	candidates := []*Entity{target}
	proj := &Entity{
		ID: 99, Kind: KindProjectile, HP: 1,
		X: bx - 96 - 24 - 8, Y: ay, VX: 32, VY: 0,
	}
	_ = ax
	lc := lagCompContext{
		currentTick: 22, owtTicks: 2,
		getHist: func(EntityID) *entityHistory { return nil }, // no history
	}
	res := resolveProjectile(m, proj, 1, KindPlayer, candidates, lc)
	if res.kind != projHitEntity {
		t.Fatalf("fallback to present should hit; kind=%d", res.kind)
	}
}

// TestDeterminism_LagCompFixedOWT: two sims with identical
// (seed, inputs, OWTTicks) produce per-tick byte-identical
// fingerprints across many ticks. PHASE4.md §8.5 / DoD #1.
func TestDeterminism_LagCompFixedOWT(t *testing.T) {
	cfg := Config{
		Seed: 0xDEFACED, Width: 60, Height: 40,
		PlayerIDs: []EntityID{1, 2}, NoGenerators: true, NoRespawn: true,
	}
	s1, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		owt := uint8((i / 30) % (LagCompTicks + 1)) // cycles 0..8
		inputs := []PlayerInput{
			{
				PlayerID: 1, Dir: Dir((i % 8) + 1), FireDir: Dir((i + 3) % 9),
				ClientTick: uint16(i),
				LagComp:    FireOptions{OWTTicks: owt},
			},
			{
				PlayerID: 2, Dir: Dir(((i + 4) % 8) + 1),
				ClientTick: uint16(i + 1000),
				LagComp:    FireOptions{OWTTicks: owt},
			},
		}
		ev1, err1 := s1.Tick(inputs)
		ev2, err2 := s2.Tick(inputs)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("tick %d: err mismatch %v vs %v", i, err1, err2)
		}
		if len(ev1) != len(ev2) {
			t.Fatalf("tick %d: event count %d vs %d", i, len(ev1), len(ev2))
		}
		for j := range ev1 {
			if ev1[j] != ev2[j] {
				t.Fatalf("tick %d ev %d: %+v vs %+v", i, j, ev1[j], ev2[j])
			}
		}
		f1 := s1.Fingerprint()
		f2 := s2.Fingerprint()
		if f1 != f2 {
			t.Fatalf("tick %d: fingerprint divergence %x vs %x", i, f1, f2)
		}
	}
}
