# Phase 3 — Snipes & generators AI

This document is the buildable, testable expansion of §8 Phase 3 of
[`SPEC.md`](./SPEC.md). It assumes Phase 1 (`internal/sim`) and
Phase 2 (`internal/proto` / `internal/match` / `internal/net` /
`internal/lobby` / `cmd/isnipes`) are on `main`.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§3.5" point into `SPEC.md`; "P1 §x", "P2 §x" point into the
earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Add AI snipes and generator emission to the deterministic
sim, plus the A–Z × 1–9 level table that scales their parameters.
Specifically:

1. A new `KindSnipe` (= 4) entity with a deterministic state machine
   (`IDLE → PATROL → CHASE → ATTACK → DEAD`).
2. Generator entities gain a deterministic emission timer that spawns
   snipes per §3.6 of `SPEC.md`.
3. Tile-grid BFS pathfinding for snipes in `CHASE`/`ATTACK`, with
   staggered per-snipe recomputation per §3.5.
4. Tile raycast for line-of-sight from snipe to player.
5. Snipe firing into the §10 projectile pipeline (snipe-owned
   projectiles damage players; friendly-fire among snipes is off).
6. `Config.LevelLetter` (A–Z) and `Config.LevelNumber` (1–9) gate the
   §3.7 parameter table.
7. The §3.8.1 `PVE_COMPLETE` end-reason becomes reachable; Phase 2's
   match actor is updated to evaluate it.

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./internal/sim/...` is green on the four
   SPEC §12-mandated native CI runners (linux/amd64, linux/arm64,
   darwin/arm64, windows/amd64). Phase 1's
   `TestDeterminism_GoldenFingerprint` continues to pass byte-for-byte
   (Phase 3 adds new entity kinds and per-snipe PRNG state; the golden
   replay is **regenerated** to include them).
2. Coverage ≥ 80 % statements for `internal/sim`. New files
   (`levels.go`, `ai.go`, `bfs.go`, `los.go`, `snipe.go`,
   `generator_emit.go`) each have direct tests in `*_test.go`
   counterparts.
3. `go test -race -count=1 ./internal/match/...` green: the
   match-actor changes (PVE_COMPLETE evaluation, snipe events through
   the wire) pass their unit tests.
4. A new committed replay fixture
   `testdata/replays/phase3_pve.{inputs,hash}` reproduces byte-for-byte
   on the four CI runners. It encodes one player + the level-9 default
   (11 generators, 54-snipe cap) and a scripted input stream that
   destroys 3 generators and ≥ 10 snipes, ending in `PVE_COMPLETE`.
5. `BenchmarkSimTick_60x40_8P_Level9` (8 players + 11 generators + up
   to 54 snipes + up to 64 projectiles): ≤ **3 ms/op** on
   `ubuntu-latest` without `-race`, ≤ **12 ms/op** under `-race`. The
   limit is 3× P1's 1 ms/op target — Phase 3 adds substantially more
   per-tick work and the SPEC's broader bandwidth/CPU envelope (§5.3)
   accommodates this.
6. The §3.5 snipe state machine is **deterministic across replays**:
   two `Sim` instances with the same `Config` and the same scripted
   inputs evolve identical snipe state at every tick. Verified by
   `TestDeterminism_SnipeAI`.
7. Every §3.7 letter bucket (A–F, G–M, N–S, T–Z) and every level
   number (1–9) has at least one unit test asserting its derived
   parameters (`TestLevelParams_<bucket>`).

---

## 2. Out of scope

Explicitly **not** built in Phase 3:

- Client prediction / interpolation / lag compensation (Phase 4).
- Lives, respawn, dead-cam, scoring (Phase 5; `PVE_COMPLETE` end-
  evaluation in the match actor is in scope, but the *score and lives*
  numbers it depends on remain zero-stub until Phase 5).
- Snipe ricochet projectiles (v1.1 candidate per §3.7).
- The full Playwright e2e in `web/` — Phase 3 doesn't change the wire
  protocol's structure (snipes ride the existing `Entity` schema with
  `Kind=4`), so the schemaChecksum is unchanged and the existing TS
  client's decode path continues to work.

Phase 3 also does **not** add any new binary wire message; snipe
events ride the existing §6.3 `Event` and `Snapshot` frames. The
schemaDescriptor string is unchanged from Phase 2 and the committed
`testdata/proto/checksum.txt` value (`0x42607394`) MUST not change.

---

## 3. Prerequisites and assumptions

- Phase 1's `internal/sim` public API in `PHASE1.md` §5.
- Phase 2's `internal/match` accepts P1 `Config` and wires the sim
  events to the wire.
- Go 1.22 or newer; `math/rand/v2` PCG.
- No new third-party Go deps. `internal/sim` continues to import
  stdlib only.

---

## 4. Package and file layout

New files in Phase 3, all under `internal/sim/`:

```
internal/sim/
├── levels.go               # NEW — A–Z × 1–9 parameter table
├── levels_test.go          # NEW
├── ai.go                   # NEW — snipe state machine driver
├── ai_test.go              # NEW
├── bfs.go                  # NEW — tile-grid BFS pathfinder
├── bfs_test.go             # NEW
├── los.go                  # NEW — tile raycast for line-of-sight
├── los_test.go             # NEW
├── snipe.go                # NEW — KindSnipe entity helpers + allocation
├── snipe_test.go           # NEW
├── generator_emit.go       # NEW — per-generator emission timer
└── generator_emit_test.go  # NEW
```

Existing files modified by Phase 3:

```
internal/sim/
├── config.go     # adds Config.LevelLetter, Config.LevelNumber, KindSnipe const
├── entity.go     # adds snipeState + per-snipe sidecar map
├── sim.go        # NewSim wires generators+snipes when level is set; Tick
│                 #   §11 step 4.5 runs AI, step 7 includes snipe firing,
│                 #   step 9 GC includes dead snipes
├── fingerprint.go# adds snipe per-entity fields to §12.1 enumeration
├── physics.go    # no change required — snipes use existing moveAndSlide
│                 # with the level-table-derived halfExt = 80
└── combat.go     # adds shooter-class checks: snipe-fired projectiles
                  #   do NOT collide with other snipes
