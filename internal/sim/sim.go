package sim

import (
	"math"
)

// Sim is the deterministic Phase 1 simulation core. See §5.
type Sim struct {
	cfg         Config
	maze        *maze
	mapBytes    []byte // immutable internal copy
	store       *entityStore
	entityPRNGs *entityPRNGs
	serverTick  uint32
	quiesced    bool

	// snapshot of player IDs in the order they were supplied.
	playerIDs []EntityID
}

// NewSim constructs a fully-initialised sim per §7 maze gen and the
// §8 entity-store rules. Returns a typed error on invalid Config.
func NewSim(cfg Config) (*Sim, error) {
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	res, err := generateMaze(cfg)
	if err != nil {
		return nil, err
	}

	// Establish nextID = max(PlayerIDs) + 1.
	var maxPID EntityID
	for _, id := range cfg.PlayerIDs {
		if id > maxPID {
			maxPID = id
		}
	}

	s := &Sim{
		cfg:         cfg,
		maze:        res.m,
		mapBytes:    packTiles(res.m),
		store:       newEntityStore(maxPID + 1),
		entityPRNGs: newEntityPRNGs(cfg.Seed),
		playerIDs:   append([]EntityID(nil), cfg.PlayerIDs...),
	}

	// Allocate players at acceptedSpawns[k] (§8).
	for k, pid := range cfg.PlayerIDs {
		idx := s.store.alloc()
		spawn := res.playerSpawns[k]
		s.store.slots[idx] = Entity{
			ID:     pid,
			Kind:   KindPlayer,
			HP:     playerHP,
			Facing: DirS,
			Flags:  0,
			X:      int32(spawn.X*subtilePerTile + subtilePerTile/2),
			Y:      int32(spawn.Y*subtilePerTile + subtilePerTile/2),
		}
		s.store.players[pid] = &playerState{lastDir: DirIdle}
	}

	// Allocate generators in tile row-major order.
	if !cfg.NoGenerators {
		// res.generatorTiles is in acceptance order, not row-major;
		// sort row-major for stable allocation.
		gens := append([]tilePos(nil), res.generatorTiles...)
		sortTilesRowMajor(gens, res.m.W)
		for _, g := range gens {
			id, ok := s.store.allocID()
			if !ok {
				return nil, ErrIDExhausted
			}
			idx := s.store.alloc()
			s.store.slots[idx] = Entity{
				ID:     id,
				Kind:   KindGenerator,
				HP:     generatorHP,
				Facing: DirS,
				Flags:  0,
				X:      int32(g.X*subtilePerTile + subtilePerTile/2),
				Y:      int32(g.Y*subtilePerTile + subtilePerTile/2),
			}
		}
	}

	return s, nil
}

func validateConfig(cfg *Config) error {
	if cfg.Width < minMapWidth || cfg.Width > maxMapWidth ||
		cfg.Height < minMapHeight || cfg.Height > maxMapHeight {
		return ErrInvalidMapSize
	}
	if len(cfg.PlayerIDs) == 0 {
		return ErrNoPlayers
	}
	if len(cfg.PlayerIDs) > maxPlayers {
		return ErrTooManyPlayers
	}
	seen := make(map[EntityID]struct{}, len(cfg.PlayerIDs))
	for _, id := range cfg.PlayerIDs {
		if id == 0 {
			return ErrZeroPlayerID
		}
		if _, dup := seen[id]; dup {
			return ErrDuplicatePlayerID
		}
		seen[id] = struct{}{}
	}
	headroom := EntityID(maxEntities * 4096)
	for _, id := range cfg.PlayerIDs {
		if id > math.MaxUint32-headroom {
			return ErrPlayerIDNoHeadroom
		}
	}
	return nil
}

func sortTilesRowMajor(t []tilePos, W int) {
	for i := 1; i < len(t); i++ {
		v := t[i]
		j := i - 1
		for j >= 0 && (t[j].Y*W+t[j].X) > (v.Y*W+v.X) {
			t[j+1] = t[j]
			j--
		}
		t[j+1] = v
	}
}

