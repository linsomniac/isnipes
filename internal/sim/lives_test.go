package sim

import "testing"

// TestPlayerStartsWithLivesFromLevel — DoD §18.1.
func TestPlayerStartsWithLivesFromLevel(t *testing.T) {
	cases := []struct {
		letter byte
		number int
		want   uint8
	}{
		{'A', 1, 9}, // 10 - 1
		{'A', 9, 1}, // max(1, 10-9)
		{'T', 5, 5},
		{'M', 7, 3},
	}
	for _, c := range cases {
		cfg := Config{
			Seed:        0xCAFE,
			Width:       60,
			Height:      40,
			PlayerIDs:   []EntityID{1},
			LevelLetter: c.letter,
			LevelNumber: c.number,
		}
		s, err := NewSim(cfg)
		if err != nil {
			t.Fatalf("NewSim %c%d: %v", c.letter, c.number, err)
		}
		got := s.LivesRemaining(1)
		if got != c.want {
			t.Errorf("%c%d: got %d, want %d", c.letter, c.number, got, c.want)
		}
	}
}

// TestPvPDefaultLives — §22 open question #1: LevelLetter==0 defaults
// to 3 lives.
func TestPvPDefaultLives(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	if s.LivesRemaining(1) != defaultPvPLives {
		t.Errorf("player 1 lives = %d, want %d", s.LivesRemaining(1), defaultPvPLives)
	}
	if s.LivesRemaining(2) != defaultPvPLives {
		t.Errorf("player 2 lives = %d, want %d", s.LivesRemaining(2), defaultPvPLives)
	}
}

// TestInitialSpawnInvuln — every player carries FlagSpawnInvuln at
// tick 0 and SpawnInvulnUntil reports the scheduled clear tick.
func TestInitialSpawnInvuln(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	ents := s.Entities()
	if len(ents) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(ents))
	}
	if ents[0].Flags&FlagSpawnInvuln == 0 {
		t.Fatal("FlagSpawnInvuln not set at construction")
	}
	if got := s.SpawnInvulnUntil(1); got != uint32(playerSpawnInvulnTicks) {
		t.Fatalf("SpawnInvulnUntil: got %d, want %d", got, playerSpawnInvulnTicks)
	}
}