```

`internal/match/match.go` gains `evaluateMatchEnd` cases for
`PVE_COMPLETE` (every generator destroyed + every snipe destroyed +
≥ 1 live player). No new file in `internal/match`.

`internal/proto/` is **untouched** — the wire schema is unchanged.

---

## 5. Public API additions

```go
// KindSnipe matches the reserved enum slot from P1 §5.
const KindSnipe EntityKind = 4

type Config struct {
    Seed         uint32
    Width        int
    Height       int
    PlayerIDs    []EntityID
    NoRespawn    bool
    NoGenerators bool

    // Phase 3 additions:
    // LevelLetter is 'A'..'Z' (case-insensitive on input; canonicalised
    // upper). The zero value (byte 0) means "use Phase 1 defaults"
    // (no snipes, level-table parameters not applied) — this is what
    // Phase 2's PvP-only match passes.
    LevelLetter byte

    // LevelNumber is 1..9. The zero value means "use Phase 1 defaults"
    // (no scaling). When LevelLetter is set, LevelNumber MUST also be
    // set; mismatches return ErrInvalidLevel.
    LevelNumber int
}

// Errors.
var ErrInvalidLevel = errors.New(
    "isnipes/sim: Config.LevelLetter / LevelNumber out of range " +
    "or only one set")
```

Both `LevelLetter` and `LevelNumber` must be set together. Setting
exactly one returns `ErrInvalidLevel` from `NewSim`. Setting both with
`LevelLetter ∉ 'A'..'Z'` or `LevelNumber ∉ [1, 9]` also returns
`ErrInvalidLevel`.

The `EntityKind` values for `Snapshot.Entity.Kind` now include `4`
(snipe). The §6.3.3 wire format accepts this naturally (the field is
already `u8`). No proto changes.

Phase 3 emits these event kinds that Phase 2 did not:

- `entity_spawn{actor: generatorID, target: snipeID, reason: 0}` —
  fired by `generator_emit.go` on each successful spawn.
- `entity_kill{actor: shooterID, target: snipeID, reason: 0}` — when
  a player's projectile kills a snipe (existing P1 logic; the
  difference is the target is now a snipe).
- `entity_hit{actor: snipeID, target: playerID, reason: 0}` — when a
  snipe-fired projectile hits a player.

No new event kinds are added; existing P2 kinds (`entity_spawn`,
`entity_hit`, `entity_kill`, `generator_destroyed`) cover everything.

---

## 6. Level table (§3.7)

`internal/sim/levels.go` exposes:

```go
// LevelParams is the resolved per-(letter, number) tuning.
type LevelParams struct {
    SnipeSpeed       int32  // subtile/tick
    SnipeFireCooldown uint16 // ticks
    SnipeLeadFactor  uint8  // 0=no lead, 1=half lead, 2=full lead
    LOSRadius        int    // tiles
    GeneratorHP      uint8
    MaxSnipesTotal   int    // global snipe cap
    InitialGenerators int   // count at NewSim
    PlayerLives      int    // Phase 5; recorded here for forward-compat
}