// Tile returns the tile at (tx, ty). Out-of-bounds reads return TileWall.
func (s *Sim) Tile(tx, ty int) Tile { return s.maze.at(tx, ty) }

// Width returns the maze width in tiles.
func (s *Sim) Width() int { return s.maze.W }

// Height returns the maze height in tiles.
func (s *Sim) Height() int { return s.maze.H }

// ServerTick returns the current tick count.
func (s *Sim) ServerTick() uint32 { return s.serverTick }

// LastInputTick returns the most-recently-consumed ClientTick for
// playerID, or 0 if none.
func (s *Sim) LastInputTick(playerID EntityID) uint16 {
	if ps, ok := s.store.players[playerID]; ok {
		return ps.lastInputTick
	}
	return 0
}

// MapBytes returns a freshly-allocated copy of the packed-tile bytes.
func (s *Sim) MapBytes() []byte {
	out := make([]byte, len(s.mapBytes))
	copy(out, s.mapBytes)
	return out
}

// Entities returns a snapshot copy of every occupied slot in ascending
// EntityID order.
func (s *Sim) Entities() []Entity {
	ids := s.store.liveIDsSorted()
	out := make([]Entity, 0, len(ids))
	for _, id := range ids {
		idx := s.store.findByID(id)
		if idx >= 0 {
			out = append(out, s.store.slots[idx])
		}
	}
	return out
}