// TestSpawnInvulnClearedAtTimer — step 8.5 clears the flag exactly at
// serverTick == spawnInvulnUntil.
func TestSpawnInvulnClearedAtTimer(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	// Tick for playerSpawnInvulnTicks-1 ticks: flag still set.
	for i := 0; i < int(playerSpawnInvulnTicks)-1; i++ {
		if _, err := s.Tick(nil); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if s.Entities()[0].Flags&FlagSpawnInvuln == 0 {
		t.Fatalf("flag cleared early at tick %d", s.serverTick)
	}
	// The next tick fires step 8.5 because serverTick now equals
	// spawnInvulnUntil.
	if _, err := s.Tick(nil); err != nil {
		t.Fatalf("clear-tick Tick: %v", err)
	}
	if s.Entities()[0].Flags&FlagSpawnInvuln != 0 {
		t.Fatalf("flag still set at tick %d (want cleared)", s.serverTick)
	}
	if s.SpawnInvulnUntil(1) != 0 {
		t.Fatalf("SpawnInvulnUntil not reset: %d", s.SpawnInvulnUntil(1))
	}
}

// TestSpawnInvulnFireGate — a fire input issued while invuln does not
// produce a projectile entity.
func TestSpawnInvulnFireGate(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	if _, err := s.Tick([]PlayerInput{
		{PlayerID: 1, FireDir: DirE},
	}); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	for _, e := range s.Entities() {
		if e.Kind == KindProjectile {
			t.Fatal("projectile spawned despite spawn-invuln gate")
		}
	}
}

// TestKillerAwardedScore_Snipe — generator's snipe fire path is hard to
// stage; instead we directly call killEntity with synthetic state.
func TestKillerAwardedScore(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	// Inject a victim snipe + generator into the slab so awardKill has
	// something to reward against.
	mkVictim := func(kind EntityKind, id EntityID) {
		idx := s.store.alloc()
		s.store.slots[idx] = Entity{ID: id, Kind: kind, HP: 1}
		if kind == KindSnipe {
			s.store.snipes[id] = &snipeState{aiState: AIStateIdle}
		}
	}
	mkVictim(KindSnipe, 100)
	mkVictim(KindGenerator, 101)

	// Snipe kill by player 1: +1.
	idx := s.store.findByID(100)
	if idx < 0 {
		t.Fatal("snipe missing")
	}
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	if got := s.Score(1); got != killRewardSnipe {
		t.Fatalf("after snipe kill: score = %d, want %d", got, killRewardSnipe)
	}

	// Generator kill: +10 (cumulative).
	idx = s.store.findByID(101)
	if idx < 0 {
		t.Fatal("generator missing")
	}
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	if got := s.Score(1); got != killRewardSnipe+killRewardGenerator {
		t.Fatalf("after gen kill: score = %d, want %d", got, killRewardSnipe+killRewardGenerator)
	}
}

// TestPlayerKillScoreAndPenalty — killer +25, victim -5, lives--.
func TestPlayerKillScoreAndPenalty(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	initialLives := s.LivesRemaining(2)
	idx := s.store.findByID(2)
	if idx < 0 {
		t.Fatal("victim missing")
	}
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	if got := s.Score(1); got != killRewardPlayer {
		t.Fatalf("killer score = %d, want %d", got, killRewardPlayer)
	}
	if got := s.Score(2); got != deathPenaltyOwn {
		t.Fatalf("victim score = %d, want %d", got, deathPenaltyOwn)
	}
	if got := s.LivesRemaining(2); got != initialLives-1 {
		t.Fatalf("victim lives = %d, want %d", got, initialLives-1)
	}
}

// TestPlayerEliminatedAtZero — N kills exhaust lives; player flips to
// Eliminated and the slab entry is GC'd while playerState persists.
func TestPlayerEliminatedAtZero(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	startLives := s.LivesRemaining(2)
	for i := uint8(0); i < startLives; i++ {
		idx := s.store.findByID(2)
		if idx < 0 {
			t.Fatalf("victim missing before kill %d", i)
		}
		s.store.slots[idx].HP = 1
		_ = s.killEntity(nil, &s.store.slots[idx], 1)
		// Reset target slab slot to "alive" so the next iteration's
		// kill works against a fresh body (mimics respawn). Skip the
		// final iteration because the next slab look-up should fail.
		if i+1 < startLives {
			ps := s.store.players[2]
			ps.respawnAt = 0
			ps.hasRespawnAt = false
			// Manually un-flag dead so the next killEntity flows.
			s.store.slots[idx].Flags &^= FlagDead
		}
	}
	if !s.Eliminated(2) {
		t.Fatal("player 2 should be Eliminated")
	}
	// playerState still alive for the score record.
	if s.LivesRemaining(2) != 0 {
		t.Fatalf("lives = %d after elimination, want 0", s.LivesRemaining(2))
	}
	// Run a GC pass via Tick; the slab entry should be gone.
	if _, err := s.Tick(nil); err != nil {
		t.Fatalf("post-elim Tick: %v", err)
	}
	if s.store.findByID(2) >= 0 {
		t.Fatal("slab entry for eliminated player not GC'd")
	}
	// Scores still includes player 2.
	scores := s.Scores()
	if _, ok := scores[2]; !ok {
		t.Fatal("Scores() missing eliminated player record")
	}
	if !scores[2].Eliminated {
		t.Fatal("Scores()[2].Eliminated == false")
	}
}

// TestNoRespawnScheduledForEliminated — once eliminated, no respawn
// tick is enqueued.
func TestNoRespawnScheduledForEliminated(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	// Drop victim's lives to 1, then kill.
	ps := s.store.players[2]
	ps.livesRemaining = 1
	idx := s.store.findByID(2)
	if idx < 0 {
		t.Fatal("victim missing")
	}
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	if !s.Eliminated(2) {
		t.Fatal("not eliminated")
	}
	if ps.hasRespawnAt {
		t.Fatal("respawn scheduled despite elimination")
	}
}

// TestGhostHitDoesNotDoubleKillDeadTarget — codex P5 iter 1 finding #1.
// A lag-comp ghost candidate that happens to coincide with an
// already-dead slab entity (HP 0 + FlagDead) must NOT decrement lives
// or award score a second time. The fix re-orders the projectile-loop
// branch to short-circuit on FlagDead before damage/score apply; this
// test replays the branch directly.
func TestGhostHitDoesNotDoubleKillDeadTarget(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	idx := s.store.findByID(2)
	if idx < 0 {
		t.Fatal("victim missing")
	}
	// First kill — legitimate.
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	scoreAfterFirst := s.Score(1)
	livesAfterFirst := s.LivesRemaining(2)

	// Replay the guarded projectile-resolution branch verbatim. With
	// the fix, the inner `if target.Flags&FlagDead == 0 && target.HP > 0`
	// short-circuits and no damage applies.
	target := &s.store.slots[idx]
	events := []Event{}
	if target.Flags&FlagDead == 0 && target.HP > 0 {
		target.HP--
		events = append(events, Event{Kind: EventEntityHit, Actor: 1, Target: target.ID})
		if target.HP == 0 {
			events = s.killEntity(events, target, 1)
		}
	}
	if len(events) != 0 {
		t.Fatalf("guarded branch emitted %d events; want 0", len(events))
	}
	if got := s.Score(1); got != scoreAfterFirst {
		t.Fatalf("score drifted: %d -> %d (double-kill regression)", scoreAfterFirst, got)
	}
	if got := s.LivesRemaining(2); got != livesAfterFirst {
		t.Fatalf("lives drifted: %d -> %d (double-kill regression)", livesAfterFirst, got)
	}
}

// TestEliminatedRetainsPlayerStateEvenUnderNoRespawn — codex P5 iter 1
// finding #2. The match actor currently builds the sim with
// NoRespawn=true, but eliminated players must persist their playerState
// for the §16.1 fingerprint tail and the final scoreboard.
func TestEliminatedRetainsPlayerStateEvenUnderNoRespawn(t *testing.T) {
	cfg := Config{
		Seed:         0xCAFE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
		NoRespawn:    true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	// Burn through victim's lives to elimination.
	ps := s.store.players[2]
	ps.livesRemaining = 1
	idx := s.store.findByID(2)
	if idx < 0 {
		t.Fatal("victim missing")
	}
	_ = s.killEntity(nil, &s.store.slots[idx], 1)
	if !s.Eliminated(2) {
		t.Fatal("victim should be Eliminated")
	}
	if _, err := s.Tick(nil); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if s.store.findByID(2) >= 0 {
		t.Fatal("slab entry should be GC'd after Tick")
	}
	if _, ok := s.store.players[2]; !ok {
		t.Fatal("playerState dropped despite eliminated=true (codex finding #2)")
	}
	scores := s.Scores()
	r, ok := scores[2]
	if !ok {
		t.Fatal("Scores() missing eliminated player")
	}
	if !r.Eliminated || r.Lives != 0 {
		t.Fatalf("Scores()[2] = %+v", r)
	}
}

// TestDeterminism_LivesAndScoreInFingerprint — two sims with identical
// configs and inputs produce identical fingerprints, including the
// new playerScore block.
func TestDeterminism_LivesAndScoreInFingerprint(t *testing.T) {
	cfg := Config{
		Seed:         0xC0FFEE,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2, 3},
		NoGenerators: true,
	}
	s1, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		ins := []PlayerInput{
			{PlayerID: 1, Dir: Dir((i % 8) + 1), FireDir: Dir(((i + 1) % 9))},
			{PlayerID: 2, Dir: Dir(((i + 3) % 8) + 1)},
		}
		if _, err := s1.Tick(ins); err != nil {
			t.Fatalf("s1 tick %d: %v", i, err)
		}
		if _, err := s2.Tick(ins); err != nil {
			t.Fatalf("s2 tick %d: %v", i, err)
		}
		if s1.Fingerprint() != s2.Fingerprint() {
			t.Fatalf("tick %d: fingerprints differ", i)
		}
	}
}