// LookupLevel returns the params for (letter, number).
// Caller validates inputs; this function panics on out-of-range.
func LookupLevel(letter byte, number int) LevelParams
```

### 6.1 Letter buckets

The letter selects qualitative AI behavior (§3.7). The mapping is
**inclusive at the bucket boundary**; the four buckets are:

| Bucket | Letters | LOS (tiles) | Lead | Fire cd (ticks) | Snipe speed | Gen HP |
|---|---|---:|---|---:|---:|---:|
| Easy   | A..F | 6  | none (0) | 20 | 12 | 3 |
| Medium | G..M | 8  | half (1) | 20 | 12 | 3 |
| Hard   | N..S | 10 | full (2) | 15 (= 20×0.75 floored) | 12 | 3 |
| Brutal | T..Z | 14 | full (2) | 15 | 15 (= 12×1.25 floored) | 5 |

### 6.2 Number scaling

Independent of the letter:

| Field | Formula | Range (1→9) |
|---|---|---|
| `MaxSnipesTotal` | `n × 6` | 6 → 54 |
| `InitialGenerators` | `n + 2` | 3 → 11 |
| `PlayerLives` | `max(1, 10 - n)` | 9 → 1 |

### 6.3 Reconciliation with Phase 1 generator placement

Phase 1's §7.0.1 caps generator count by interior area. Phase 3's
level-table value may exceed that cap on small maps; the generator
placer in `maze.go` clamps `generatorCount` to `min(LevelTable,
GeometricCap)`. On clamp, a `WARN`-level slog event is emitted
(`level=info`-and-above by default) so operators see when a level's
nominal generator count couldn't be honored.

The clamp is **deterministic**: same `(Seed, W, H, level)` always
clamps to the same value. The §13.4 `TestLevelGeneratorPlacementCap`
test exercises this.

### 6.4 Implementation-binding contract

`LookupLevel` is **pure** (no side effects, no PRNG, no `time.Now`).
`TestLevelParams_<bucket>` and `TestLevelNumberScaling_<n>` assert
exact values per the §6.1/§6.2 tables. Changing any entry without
updating the corresponding test is a Phase 3 contract violation.

---

## 7. Snipe entity

### 7.1 Allocation

Snipes are allocated from the §8 P1 slab using `allocID` like any
other dynamic entity. Snipe `EntityID` values fall in the same
monotonic ID space as projectiles.

`Entity` for a snipe:

- `Kind = KindSnipe` (4)
- `HP = 1`
- `Facing` is set to the direction *away from* the parent generator
  at spawn time (computed as the 8-direction Dir8 best matching the
  vector from generator centre to snipe centre).
- `Flags = FlagSpawnInvuln` for the first **15 ticks** (§3.6
  "0.5 s post-spawn invulnerability"); cleared by AI step on tick
  16+. While `FlagSpawnInvuln` is set the snipe cannot be hit by any
  projectile (existing P1 `resolveProjectile` filter is extended).
- `X, Y` = the centre of the chosen spawn tile.
- `VX, VY = 0` on spawn.

### 7.2 Unexported per-snipe state

```go
type snipeState struct {
    parentGen    EntityID  // generator that spawned this snipe; 0 after gen dies
    aiState      AIState
    aiTimer      uint16    // ticks until next AI re-evaluation event
    lastBFSTick  uint32    // last serverTick we recomputed the BFS
    bfsPath      []tilePos // cached path to the chase target
    chaseTarget  EntityID  // the player we're chasing; 0 if not chasing
    losLostTicks uint16    // consecutive ticks without LOS in CHASE
    fireCooldown uint16
}
```

`AIState` is an exported `uint8` mirroring §3.5's state machine names:

```go
type AIState uint8
const (
    AIStateIdle    AIState = 0  // 0..15 ticks post-spawn (invulnerable)
    AIStatePatrol  AIState = 1
    AIStateChase   AIState = 2
    AIStateAttack  AIState = 3
    AIStateDead    AIState = 4
)
```

Snipes in `AIStateDead` are GC'd on the same tick by §11 step 9 (per
P1 §10.4 generator GC pattern). The `AIState` enum is also exposed
for tests via `export_test.go`'s `SnipeStateForTest(s, id)`.

### 7.3 Snipe sidecar map on entityStore

`entityStore` (P1 `entity.go`) gains:

```go
type entityStore struct {
    // existing P1 fields...
    snipes map[EntityID]*snipeState
}
```

`alloc`, `remove`, etc. are extended to clean up the snipes map. The
map is never iterated in a way that affects sim state (per P1 §12
determinism rule); lookups are by key only.

---

## 8. BFS pathfinding (§3.5)

`internal/sim/bfs.go` exposes:

```go
// PathNext returns the next tile in the BFS-optimal 4-neighborhood
// path from (sx, sy) to (tx, ty), treating WALL tiles as blocked.
// Returns (0, 0, false) if no path exists within maxDepth tiles.
func PathNext(m *maze, sx, sy, tx, ty, maxDepth int) (int, int, bool)
```

### 8.1 Algorithm

Standard breadth-first search:

1. If `(sx, sy) == (tx, ty)`: return source (degenerate; the AI
   shouldn't call PathNext when already on target).
2. Initialise visited bitmap of size `W*H`.
3. Push `(sx, sy)` onto a FIFO queue with depth `0`. Mark visited.
4. While queue non-empty AND depth ≤ maxDepth:
   - Pop front `(x, y, d, firstStepDir)`.
   - For each of N, E, S, W in fixed order (matching P1 §7.3's order
     for determinism): compute `(nx, ny)`. If out of bounds, `WALL`,
     or already visited, skip.
   - If `(nx, ny) == (tx, ty)`: return the first-step direction
     recorded when this branch was extended from the source.
   - Else push `(nx, ny, d+1, firstStepDir-or-this-step)`.
5. If the loop exits without reaching the target: return `false`.

### 8.2 Determinism and cost

The fixed neighbour iteration order makes BFS deterministic — two
sims with the same maze always pick the same first step on ties.

Cost: O(W·H) worst case = O(9600) for a 120×80 map. With the
`maxDepth = LOS_RADIUS + 4` cap (max 18 tiles), the practical search
visits ≤ ~300 tiles per call. Each snipe recomputes every 15 ticks
per §3.5; at 54 snipes that's ≤ 4 BFS/tick on average.

**Staggering.** To smooth CPU per tick, the recompute schedule is
keyed off `(snipeID + serverTick) mod 15 == 0`. Each snipe
recomputes at a tick offset determined by its `EntityID`'s mod 15,
not all on the same tick.

### 8.3 Path cache

Between recomputes the AI follows `snipeState.bfsPath`. When path
becomes empty OR the path's leading tile is now blocked (e.g. another
snipe moved into it — see §9 collision), AI re-evaluates: either
pop the next tile and continue, or transition to a state that
recomputes.

The cached path stores **only** the next 3 tiles (we don't need the
full path each recompute). On reaching the end of a cached prefix
without seeing the target, AI re-fires BFS.

---

## 9. Line-of-sight (tile raycast)

`internal/sim/los.go` exposes:

```go
// HasLOS reports whether a tile-grid raycast from (sx, sy) to (tx, ty)
// is unblocked by WALL tiles, *exclusive* of both endpoints.
// Distance in Chebyshev tiles must be ≤ maxRange; LOS to a tile
// further than maxRange is "no LOS" regardless of intervening walls.
func HasLOS(m *maze, sx, sy, tx, ty, maxRange int) bool
```

### 9.1 Algorithm

Standard supercover Bresenham (visits every tile the line segment
crosses). For each visited tile EXCEPT the source and target, check
`m.at(x, y) != TileWall`. The supercover variant handles diagonal
walls correctly (a wall corner blocks LOS to the diagonally-opposite
floor tile).

The supercover algorithm uses **integer-only** arithmetic to maintain
P1 §12's determinism rules: no `math.Sqrt`, no `float64` ratios.

### 9.2 Range gate

Before running the raycast, `HasLOS` does a cheap Chebyshev distance
check: `max(|sx-tx|, |sy-ty|) > maxRange` → return false immediately.
This lets snipes O(1)-reject far-away players before raycasting.

---

## 10. Snipe AI driver (§3.5)

`internal/sim/ai.go` exposes:

```go
// stepSnipeAI advances one snipe's AIState by one tick. Called from
// sim.go's §11 step 4.5 (between player movement and projectile
// motion). Mutates the snipe's Entity and snipeState in place.
func (s *Sim) stepSnipeAI(snipe *Entity, ss *snipeState)
```

The function dispatches on `ss.aiState`:

### 10.1 `AIStateIdle` (ticks 0..14 post-spawn)

- Velocity is zero.
- `FlagSpawnInvuln` is set.
- On tick 15 (counted from `entitySpawnTick`, recorded in
  `snipeState`), transition to `AIStatePatrol` AND clear
  `FlagSpawnInvuln`.

### 10.2 `AIStatePatrol`

- If `aiTimer == 0`, pick a new cardinal direction via
  `entityRand(snipe.ID).IntN(4)` (mapped to N, E, S, W in the fixed
  order from §7.3). Set `aiTimer = 30 + entityRand.IntN(61)` (30..90
  inclusive), set facing accordingly.
- Each tick, decrement `aiTimer`. If movement is blocked by a wall
  this tick (the moveAndSlide reduced velocity to 0 on the chosen
  axis), reset `aiTimer = 0` so the next tick picks a new direction.
- Per tick: apply velocity = `SnipeSpeed` in the chosen direction
  (using §9.2 P1's `velocityFor` with the level-derived speed).
- **LOS check** (every tick): for each live player whose tile-
  Chebyshev distance from this snipe is ≤ `LOSRadius` AND `HasLOS`
  returns true, transition to `AIStateChase`. The first player found
  in ascending `EntityID` order wins (determinism).

### 10.3 `AIStateChase`

- If `chaseTarget` is dead OR out of LOS for ≥ **60 ticks**
  (`losLostTicks ≥ 60`): clear `chaseTarget`, transition to
  `AIStatePatrol` (resetting `aiTimer = 0` so a new direction is
  drawn on the next tick).
- Every tick: re-check LOS to `chaseTarget`. If LOS regained,
  `losLostTicks = 0`. If lost, `losLostTicks++`.
- Recompute BFS every **15 ticks** per the §8.2 stagger schedule
  (`(snipe.ID + serverTick) % 15 == 0`). On recompute, store the next
  3 tiles in `bfsPath`.
- Apply velocity toward the next tile in `bfsPath`. When the snipe
  has *centred* on a path tile (its centre is within 16 subtiles of
  the tile centre), pop the head of `bfsPath`.
- **Attack gate**: if LOS exists AND the
  **projectile path is clear** (a raycast from the snipe along the
  Dir8 best-matching the vector to the player), transition to
  `AIStateAttack`.

### 10.4 `AIStateAttack`

- Velocity = 0 (snipes stop to shoot — matches the original game's
  feel).
- If `fireCooldown > 0`: decrement, stay in `AIStateAttack`.
- Else:
  - Compute the **fire direction** as Dir8 toward the player's
    *lead-adjusted* position:
    - Lead distance vector = `(playerVX * dist_in_ticks, playerVY *
      dist_in_ticks) * SnipeLeadFactor / 2`
      where `dist_in_ticks = |snipePos - playerPos| / proj_speed`.
    - Round to nearest Dir8.
    - SnipeLeadFactor 0 → no lead (aim at current position).
    - SnipeLeadFactor 1 → half lead (vector / 2).
    - SnipeLeadFactor 2 → full lead.
  - Allocate a `KindProjectile` entity at the snipe's centre, with
    that fire direction, speed 32, lifetime 90, **shooterID = snipe's
    EntityID**.
  - `fireCooldown = SnipeFireCooldown` (level-derived).
  - Stay in `AIStateAttack` for this tick.
- After firing, on the **next tick**, re-evaluate: if LOS still
  exists, stay in `AIStateAttack`; else transition back to
  `AIStateChase`.

### 10.5 `AIStateDead`

- Set by the projectile-hit handler when snipe HP → 0.
- §11 step 9 GC removes the entity on the same tick the kill event
  fires.

### 10.6 Iteration order

Per P1 §12, the AI pass iterates snipes in **ascending EntityID**
order. Multiple snipes choosing the same path tile on the same tick
break ties by ID (lower ID gets the tile first; the higher-ID snipe's
movement is blocked by the §11.4 snipe-vs-snipe weak block).

### 10.7 Snipe-vs-snipe weak block

Per §3.3, snipes block other snipes "weakly (cheap separation
steering)". Phase 3 implements this as:

- During the move pass (after applying player movement), each snipe
  with non-zero velocity tests its post-move AABB against every other
  live snipe's AABB. On overlap:
  - The lower-`EntityID` snipe keeps its position.
  - The higher-`EntityID` snipe's velocity component pointing into
    the overlap is zeroed on the dominant axis (X if |dx| > |dy|,
    else Y), and its position is clamped back by 1 subtile.

This is intentionally crude — the SPEC says "cheap separation
steering". A more sophisticated push-out would be Phase 7+.

---

## 11. Generator emission (§3.6)

`internal/sim/generator_emit.go` exposes:

```go
// stepGeneratorEmission runs one generator's emission timer for one
// tick. Called from sim.go §11 step 4.6 (after AI but before projectile
// motion). Returns events to append.
func (s *Sim) stepGeneratorEmission(gen *Entity, gs *generatorState) []Event
```

### 11.1 Generator sidecar state

```go
type generatorState struct {
    emitCooldown uint16 // ticks until next emission attempt
    // rotation: a per-generator 0..7 offset for the §3.6
    // "N, E, S, W, NE, SE, SW, NW for that generator's EntityID-
    // derived rotation" rule. Computed as gen.ID % 8 at allocation.
    rotation uint8
}
```

`entityStore` gains `generators map[EntityID]*generatorState`,
created/destroyed alongside the entity.

### 11.2 Initial cooldown

On generator allocation (NewSim), `emitCooldown` is set to a per-gen
draw from `entityRand(gen.ID)` in `[120, 240]` (= 4..8 seconds at
30 Hz). This spreads first-emission times across generators.

### 11.3 Per-tick logic

1. If `emitCooldown > 0`, decrement and return (no events).
2. If the **global snipe cap** is reached (`len(liveSnipes) ≥
   MaxSnipesTotal`): emission **suppressed** but cooldown not reset
   (§3.5 explicit). Return.
3. Walk the 8 neighbouring tiles in the per-generator rotation order:
   - Skip if tile is not `TileFloor`.
   - Skip if any entity's AABB overlaps the spawn-position AABB.
   - Skip if the snipe AABB (half-extent 80, level-bucket-derived)
     would overlap a wall (out-of-corridor case).
   - First slot that satisfies all three: use it.
4. If no slot satisfies: emission suppressed; cooldown not reset.
   Return.
5. On successful slot: allocate a `KindSnipe` entity at the slot's
   centre. Compute facing as Dir8 best-matching the vector from
   generator centre to slot centre. Set `Flags |= FlagSpawnInvuln`.
   Set `snipeState.aiState = AIStateIdle, aiTimer = 0, parentGen = gen.ID`.
6. Reset `emitCooldown` to the next per-gen interval: a fresh
   `entityRand(gen.ID).IntN(121) + 120` draw (120..240 ticks).
7. Emit `Event{Kind: entity_spawn, Actor: gen.ID, Target: snipe.ID,
   Reason: 0}`.

### 11.4 Iteration order

§11 step 4.6 iterates generators in **ascending EntityID** order
(P1 §12 determinism). Two generators whose cooldown reaches 0 on the
same tick emit in ID order.

### 11.5 Generator destruction

Unchanged from P1: HP reaches 0, generator is `Flags |= FlagDead`,
removed by §11 step 9 GC the same tick. Phase 3 additionally:

- The generator's `generatorState` entry is removed from
  `entityStore.generators`.
- Any snipe whose `snipeState.parentGen == gen.ID` has its
  `parentGen` set to 0 (purely diagnostic; doesn't affect AI).

---

## 12. Combat extensions

### 12.1 Snipe damage

Projectile-hit handling (P1 §10.2) is extended so that:

- A **snipe-fired projectile** (shooter is `KindSnipe`) does NOT
  collide with other snipes — the §10.2 step 3 candidate filter
  excludes targets where both `e.Kind == KindSnipe` AND
  `proj.shooter is a KindSnipe entity`. The shooter-self exclusion
  already prevents self-hits.
- A **snipe-fired projectile** hitting a player decrements the
  player's HP by 1 (existing P1 logic; the difference is just that
  the shooter is now a snipe entity). The §6.4 event translation
  emits `entity_hit{actor: snipe.ID, target: player.ID, reason: 0}`.
- A **player-fired projectile** hitting a snipe is the standard hit
  path. On HP=0, the snipe enters `AIStateDead` and is GC'd this tick.
  The `entity_kill` event uses the shooter as `actor`.

### 12.2 Spawn-invulnerability

`Flags & FlagSpawnInvuln != 0` short-circuits the §10.2 entity-hit
candidate filter: invulnerable entities are NOT in the candidate list.
This applies uniformly to any entity with the flag (Phase 5 will reuse
it for respawn-invuln).

### 12.3 Friendly-fire matrix

| Shooter | Target | Damage? |
|---|---|---|
| Player | Player | yes (FFA) |
| Player | Snipe | yes |
| Player | Generator | yes |
| Snipe | Player | yes |
| Snipe | Snipe | **no** (§12.1 above) |
| Snipe | Generator | no (snipes can't damage their own gen lineage) |
| Snipe | shooter | no (existing P1 self-filter) |

The "Snipe shoots Generator" no-damage rule is implemented as a
single check in §10.2 step 3: if `proj.shooter is KindSnipe AND
target.Kind == KindGenerator`, skip the candidate.

---

## 13. Sim tick loop changes (§11)

P1 §11 step list is extended:

```
0.  preflight (P1 §11.0)
1.  serverTick++
2.  build input map
3.  apply input to live players (P1 §11.3)
4.  move players (P1 §11.4)
4.5 step snipe AI (Phase 3 §10) — iterates ascending EntityID
4.6 step generator emission (Phase 3 §11) — iterates ascending EntityID
5.  resolve projectile motion + collision (P1 §11.5)
6.  decrement projectile lifetimes (P1 §11.6)
7.  player firing (P1 §11.7)
8.  respawn timers (P1 §11.8)
9.  GC (P1 §11.9) — now also removes AIStateDead snipes
10. return events
```

**Ordering rationale.** AI runs before projectile motion so a snipe
that fires this tick gets its projectile inserted at the same
"after step 7" position as a player's — i.e. by adding the snipe
projectile to the projectile pool from §10's stepSnipeAI before
§11.5 runs, the snipe's first-tick-of-flight is deferred to the next
tick (matching P1 §10.1's spawn-then-motion rule).

§11 step 9 GC: snipes with `Flags & FlagDead != 0` OR `aiState ==
AIStateDead` are removed.

---

## 14. Match-actor changes (`internal/match`)

The match actor's `evaluateMatchEnd` (`internal/match/match.go`) is
extended to add the §9.3 Phase 3 rule **at the top** of the priority
order:

```go
// Phase 3 addition: PVE_COMPLETE wins before LAST_STANDING when both
// fire on the same tick (§3.8.1).
if pveComplete(sim) && livePlayers >= 1 {
    return proto.EndPVEComplete, winnerOr0, true
}
// then existing P2 cases:
if live == 1 && startCount >= 2 { return EndLastStanding, ... }
if live == 0 { return EndAllEliminated, ... }
```

`pveComplete` is true iff **every** generator has been destroyed
(i.e. zero `KindGenerator` entities in `Entities()`) AND every snipe
has been destroyed (zero `KindSnipe` entities).

The "winner" for `PVE_COMPLETE` (§3.8) is **all surviving players**;
since Phase 5 owns the scoreboard, Phase 3's match actor selects
`winnerOr0 = 0` (no single winner) and sets the MatchOver entry list
to the ID-sorted players with `LivesRemaining = 1` for living and
`0` for dead. Score remains `0` (Phase 5).

Match config: `internal/match/match.go`'s `MatchConfig` gains
`LevelLetter byte` and `LevelNumber int`, passed straight through to
`sim.Config`. The lobby's `startMatch` plumbs them from the existing
`Level` field in `createRoom`. Phase 2's PvP-only mode passes
`LevelLetter = 0, LevelNumber = 0` (Phase 1 defaults), preserving the
PvP-only behaviour.

---

## 15. Determinism rules (additions)

Phase 1's rules in §12 remain. Phase 3 adds:

- The AI pass iterates snipes in **ascending EntityID** order; the
  generator emission pass iterates generators in **ascending EntityID**
  order. (Phase 1 already requires this for any entity-affecting loop.)
- BFS neighbour iteration order is fixed `[N, E, S, W]` matching
  P1 §7.3.
- LOS raycast uses integer-only Bresenham — no floats.
- All AI random draws (patrol direction, patrol-timer duration,
  spawn-tile slot ordering when ties are possible) consume from the
  **per-entity PRNG** so adding/removing other snipes never shifts
  any given snipe's draws.

### 15.1 Fingerprint additions (§12.1)

`Sim.Fingerprint()` (P1 §12.1) is extended with **new fields** in
the per-entity section (item 6) for snipes and generators:

For each `KindSnipe` entity, after the `lastInputTick` field group
(which is zero for non-players), emit:

```
u8 aiState
u16 aiTimer
u32 lastBFSTick
u8 pathLen (max 3)
[u8 tileX, u8 tileY] × pathLen   // bfsPath head
u32 chaseTarget
u16 losLostTicks
u16 fireCooldown
u32 parentGen
```

For each `KindGenerator` entity, append:

```
u16 emitCooldown
u8 rotation
```

These fields are emitted **only for the appropriate kind**; the
existing P1 fingerprint convention of zero-padding for inapplicable
fields is preserved by skipping the snipe block for non-snipe
entities, and the generator block for non-generator entities.

**Compatibility.** Phase 3's fingerprint format differs from P1's by
the bytes added above. The Phase 1 golden replay
(`testdata/replays/baseline.{inputs,hash}`) is regenerated as part of
this phase; the hash file will change. CI's cross-arch matrix is the
ground truth for the new bytes.

---

## 16. Test plan

All tests in `internal/sim/*_test.go`. Test names below are exact;
CI uses `go test -race -count=1`.

### 16.1 Level table (`levels_test.go`)

- `TestLevelParams_AtoF`: assert `LookupLevel('A', 1)`,
  `LookupLevel('F', 5)` etc. return the §6.1 row for the Easy
  bucket.
- `TestLevelParams_GtoM`, `_NtoS`, `_TtoZ`: same for the other
  buckets.
- `TestLevelNumberScaling_<n>` for `n ∈ {1, 2, 3, 4, 5, 6, 7, 8, 9}`:
  `LookupLevel('A', n).MaxSnipesTotal == n*6` etc.
- `TestNewSimRejectsInvalidLevel`: `LevelLetter='A', LevelNumber=0`
  → `ErrInvalidLevel`; `LevelLetter=0, LevelNumber=5` →
  `ErrInvalidLevel`; `LevelLetter='`', LevelNumber=10` →
  `ErrInvalidLevel`; `LevelLetter='@'` → `ErrInvalidLevel`.

### 16.2 BFS (`bfs_test.go`)

- `TestBFS_StraightLine`: open-floor 30×30 maze, source `(1, 1)`,
  target `(20, 1)`. Expected first step = `E`.
- `TestBFS_AroundWall`: hand-built fixture with a horizontal wall
  between source and target. First step heads `N` (or `S`,
  whichever exists) along the §7.3 N-E-S-W tie-break.
- `TestBFS_NoPath`: source surrounded by walls. Returns
  `(0, 0, false)`.
- `TestBFS_DepthCap`: target beyond `maxDepth`. Returns `false`.
- `TestBFS_Determinism`: 10 random mazes, each BFS called twice;
  byte-identical first-step returns.

### 16.3 LOS (`los_test.go`)

- `TestLOS_ClearOpenRoom`: source `(5, 5)`, target `(10, 5)`, all-
  floor. Returns true.
- `TestLOS_BlockedByWall`: source `(5, 5)`, wall at `(7, 5)`,
  target `(10, 5)`. Returns false.
- `TestLOS_RespectsRange`: maxRange = 4, distance = 5. Returns
  false regardless of intervening tiles.
- `TestLOS_DiagonalCornerBlock`: L-shaped wall corner between
  source and target on a diagonal. Returns false (supercover
  variant).
- `TestLOS_DiagonalOpen`: open diagonal between source and target.
  Returns true.
- `TestLOS_Determinism`: 100 random fixture configs; LOS results
  invariant across two calls.

### 16.4 Snipe state machine (`ai_test.go`)

- `TestSnipeIdleToPatrolAfter15Ticks`: spawn a snipe via direct
  fixture; tick 14 times — `AIState == AIStateIdle`,
  `FlagSpawnInvuln` set. Tick once more — `AIState == AIStatePatrol`,
  `FlagSpawnInvuln` cleared.
- `TestSnipePatrolPicksValidDirection`: snipe in open room; after
  N ticks the snipe has moved by `aiTimer` ticks worth of velocity.
  Verifies `aiTimer` resets when a wall is hit.
- `TestSnipePatrolToChase`: snipe at `(10, 10)`, player at
  `(12, 10)`, LOSRadius=6. Single tick after invuln expires →
  `AIState == AIStateChase`, `chaseTarget == player.ID`.
- `TestSnipeChaseToPatrolOnLOSLost`: enter chase, then move the
  player around a corner; after 60 ticks without LOS, snipe returns
  to `AIStatePatrol`.
- `TestSnipeAttackFiresProjectile`: snipe in chase with clear LOS
  → next tick `AIStateAttack` → projectile spawned with
  `shooter=snipe.ID`. Check `entity_spawn{actor: snipe.ID,
  target: proj.ID}`.
- `TestSnipeLeadFactor`: with `LevelLetter='N'` (full lead), a
  player moving E at speed 16 is led; snipe firing W of player aims
  ahead of the player's current position. With `'A'` (no lead),
  snipe aims at current position.
- `TestSnipeSpawnInvuln`: fire projectile at a freshly-spawned
  snipe; the projectile passes through (no hit) until tick 15+.

### 16.5 Generator emission (`generator_emit_test.go`)

- `TestGeneratorEmitFirstSnipeWithin8s`: single generator, 240
  ticks; assert ≥ 1 snipe spawned and emitted via the expected
  `entity_spawn` event.
- `TestGeneratorEmitsAtRate`: single generator, 600 ticks (~20s);
  assert 2–5 snipes spawned (rate 4–8s per snipe).
- `TestGeneratorRespectsGlobalCap`: level 9 (`MaxSnipesTotal=54`);
  run 11 generators for 1200 ticks (~40s); assert live snipe count
  never exceeds 54 (the §3.5 cap suppression rule).
- `TestGeneratorDestructionStopsEmissions`: 1 generator;
  destroy via 3 projectile hits; tick another 600 ticks; assert no
  new snipe spawns.
- `TestGeneratorSlotSearchOrder`: generator with a hand-built
  fixture where only one of its 8 neighbours is `TileFloor` and
  un-occupied. Assert the snipe spawns on that tile regardless of
  the per-generator rotation.
- `TestGeneratorEmissionDeferredCooldownNotReset`: place a
  generator in a sealed pocket (no qualifying neighbours); tick 600
  ticks; assert no snipes spawn; after clearing one neighbour by
  killing another snipe, the next emission fires within ≤ 1 tick.

### 16.6 Match-end evaluation (`internal/match/match_test.go`)

- `TestMatchEndsPVEComplete`: configure a fixture sim with 1
  generator + 1 snipe; the match actor sees both destroyed via
  injected events; emits `match_end{reason: PVE_COMPLETE}` followed
  by `MatchOver{reason: 0}`. The `winner` is `0` (Phase 3 doesn't
  resolve ties — Phase 5).
- `TestPVECompleteWinsTieWithLastStanding`: 2-player match,
  player 2 dies the same tick the last generator/snipe is destroyed.
  `reason = PVE_COMPLETE` (priority over `LAST_STANDING`).

### 16.7 Determinism & replay (`sim_test.go`)

- `TestDeterminism_SnipeAI`: two sims with the same `Config`
  including `LevelLetter='C', LevelNumber=5`; 600 ticks of scripted
  player inputs. Assert per-tick `Fingerprint()` equality.
- `TestDeterminism_GoldenFingerprint_Level9`: load
  `testdata/replays/phase3_pve.{inputs,hash}` (1-player PvE,
  level T9 — Brutal + max scaling — destroying 3 generators and 10
  snipes); run the sim; assert final `Fingerprint()` matches the
  hex committed in the .hash file.

### 16.8 Snipe-vs-snipe weak block (`ai_test.go`)

- `TestSnipeWeakBlockHigherIDBlocked`: two snipes moving toward
  the same tile; the lower-ID snipe occupies it, the higher-ID
  snipe is clamped. Snipes do not penetrate each other.

### 16.9 Friendly-fire matrix (`combat_test.go`)

- `TestSnipeProjectileDoesntHitOtherSnipes`: snipe A fires E,
  snipe B is on the projectile's path; the projectile passes through
  (and eventually hits a wall or expires). No `entity_hit` event.
- `TestSnipeProjectileDoesntDamageGenerators`: snipe A fires at a
  generator's tile; no `entity_hit` on the generator.
- `TestPlayerProjectileKillsSnipe`: standard kill path. After the
  hit, snipe is GC'd same tick (no `Entities()` entry next tick).

### 16.10 Property tests (`property_test.go`)

- `TestPropertyMaxSnipesTotalNeverExceeded`: 30 random levels × 30
  random `(Seed, W, H, players)`; tick 1200 times with random
  inputs; at every tick `liveSnipes ≤ LevelParams.MaxSnipesTotal`.
- `TestPropertyNoEntityInsideWall_WithSnipes`: same as P1's
  property test, but for sims with snipes alive. Snipes must
  respect P1 §9.3 swept AABB rules.

### 16.11 Benchmark (`bench_test.go`)

- `BenchmarkSimTick_60x40_8P_Level9`: 8 players + level T9 (11
  generators + 54-cap snipes + up to 64 projectiles); pre-warmed to
  steady state by ticking 600 ticks before `b.ResetTimer()`. Report
  `ns/op` and `allocs/op`. Target ≤ 3 ms/op (linux/amd64,
  ubuntu-latest, no `-race`); ≤ 12 ms/op under `-race`.

---

## 17. Testdata

```
testdata/
├── mazes/                            # P1 hashes (unchanged)
└── replays/
    ├── baseline.{inputs,hash}        # P1 baseline (REGENERATED — the
    │                                 # fingerprint includes new snipe/
    │                                 # generator fields per §15.1; the
    │                                 # input set itself does not change)
    └── phase3_pve.{inputs,hash}      # NEW — single-player PvE level T9
```

`baseline.inputs` is unchanged byte-for-byte; only `baseline.hash` is
regenerated. The `-update` flag on `TestDeterminism_GoldenFingerprint`
regenerates the hash file. CI is the cross-arch authority.

---

## 18. Risks

- **AI determinism under `-race`.** The AI pass adds per-tick BFS and
  LOS computation per snipe. Race detector iteration order *should*
  not affect a single-threaded sim, but slice-of-pointer patterns
  could in principle. Mitigation per P1 §15: never store `*Entity`
  in per-tick locals; index by slot.
- **BFS recompute storms.** At 54 snipes recomputing every 15 ticks,
  the worst-case is ~4 BFS/tick on average but ~14 if many snipes
  share the same `(ID + tick) % 15`. The §8.2 stagger keys off
  `(snipeID + serverTick) % 15`; in pathological seeds the
  distribution is uneven. Mitigation: the benchmark `_Level9`
  measures the actual cost; CI rejects regressions > 25 % vs.
  committed baseline.
- **Generator emission cap interplay with §7.0.1 geometric cap.**
  Level 9 wants 11 generators; the geometric cap on small maps
  caps below that. The §6.3 clamp is deterministic and tested. A
  match starting on a 30×20 map at level 9 will have fewer
  generators than 11 — operators should expect this and the test
  suite documents the actual count per (W, H, level).
- **Snipe-vs-snipe weak block is order-sensitive.** Resolving
  collisions in EntityID order rather than spatial proximity can
  produce visible "trains" of snipes; the SPEC says "cheap
  separation steering" so this is acceptable for v1.
- **PVE_COMPLETE evaluation order.** Phase 3 places it *first* in
  the priority list. If a future phase adds another high-priority
  rule, the implementation must preserve §3.8.1's stated order.
- **Fingerprint format change requires Phase 1 baseline regen.**
  Any Phase 3 implementer who forgets to regenerate
  `testdata/replays/baseline.hash` will see CI fail. The
  `-update` flag on the relevant test regenerates both the
  P1 baseline and the new `phase3_pve` fixture.

---

## 19. Open questions

These are flagged for codex review and for the author to decide
before implementation begins. Defaults are listed if no decision is
forced.

1. **Snipe-vs-player AABB on chase.** When a snipe in `CHASE`
   centres on the same tile as the player, does it stop? §3.3 says
   snipes don't block players (only generators do). Default:
   snipe simply passes through the player's tile and continues to
   chase; the player can be hit by the projectile, but body contact
   is a no-op.
2. **BFS path depth cap value.** §8.1 uses `maxDepth = LOS_RADIUS +
   4`. Should it be `LOS_RADIUS * 2`? Default: `LOS_RADIUS + 4` —
   the snipe only needs to reach LOS-range tiles, plus a small
   buffer for nearby corner cases. Larger caps cost CPU.
3. **Spawn-invuln duration scaling.** §3.6 hard-codes 0.5 s
   (= 15 ticks). Could it be level-dependent? Default: no, keep
   fixed.
4. **Snipe respawn after death.** §3.5 doesn't mention it. Default:
   snipes do **not** respawn. They are spawned only by generators,
   which themselves do not respawn (§3.6). PvE matches end when both
   generators and snipes are gone.
5. **`Config.LevelLetter == 0` semantics.** Phase 1/2 callers use
   the zero default for "no level table — Phase 1 defaults". Should
   `LevelLetter == 0` also be a valid input meaning "PvP mode"?
   Default: yes — the zero value preserves Phase 2 PvP behaviour.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./internal/sim/...` green on 4 CI runners | CI |
| 2 | Coverage ≥ 80 % for `internal/sim` | CI |
| 3 | `go test -race -count=1 ./internal/match/...` green | CI |
| 4 | `testdata/replays/phase3_pve.{inputs,hash}` reproduces byte-for-byte | §16.7 |
| 5 | `BenchmarkSimTick_60x40_8P_Level9` ≤ 3 ms/op | CI bench gate |
| 6 | Snipe AI deterministic across replays | `TestDeterminism_SnipeAI` |
| 7 | Every level bucket + every level number tested | §16.1 |
| 8 | `PVE_COMPLETE` reaches `MatchOver` from a scripted PvE | §16.6 |
| 9 | `schemaChecksum` unchanged from Phase 2 | `TestSchemaChecksumValue` |
| 10 | Phase 1 `TestDeterminism_GoldenFingerprint` passes against the **regenerated** baseline hash | §17 |

Items 1–10 are gate-able in CI.

The wire protocol is unchanged, so the existing Phase 2 vitest mirror
under `web/tests/proto.test.ts` continues to pass without
modification.