// Tick advances the simulation one step (§11).
func (s *Sim) Tick(inputs []PlayerInput) ([]Event, error) {
	// Step 0: quiesce / ID-headroom preflight.
	if s.quiesced {
		return nil, ErrIDExhausted
	}
	livePlayers := 0
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 || e.Kind != KindPlayer {
			continue
		}
		if e.Flags&FlagDead == 0 {
			livePlayers++
		}
	}
	if uint64(s.store.nextID)+uint64(livePlayers) > math.MaxUint32 {
		s.quiesced = true
		return nil, ErrIDExhausted
	}

	// Step 1: serverTick++.
	s.serverTick++

	// Step 2: build per-player input map. Sanitize Dir/FireDir to the
	// 0..8 range; out-of-range values become DirIdle so the wire
	// invariant (Entity.facing ∈ {1..8}) cannot be corrupted by a
	// malformed client message.
	inputMap := make(map[EntityID]PlayerInput, len(inputs))
	for _, inp := range inputs {
		if inp.Dir > DirNW {
			inp.Dir = DirIdle
		}
		if inp.FireDir > DirNW {
			inp.FireDir = DirIdle
		}
		inputMap[inp.PlayerID] = inp
	}

	events := make([]Event, 0, 16)

	// Step 3: apply input to live players (ID-sorted).
	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind != KindPlayer {
			continue
		}
		if e.Flags&FlagDead != 0 {
			// Dead players: keep velocity 0, facing intact.
			e.VX, e.VY = 0, 0
			continue
		}
		ps := s.store.players[id]
		inp, hasInput := inputMap[id]
		// Decrement fireCooldown.
		if ps.fireCooldown > 0 {
			ps.fireCooldown--
		}
		if !hasInput {
			e.VX, e.VY = 0, 0
			// Facing retained.
			continue
		}
		// Persist ClientTick.
		ps.lastInputTick = inp.ClientTick

		// Turbo lock (§10.6).
		effectiveDir := inp.Dir
		var turboActive bool
		if inp.Turbo && inp.Dir != DirIdle {
			if ps.lastDir == DirIdle {
				ps.lastDir = inp.Dir
				effectiveDir = inp.Dir
			} else {
				effectiveDir = ps.lastDir
			}
			turboActive = true
		} else if inp.Turbo && inp.Dir == DirIdle {
			// Turbo-cancel-via-idle.
			ps.lastDir = DirIdle
			effectiveDir = DirIdle
			turboActive = false
		} else {
			// Turbo not held.
			ps.lastDir = DirIdle
			effectiveDir = inp.Dir
			turboActive = false
		}

		// Update facing iff effectiveDir != DirIdle.
		if effectiveDir != DirIdle {
			e.Facing = effectiveDir
		}

		// Apply turbo flag.
		if turboActive {
			e.Flags |= FlagTurbo
		} else {
			e.Flags &^= FlagTurbo
		}

		// Velocity.
		var speed int32 = playerSpeed
		if turboActive {
			speed = playerTurboSpeed
		}
		vx, vy := velocityFor(effectiveDir, speed)
		e.VX, e.VY = int16(vx), int16(vy)
	}

	// Step 4: move live players (ID-sorted) with wall + generator AABBs.
	solids := s.collectGeneratorAABBs()
	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind != KindPlayer || e.Flags&FlagDead != 0 {
			continue
		}
		if e.VX == 0 && e.VY == 0 {
			continue
		}
		nx, ny, nvx, nvy := moveAndSlide(s.maze, e.X, e.Y, int32(e.VX), int32(e.VY), playerHalfExt, solids)
		e.X, e.Y = nx, ny
		e.VX, e.VY = int16(nvx), int16(nvy)
	}

	// Step 5: resolve projectile motion + collision (ID-sorted).
	// Build candidates: every live, non-projectile entity.
	preTickProjIDs := make([]EntityID, 0, 16)
	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		if s.store.slots[idx].Kind == KindProjectile {
			preTickProjIDs = append(preTickProjIDs, id)
		}
	}
	for _, pid := range preTickProjIDs {
		idx := s.store.findByID(pid)
		if idx < 0 {
			continue
		}
		proj := &s.store.slots[idx]
		projState := s.store.projectiles[pid]
		shooterID := projState.shooterID
		// Build candidates fresh each projectile (other entities may
		// have died earlier in this step).
		candidates := s.collectHitCandidates()
		res := resolveProjectile(s.maze, proj, shooterID, candidates)
		switch res.kind {
		case projHitNone:
			proj.X, proj.Y = res.endX, res.endY
		case projHitWall:
			proj.X, proj.Y = res.endX, res.endY
			events = append(events, Event{Kind: EventEntityKill, Actor: 0, Target: pid, Reason: 1})
			s.store.remove(pid)
		case projHitEntity:
			proj.X, proj.Y = res.endX, res.endY
			tidx := s.store.findByID(res.targetID)
			if tidx >= 0 {
				target := &s.store.slots[tidx]
				if target.HP > 0 {
					target.HP--
				}
				events = append(events, Event{Kind: EventEntityHit, Actor: shooterID, Target: res.targetID, Reason: 0})
				if target.HP == 0 {
					events = s.killEntity(events, target, shooterID)
				}
			}
			s.store.remove(pid)
		}
	}

	// Step 6: decrement projectile lifetimes for projectiles that
	// survived step 5 *and* existed before this tick.
	for _, pid := range preTickProjIDs {
		idx := s.store.findByID(pid)
		if idx < 0 {
			continue
		}
		projState := s.store.projectiles[pid]
		if projState.lifetime > 0 {
			projState.lifetime--
		}
		if projState.lifetime == 0 {
			events = append(events, Event{Kind: EventEntityKill, Actor: 0, Target: pid, Reason: 2})
			s.store.remove(pid)
		}
	}

	// Step 7: firing for live, non-turbo players (ID-sorted).
	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind != KindPlayer || e.Flags&FlagDead != 0 {
			continue
		}
		ps := s.store.players[id]
		inp, hasInput := inputMap[id]
		if !hasInput || inp.FireDir == DirIdle {
			continue
		}
		if e.Flags&FlagTurbo != 0 {
			continue
		}
		if ps.fireCooldown > 0 {
			continue
		}
		// Live projectile count.
		liveProj := 0
		for j := range s.store.slots {
			if s.store.slots[j].ID != 0 && s.store.slots[j].Kind == KindProjectile {
				liveProj++
			}
		}
		if liveProj >= maxInFlightProjectiles {
			continue
		}
		// Allocate projectile.
		pid, ok := s.store.allocID()
		if !ok {
			s.quiesced = true
			return nil, ErrIDExhausted
		}
		ps.fireCooldown = fireCooldownTicks
		pIdx := s.store.alloc()
		vx, vy := velocityFor(inp.FireDir, projectileSpeed)
		s.store.slots[pIdx] = Entity{
			ID:     pid,
			Kind:   KindProjectile,
			HP:     1,
			Facing: inp.FireDir,
			Flags:  0,
			X:      e.X,
			Y:      e.Y,
			VX:     int16(vx),
			VY:     int16(vy),
		}
		s.store.projectiles[pid] = &projectileState{
			lifetime:  projectileLifetime,
			shooterID: id,
		}
		events = append(events, Event{Kind: EventEntitySpawn, Actor: id, Target: pid, Reason: 0})
	}

	// Step 8: respawn timers (ID-sorted).
	for _, id := range s.store.liveIDsSorted() {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind != KindPlayer || e.Flags&FlagDead == 0 {
			continue
		}
		ps := s.store.players[id]
		if !ps.hasRespawnAt || ps.respawnAt != s.serverTick {
			continue
		}
		// Select tile.
		t, ok := s.selectRespawnTile(id)
		if !ok {
			ps.respawnAt = s.serverTick + 1
			continue
		}
		// Reset player in place.
		e.Flags = 0
		e.HP = playerHP
		e.X = int32(t.X*subtilePerTile + subtilePerTile/2)
		e.Y = int32(t.Y*subtilePerTile + subtilePerTile/2)
		e.VX, e.VY = 0, 0
		e.Facing = DirS
		ps.fireCooldown = 0
		ps.lastDir = DirIdle
		ps.hasRespawnAt = false
		events = append(events, Event{Kind: EventEntitySpawn, Actor: 0, Target: id, Reason: 0})
	}

	// Step 9: GC.
	s.garbageCollect()

	// Step 10: return.
	return events, nil
}

// killEntity runs the §10.4 pipeline for one dying entity. Appends
// events for entity_kill (and generator_destroyed if applicable).
func (s *Sim) killEntity(events []Event, e *Entity, killer EntityID) []Event {
	e.Flags = FlagDead
	e.VX, e.VY = 0, 0
	events = append(events, Event{Kind: EventEntityKill, Actor: killer, Target: e.ID, Reason: 0})
	switch e.Kind {
	case KindPlayer:
		ps := s.store.players[e.ID]
		ps.deathX, ps.deathY = e.X, e.Y
		ps.fireCooldown = 0
		ps.lastDir = DirIdle
		if !s.cfg.NoRespawn {
			ps.respawnAt = s.serverTick + respawnTimerTicks
			ps.hasRespawnAt = true
		}
	case KindGenerator:
		events = append(events, Event{Kind: EventGeneratorDestroyed, Actor: killer, Target: e.ID, Reason: 0})
	}
	return events
}

// garbageCollect removes dead generators and (under NoRespawn) dead
// players. Projectile slots were freed inline. §11 step 9.
func (s *Sim) garbageCollect() {
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 {
			continue
		}
		if e.Flags&FlagDead == 0 {
			continue
		}
		switch e.Kind {
		case KindGenerator:
			id := e.ID
			s.store.remove(id)
		case KindPlayer:
			if s.cfg.NoRespawn {
				id := e.ID
				s.store.remove(id)
			}
		}
	}
}

func (s *Sim) collectGeneratorAABBs() []aabb {
	out := make([]aabb, 0, 8)
	for i := range s.store.slots {
		e := &s.store.slots[i]
		if e.ID == 0 || e.Kind != KindGenerator || e.Flags&FlagDead != 0 {
			continue
		}
		out = append(out, aabb{cx: e.X, cy: e.Y, he: generatorHalfExt})
	}
	return out
}

// collectHitCandidates returns pointers to every live non-projectile
// entity. Pointers are into the slab; safe to use during the current
// tick step.
func (s *Sim) collectHitCandidates() []*Entity {
	ids := s.store.liveIDsSorted()
	out := make([]*Entity, 0, len(ids))
	for _, id := range ids {
		idx := s.store.findByID(id)
		if idx < 0 {
			continue
		}
		e := &s.store.slots[idx]
		if e.Kind == KindProjectile {
			continue
		}
		if e.Flags&FlagDead != 0 {
			continue
		}
		out = append(out, e)
	}
	return out
}
