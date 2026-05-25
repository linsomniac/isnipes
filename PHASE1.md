# Phase 1 — Deterministic Simulation Core

This document is the buildable, testable expansion of §8 Phase 1 of
[`SPEC.md`](./SPEC.md). It assumes Phase 0 (module scaffolding, CI,
`make build`, `/healthz`) is complete and produces only the
`internal/sim` package — no I/O, no transport, no AI, no lobby.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§3.3" point into `SPEC.md`.

---

## 1. Scope and definition of done

**Scope.** Produce a single Go package, `internal/sim`, that:

1. Generates a maze deterministically from `(seed, width, height)`.
2. Hosts a fixed-step integer-math tick loop for player entities,
   generator entities, and projectile entities.
3. Accepts 8-direction movement input + turbo + 8-direction fire input
   from one or more players, applies authoritative physics, and resolves
   projectile collisions with walls and other entities.
4. Handles entity damage, death, and a simple respawn timer for players.
5. Exposes a small public API that the future `internal/match` package
   will drive in Phase 2.

**Definition of done.** All of the following pass on `main`:

- `go test -race ./internal/sim/...` is green.
- Coverage ≥ 80 % statements for `internal/sim` (per §9 of `SPEC.md`).
- Eight committed golden maze hashes in `testdata/mazes/<seed>.hash`
  reproduce byte-for-byte on the four SPEC §12-mandated native CI
  runners: linux/amd64, linux/arm64, darwin/arm64, windows/amd64
  (not just cross-compiled binaries).
- One committed replay fixture (`testdata/replays/baseline.{inputs,hash}`)
  reproduces byte-for-byte on the same matrix.
- `go test -bench=BenchmarkSimTick_60x40_8P -benchtime=2s` reports
  ≤ 1 ms/op on a reference machine (GitHub Actions `ubuntu-latest`,
  4 vCPU x86_64) **and** ≤ 5 ms/op under `-race`.

---

## 2. Out of scope

Explicitly **not** built in Phase 1, even though the wire schema in
§4.3.2 of `SPEC.md` mentions them:

- Snipe entities, AI state machine, BFS pathfinding (Phase 3).
- Generator snipe-emission timer (Phase 3) — generators exist as
  static, destructible entities but emit nothing.
- The full A–Z × 1–9 level parameter table (Phase 3). Phase 1 uses a
  fixed default parameter set documented in §7.0 below.
- Networking, framing, snapshot encoding, AOI filtering (Phase 2/5).
- Client prediction, reconciliation, lag compensation (Phase 4).
- Lives counter, scoring, match-end conditions, dead-cam, spawn
  invulnerability (Phase 5).
- Chat, lobby, room server (Phase 2/6).

Phase 1 is a headless, in-process library. There is no concept of a
"match end" — tests drive `Sim.Tick` for a fixed number of ticks and
assert on the resulting state.

---

## 3. Prerequisites and assumptions

- Go 1.22 or newer (required for `math/rand/v2`).
- Module path `github.com/<org>/isnipes` (placeholder; settled in
  Phase 0).
- Repo layout per §4.2 of `SPEC.md`. Phase 1 only writes to
  `internal/sim/*.go`, `testdata/mazes/`, `testdata/replays/`.
- No external runtime dependencies beyond the standard library. The
  goal of `internal/sim` is a pure, deterministic library; importing
  third-party packages requires explicit justification.

---

## 4. Package and file layout

```
internal/sim/
├── sim.go              # Sim struct, NewSim, Tick, public surface
├── config.go           # Config struct, level defaults (§7.0)
├── maze.go             # generation: growing-tree, rooms, doorways, placement
├── maze_pack.go        # 2-bit tile packing per §4.3.4
├── entity.go           # Entity struct, Kind enum, slab allocator, ID issuance
├── physics.go          # subtile math, swept-AABB, sliding, diag normalization
├── combat.go           # projectile motion, hit detection, damage, death
├── respawn.go          # respawn timer + spawn-tile selection
├── prng.go             # PCG seeding, per-entity streams
├── fingerprint.go      # deterministic state hash for replay tests
├── doc.go              # package doc comment
│
├── maze_test.go
├── maze_pack_test.go
├── physics_test.go
├── combat_test.go
├── respawn_test.go
├── sim_test.go
├── bench_test.go
└── export_test.go      # in-package _test.go that exports
                        # `ForceExhaustedForTest` and other test-
                        # only knobs to sibling _test.go files
                        # without exposing them in the production
                        # build (Go's standard "export_test.go"
                        # convention; see §13.6).
```

No file other than the listed `*_test.go` files imports `testing`.
No file in `internal/sim` imports `net`, `net/http`, `encoding/json`,
`encoding/binary` (the packed-tile encoder uses raw byte slicing).

---

## 5. Public API

These are the only exported symbols in `internal/sim` after Phase 1.
Phase 2 and later may add to this surface but should not break it.

```go
// Tile codes match the wire encoding in §4.3.4 of SPEC.md.
type Tile uint8
const (
    TileWall            Tile = 0
    TileFloor           Tile = 1
    TileSpawnPlayer     Tile = 2
    TileSpawnGenerator  Tile = 3
)

// EntityKind matches the wire enum in §4.3.2.
type EntityKind uint8
const (
    KindPlayer     EntityKind = 1
    KindGenerator  EntityKind = 2
    KindProjectile EntityKind = 3
    // KindSnipe = 4 reserved for Phase 3.
)

// Direction codes match the Dir8 enum in §4.3.2.
type Dir uint8
const (
    DirIdle Dir = 0
    DirN    Dir = 1
    DirNE   Dir = 2
    DirE    Dir = 3
    DirSE   Dir = 4
    DirS    Dir = 5
    DirSW   Dir = 6
    DirW    Dir = 7
    DirNW   Dir = 8
)

// EntityID 0 is reserved as a "no entity" sentinel (§3.2).
type EntityID uint32

// Entity.Flags bits — match the wire Entity flags in §4.3.2.
const (
    FlagDead        uint8 = 1 << 0
    FlagSpawnInvuln uint8 = 1 << 1 // unused in Phase 1; reserved for Phase 5
    FlagTurbo       uint8 = 1 << 2
)

// Entity is the canonical sim record. Field layout matches the wire
// Entity in §4.3.2 but is not the wire encoding itself.
type Entity struct {
    ID     EntityID
    Kind   EntityKind
    HP     uint8
    Facing Dir
    Flags  uint8     // see Flag* constants above
    X, Y   int32     // subtile coordinates (§3.1)
    VX, VY int16     // last applied velocity in subtile units/tick
}

// PlayerInput is one tick of intent for one player. ClientTick is
// stored verbatim so Phase 2/4 can use it; Phase 1 ignores it for
// sim purposes but echoes it back in Sim.LastInputTick(playerID).
type PlayerInput struct {
    PlayerID   EntityID
    Dir        Dir
    Turbo      bool
    FireDir    Dir
    ClientTick uint16
}

// Event mirrors §4.3.2 Event{kind,actor,target,reason}. Phase 1
// emits only the EventEntity* and EventGeneratorDestroyed kinds;
// later phases will emit the rest.
type Event struct {
    Kind   uint8
    Actor  EntityID
    Target EntityID
    Reason uint8
}

// Event.Kind values — match the wire enum in §4.3.2. Phase 1 emits
// only the first four; later constants are reserved here so that
// internal/match (Phase 2) can name them without redefining.
const (
    EventEntitySpawn        uint8 = 0x01
    EventEntityHit          uint8 = 0x02
    EventEntityKill         uint8 = 0x03
    EventGeneratorDestroyed uint8 = 0x04
    // Reserved for Phase 2+:
    EventPlayerJoin    uint8 = 0x05
    EventPlayerLeave   uint8 = 0x06
    EventPlayerDC      uint8 = 0x07
    EventPlayerRejoin  uint8 = 0x08
    EventMatchStarting uint8 = 0x09
    EventMatchStarted  uint8 = 0x0A
    EventMatchEnd      uint8 = 0x0B
    EventChatRelay     uint8 = 0x0C
    EventRespawnPending uint8 = 0x0D
)

type Config struct {
    Seed       uint32 // matches SPEC §4.3.4 MapInit.seed wire width
    Width      int    // tiles, 30..120
    Height     int    // tiles, 20..80
    PlayerIDs  []EntityID  // pre-allocated player IDs; len() is player count
    // NoRespawn disables player respawn. When false (default),
    // players whose HP reaches 0 are DEAD-flagged for 90 ticks
    // and then respawned in place per §10.5. When true, players
    // are removed from the entity store by the **same tick's**
    // §11 step 9 GC pass (the entity_kill event still fires before
    // the GC, so callers see the kill in the event stream but not
    // the corpse in Entities() afterwards). They do not return for
    // the lifetime of this Sim. Phase 2 sets this to true to match
    // its gate (killed players absent from subsequent snapshots,
    // no lives system until Phase 5).
    NoRespawn bool
    // NoGenerators suppresses generator placement. When false
    // (default), §7.6 places generators per the §7.0.1 formula.
    // When true, no generators are placed: SPAWN_GENERATOR tiles
    // are not emitted, no KindGenerator entities are allocated,
    // and the player-vs-generator collision path in §9.4 is a
    // no-op. Phase 2 sets this to true to match its gate (PvP-
    // only match; snipes and generators defer to Phase 3).
    NoGenerators bool
    // Phase 3 will add LevelLetter, LevelNumber; Phase 1 uses
    // defaults for the speed/HP/timing parameters in §7.0.
}

type Sim struct{ /* unexported */ }

// NewSim runs maze generation, places entities at SPAWN_PLAYER and
// SPAWN_GENERATOR tiles, and returns a sim ready for Tick(). Returns
// an error if Config is invalid or if the generated maze fails the
// connectivity check after the maximum retry budget (§7.8).
func NewSim(cfg Config) (*Sim, error)

// Tick advances the simulation one 33.3 ms step. inputs may be
// empty; missing players default to "idle, no fire, no turbo".
// Returns events emitted this tick, in deterministic order.
//
// Tick returns a non-nil error only for unrecoverable conditions —
// in Phase 1 the only case is EntityID-space exhaustion (§8). When
// Tick returns a non-nil error the sim is quiesced: subsequent
// calls return (nil, ErrIDExhausted) immediately without advancing
// serverTick or processing inputs. The caller MUST stop ticking and
// tear down the match. Use errors.Is(err, ErrIDExhausted) to detect.
func (s *Sim) Tick(inputs []PlayerInput) ([]Event, error)

// ErrIDExhausted indicates the sim attempted to allocate an
// EntityID that would exceed math.MaxUint32. With the §8 headroom
// validation in NewSim, this is unreachable inside the documented
// playtime envelope; it exists so that internal/match (Phase 2) can
// recognise the condition without parsing event payloads.
var ErrIDExhausted = errors.New("isnipes/sim: EntityID space exhausted")

// Config validation errors from NewSim (§8). All satisfy
// errors.Is for the listed sentinels so callers can branch on them
// without parsing error strings.
var (
    ErrNoPlayers          = errors.New("isnipes/sim: Config.PlayerIDs is empty")
    ErrTooManyPlayers     = errors.New("isnipes/sim: Config.PlayerIDs exceeds Phase 1 cap (8)")
    ErrZeroPlayerID       = errors.New("isnipes/sim: Config.PlayerIDs contains 0 (reserved sentinel)")
    ErrDuplicatePlayerID  = errors.New("isnipes/sim: Config.PlayerIDs contains duplicates")
    ErrPlayerIDNoHeadroom = errors.New("isnipes/sim: Config.PlayerIDs exceeds the §8 headroom bound")
    ErrInvalidMapSize     = errors.New("isnipes/sim: Config.Width/Height outside [30..120] × [20..80]")
)

// Tile reads a maze tile. Out-of-bounds reads return TileWall.
func (s *Sim) Tile(tx, ty int) Tile
func (s *Sim) Width() int
func (s *Sim) Height() int

// Entities returns a snapshot copy of every entity currently in the
// store, in ascending ID order. This includes players in the DEAD
// interval between death and respawn (see §10.4 / §10.5) — callers
// who want only live entities should filter on `Flags & FlagDead`.
// Garbage-collected entities (e.g. destroyed generators, expired
// projectiles) are not returned.
func (s *Sim) Entities() []Entity

// LastInputTick returns the most recent ClientTick consumed for
// playerID, or 0 if none yet. Used by Phase 2's Snapshot encoder.
func (s *Sim) LastInputTick(playerID EntityID) uint16

// MapBytes returns a fresh copy of the §4.3.4 packed-tile byte
// slice. Callers may mutate the returned slice without affecting
// the sim. The map is small (≤ 2400 bytes for the max 120×80 maze)
// so the per-call allocation is negligible.
func (s *Sim) MapBytes() []byte

// Fingerprint returns a 32-byte SHA-256 hash of the complete sim
// state — every field that can affect future ticks — see §12.1 for
// the full enumeration. Two sims with equal Fingerprint(), given
// equal inputs, are guaranteed to evolve identically; this is the
// replay/determinism oracle. SHA-256 is used because it lives in the
// Go standard library and stability is the only requirement.
func (s *Sim) Fingerprint() [32]byte

// ServerTick returns the current tick number (0 immediately after
// NewSim; incremented at the top of Tick).
func (s *Sim) ServerTick() uint32
```

Anything else (`entity_test_*`, `physicsInternal_*`, etc.) stays
unexported.

---

## 6. PRNG strategy

A single seed produces a single, totally-ordered stream of random
draws for the maze generation pass. After that, randomness is
**partitioned per entity** so that adding or removing an entity does
not shift the draws used by any other entity.

- **Maze PRNG**: a `*rand.PCG` constructed as
  `rand.NewPCG(uint64(cfg.Seed), 0xA17ECAFE)` and wrapped at the
  call site with `rand.New(pcg)`. `math/rand/v2.NewPCG(seed1,
  seed2)` stores `seed1` in the high half and `seed2` in the low
  half of the PCG state, so this call uses `cfg.Seed` (widened to
  `uint64`) as the **high** half and `0xA17ECAFE` as the pinned
  **low** half. Consumed by: growing-tree carving, room placement,
  doorway opening, generator placement, player-spawn placement.
  Consumed in that fixed order in `maze.go`. The PCG source is
  *not* retained past `NewSim` (per §12.1 it isn't part of the
  fingerprint).
- **Per-entity PRNG** (`entityRand(id)`): lazily constructed
  `*rand.PCG` via `rand.NewPCG(uint64(cfg.Seed), uint64(id))` —
  `cfg.Seed` is again the high half and the entity ID is the low
  half. Cached in `map[EntityID]*rand.PCG`. Call sites wrap with
  `rand.New(pcg)` for the duration of one draw. **Caching the `*rand.PCG` (not the
  `*rand.Rand` wrapper) is required** because `math/rand/v2.Rand`
  does not expose its source's state; only `*rand.PCG` implements
  `encoding.BinaryMarshaler`. §12.1 item 7 calls `pcg.MarshalBinary()`
  on every cached PCG to include its state in the fingerprint.
  Consumed by: respawn-tile selection (Phase 1), AI decisions
  (Phase 3).

PCG (`math/rand/v2`) is mandatory because the legacy `math/rand`
package is **not** specified to be stable across Go releases.

`math/rand/v2.PCG.Uint64` is deterministic and identical on all
GOARCH targets that Go supports; we additionally pin Go's minimum
version in `go.mod` so a toolchain update can't silently change PCG
output. The cross-arch determinism check is enforced by
`TestDeterminism_GoldenFingerprint` (§13.6).

No `time.Now`, no `crypto/rand`, no goroutines that could observe
allocation/scheduling order, and no map iteration where the iteration
order matters for sim state. (The per-entity PRNG map is *not* iterated
during a tick; it is only looked up by key.)

---

## 7. Maze generation

> **Superseded by [`MAZE_REVAMP.md`](MAZE_REVAMP.md) (2026-05).** `internal/sim/maze.go`
> no longer uses the growing-tree + rooms + doorways pipeline described in this
> section. It now carves a **wide-corridor braided maze** on a coarse cell grid
> (6-tile corridors, 1-tile walls), with cell-based generator/spawn placement and
> rescaled entity sizes/speeds/distances. See `MAZE_REVAMP.md §2` for the current
> generator; this section is retained for history.

### 7.0 Phase 1 defaults

Phase 1 hard-codes the parameter set that Phase 3 will pull from the
level table:

| Parameter | Phase 1 value |
|---|---|
| `default_width`, `default_height` | 60, 40 |
| Player count | from `Config.PlayerIDs` |
| Generator count | per §7.0.1 formula (interior excludes the player perimeter band) |
| Max in-flight projectiles per match | 64 (excess fires rejected silently — §5.3 of `SPEC.md` and §10.1) |
| Player HP | 1 |
| Generator HP | 3 |
| Player normal speed | 16 subtile/tick |
| Player turbo speed | 32 subtile/tick |
| Projectile speed | 32 subtile/tick |
| Projectile lifetime | 90 ticks |
| Fire cooldown | 6 ticks |
| Respawn timer | 90 ticks |
| Player AABB half-extent | 96 subtile units |
| Generator AABB half-extent | 112 subtile units |
| Projectile AABB half-extent | 24 subtile units |

These are intentionally chosen to be tweakable to a level table in
Phase 3 without breaking Phase 1 tests; Phase 1 tests should reference
the constants from `config.go`, not magic numbers.

#### 7.0.1 Why the generator-count formula is capped by area

**Short-circuit:** if `Config.NoGenerators == true`, the formula
returns `0`, §7.6 is skipped entirely, no `TileSpawnGenerator` tiles
are emitted, and no `KindGenerator` entities are allocated. The rest
of this subsection applies only in the default
`NoGenerators == false` mode.

The naïve formula `max(2, len(PlayerIDs)) + 1` requests up to 9
generators for an 8-player match, which is infeasible at the minimum
30 × 20 map size. Two things must hold for `NewSim` to succeed on
the minimum size with 8 players:

1. Enough room to *fit* the generators at Chebyshev `≥ 12`.
2. Generators must not pre-empt the player-spawn perimeter band,
   otherwise `len(PlayerIDs)` spawns cannot fit there.

The cap therefore uses an "interior core" excluding the perimeter
band defined in §7.7 (`perimeterBand = max(3, H / 8)`). The
generator-only interior margin is `genMargin = perimeterBand + 1`,
so for every valid `(W, H)`:

```
genInterior = max(0, W - 2*genMargin) * max(0, H - 2*genMargin)
generatorCap = max(2, genInterior / 144)
generatorCount = max(2, min(max(2, len(PlayerIDs)) + 1, generatorCap))
```

Worked examples:

| Map | `perimeterBand` | `genMargin` | `genInterior` | `generatorCap` | Default formula | Actual |
|---|---:|---:|---:|---:|---:|---:|
| 30 × 20 (min) | 3 | 4 | 22 × 12 = 264 | 2 | up to 9 | 2 |
| 60 × 40 (default) | 5 | 6 | 48 × 28 = 1344 | 9 | up to 9 | up to 9 |
| 120 × 80 (max) | 10 | 11 | 98 × 58 = 5684 | 39 | up to 9 | up to 9 |

The cap never reduces below `max(2, …)` — every map gets at least 2
generators. Combined with the perimeter-band exclusion in §7.6 step
1, `NewSim` succeeds for every valid `(W, H, playerCount)` triple
in the allowed range.

### 7.1 Tile grid and cell grid

The tile grid is `Width × Height` tiles. The *cell grid* is the subgrid
where corridors live; cells are at odd tile coordinates and walls between
cells are at even tile coordinates. With `cellCols = (Width - 1) / 2`
and `cellRows = (Height - 1) / 2`:

- Cell `(cx, cy)` corresponds to tile `(2*cx + 1, 2*cy + 1)`.
- The wall between cells `(cx, cy)` and `(cx+1, cy)` is the tile
  `(2*cx + 2, 2*cy + 1)`.
- Tiles at `tx == 0`, `tx == Width - 1`, `ty == 0`, or `ty == Height - 1`
  are always `TileWall` (outer boundary).

For the default 60 × 40 map: `cellCols = 29`, `cellRows = 19`.

### 7.2 Initial fill and start cell

All tiles begin as `TileWall`. The start cell is `(0, 0)` in cell
coordinates, i.e. tile `(1, 1)`. The start cell is set to `TileFloor`
and pushed onto the growing-tree frontier.

### 7.3 Growing-tree carving

Standard growing-tree maze gen with bias toward the newest frontier
entry. Per iteration:

1. If the frontier is empty, stop.
2. Draw `r = mazeRand.Float64()`.
3. If `r < 0.6`, set `idx = len(frontier) - 1` (newest); else
   `idx = mazeRand.IntN(len(frontier))`.
4. Let `c = frontier[idx]`. Inspect its four cell-neighbors. Filter
   to those that are still `TileWall` at their cell tile.
5. If the filter set is empty, remove `c` from the frontier
   (swap with last and pop, deterministic) and continue.
6. Otherwise pick one neighbor uniformly using `mazeRand.IntN`.
   Carve: set the neighbor cell's tile to `TileFloor`, and set the
   wall-between tile (one tile away on the connecting axis) to
   `TileFloor`. Push the neighbor onto the frontier.

Iteration order over neighbors in step (4) is fixed as `[N, E, S, W]`
to keep behavior identical across Go map iteration randomisation.

### 7.4 Room carving

After growing-tree, carve 5–10 axis-aligned rectangular rooms:

1. `roomCount = 5 + mazeRand.IntN(6)` (5..10 inclusive). This is
   the **target** count: room carving stops as soon as that many
   rooms are accepted, never producing more.
2. For up to `64 * roomCount` attempts, propose a room with
   `w, h ∈ [4, 8]` and origin `(rx, ry)` such that
   `rx ∈ [2, Width-2-w]` and `ry ∈ [2, Height-2-h]`. Reject if the
   room rectangle overlaps any previously accepted room rectangle
   (1-tile margin). On acceptance, set every tile inside the
   rectangle to `TileFloor` and add the rectangle to the accepted
   list. **Stop immediately** once
   `len(acceptedRooms) == roomCount` — do not consume any further
   attempts and do not call `mazeRand` for them, because rooms
   beyond `roomCount` would violate SPEC §3.1's 5–10 room
   requirement and shift later draws.
3. If fewer than 5 rooms were accepted after the attempt budget,
   the maze fails this attempt: re-run the whole generation with
   the next retry seed from §7.6's perturbation scheme (max 3
   retries total across §7.4 + §7.6 + §7.8). SPEC §3.1 requires
   5–10 rooms; a maze with fewer than 5 is not a valid Phase 1
   output. The `64 * roomCount` attempt budget is generous enough
   that this failure path is hit only on adversarial seeds against
   the smallest maps; the test suite calibrates a band that detects
   regressions before they hit retry.

### 7.5 Doorway insertion

After growing-tree + rooms, scan every internal even-coordinate tile
that currently separates two adjacent `TileFloor` cells, and with
probability `pDoor = 0.75` set it to `TileFloor`. The scan order is
row-major (`ty` outer, `tx` inner).

Rationale: growing-tree alone yields average cell degree ≈ 2 (perfect
spanning tree). Each extra doorway lifts the average degree by ≈ 2/N
where N is the cell count, since opening one wall adds two
half-edges. At `pDoor = 0.75`, the expected average cell degree
converges to ~3.4–3.6 across the supported map size range, hitting
SPEC §3.1's stated target of ≈ 3.5. `TestMazeAverageDegree` (§13.3)
asserts the band `[3.3, 3.7]` for the default 60 × 40 map and
`[3.1, 3.9]` for the minimum 30 × 20 (where small-sample variance is
larger), both centred on SPEC's 3.5.

### 7.6 Generator placement

Generator placement uses dart-throw Poisson-disk sampling. This
section is **skipped entirely** when `Config.NoGenerators == true`
(see §7.0.1 short-circuit); the rest applies only in the default
mode.

1. Build a candidate list of all `TileFloor` tiles with `tx ∈
   [genMargin, W - 1 - genMargin]` and `ty ∈ [genMargin, H - 1 -
   genMargin]`, where `genMargin = perimeterBand + 1` per §7.0.1
   and §7.7. Excluding the player perimeter band guarantees that
   generator placement does not compete with player-spawn placement.
2. Shuffle the candidate list in place using `mazeRand`
   (Fisher–Yates).
3. Walk the shuffled list, accepting each tile as a generator location
   iff **every** previously-accepted location is at Chebyshev distance
   `≥ 12` tiles from it (equivalently, **no** prior location is within
   `< 12` tiles). Stop when `generatorCount` (§7.0) accepted, or the
   list is exhausted.
4. If fewer than `generatorCount` accepted, retry the whole maze
   generation. On retry attempt `k = 1, 2, 3`, derive the retry
   seed as a `uint32`:
   `seed_k = cfg.Seed ^ uint32(0xDEADBEEF * uint64(k))`
   (the `uint64` term is computed first so the multiplication can't
   overflow `uint32` before XOR; the result is truncated back to
   `uint32` to match `Config.Seed`'s width). Pass `seed_k` to the
   maze PRNG construction (§6) for the retry. Return an error if
   all three retries fail.

Each accepted tile is set to `TileSpawnGenerator`.

### 7.7 Player spawn placement

Players spawn around the perimeter. Define the *perimeter band* as
tiles whose Chebyshev distance from the outer wall is `≤ max(3, H/8)`.

1. Build the candidate list: all `TileFloor` tiles inside the perimeter
   band.
2. Shuffle using `mazeRand`.
3. Walk the shuffled list, accepting a tile as a player spawn iff
   **every** previously-accepted spawn **and** every existing
   generator tile is at Chebyshev distance `≥ 15` tiles from it
   (equivalently, no prior location is within `< 15` tiles). Stop
   when `len(PlayerIDs) + 2` spawns accepted (extra spawns let respawn
   pick a fresh tile).
4. If fewer than `len(PlayerIDs)` spawns are accepted, relax the
   minimum distance to `≥ 10` and walk the shuffled list once more
   (re-using the same shuffle). If still insufficient, return an error.

Each accepted tile is set to `TileSpawnPlayer`.

### 7.8 Connectivity validation

After all placement, BFS from any `TileSpawnPlayer` over the four-
neighborhood, treating `TileFloor`, `TileSpawnPlayer`, and
`TileSpawnGenerator` as walkable. Every `TileFloor`, every other
`TileSpawnPlayer`, and every `TileSpawnGenerator` must be reachable
(matches the canonical Phase 1 gate in §8 of `SPEC.md`). If not,
retry the whole maze using the same `seed_k` perturbation scheme as
§7.6 (max 3 retries). With `pDoor = 0.75` this never fires in 10 000-
seed random sweeps; the retry is belt-and-braces.

### 7.9 Maze packing

`MapBytes()` returns the packed-tile byte slice using `packing = 1`
per §4.3.4 of `SPEC.md`. Packing layout: 2 bits per tile, little-
endian within each byte, row-major (`ty` outer, `tx` inner). The
returned slice has length `ceil(Width * Height * 2 / 8)` and is
**freshly allocated on each call** — callers may mutate it without
affecting the sim. Internally the sim keeps an immutable copy that
`Fingerprint()` (§5) hashes directly, avoiding the per-call alloc on
the determinism hot path.

---

## 8. Entity store

A slab-style fixed-capacity allocator backed by a slice.

- `entities []Entity` — owned by the sim, fixed capacity
  `maxEntities = 256`. Slots with `Entity.ID == 0` are free; an
  allocation linearly scans for the first free slot. Removal sets
  the slot's `ID` field to `0` (along with zeroing the other fields).
  Free-slot state is therefore a pure function of the entity-slice
  contents — no parallel `freeSlots` index is needed, and the
  fingerprint (§12.1) does not have to serialise one.
- `nextID EntityID` — monotonic, never reused. Initialised by
  `NewSim` to `max(Config.PlayerIDs) + 1` **after** validation, so
  generator and projectile IDs cannot collide with caller-supplied
  player IDs. ID `0` is the reserved sentinel per §3.2 of `SPEC.md`.
  Included in the fingerprint so that two sims agreeing now will
  also agree on every future entity ID.
- Lookup is by linear scan in Phase 1 (population ≤ 256); a hash
  index is a Phase 5 optimisation if profiling demands.

**Initial facing.** Every non-projectile entity created by `NewSim`
is initialised with `Facing = DirS` (south, value 5) so that
SPEC §4.3.2's `Entity.facing ∈ {1..8}` invariant holds from tick 0
onward — even before the first non-idle input arrives. Players keep
`DirS` until their first non-idle input updates `Facing` per §11
step 3; generators never change `Facing` in Phase 1. (Phase 3 will
give snipes their own initial facing in their own spec.)

**Player-to-spawn assignment.** Player spawn placement in §7.7
yields an *ordered* list `acceptedSpawns` (the order in which the
shuffled candidate scan accepted tiles). The kth `EntityID` in
`Config.PlayerIDs` is placed at `acceptedSpawns[k]`. Because both
lists are deterministic functions of the seed and the supplied
ID list, the initial positions are fully reproducible.

Players are allocated at `NewSim` time, in the order supplied by
`Config.PlayerIDs`. `NewSim` validates that:

1. `len(Config.PlayerIDs) >= 1` — Phase 1 requires at least one
   player. An empty slice is rejected with a typed error
   (`ErrNoPlayers`), since `max(empty) + 1` for `nextID` would be
   undefined.
2. `len(Config.PlayerIDs) <= 8` — Phase 1's tested envelope.
3. Every supplied ID is `> 0` (no use of the reserved sentinel).
4. Every supplied ID is unique within the slice.
5. `max(Config.PlayerIDs) ≤ math.MaxUint32 − headroom`, where
   `headroom = maxEntities * 4096` — a generous bound on post-
   `NewSim` allocations (each in-flight projectile, generator, and
   replacement allocation consumes one ID, and 4 KiB headroom per
   slot covers a multi-hour match without coming near
   `MaxUint32`).

Failing any check returns a typed error from `NewSim`. Once players
are allocated, `nextID` is bumped past the largest supplied ID
before any further allocation. Generators are allocated next, in
tile row-major order. Phase 1 never spawns new players post-`NewSim`
(late-join is a Phase 2 concern), so the only post-init allocations
are projectiles and player respawns (which reuse the original player
ID — see §10.5).

The allocator detects exhaustion **before** any wrap occurs: the
sentinel test in `allocID` is "if issuing this ID would force
`nextID` to cross `math.MaxUint32`, refuse". Concretely, the
implementation checks `nextID == math.MaxUint32` (the next
allocation would set `nextID = MaxUint32 + 1`, which is the cross
event); on hit it returns `ErrIDExhausted` without issuing the ID.
The `nextID` field therefore never wraps to `0` during normal
operation — and `0` (the reserved sentinel) is treated as an
already-exhausted state so a test helper, a corrupted save, or any
other irregular entry into that state is also caught.

Once `allocID` returns `ErrIDExhausted`, `Sim.Tick` aborts the
in-progress tick, flips the sim into a quiesced state, and returns
`(nil, ErrIDExhausted)`. All subsequent `Tick` calls also return
`(nil, ErrIDExhausted)` without advancing `serverTick` or processing
inputs. The caller is expected to tear down the match in response.
Phase 1 does not implement ID recycling.

---

## 9. Physics

### 9.1 Subtile coordinates

One tile = 256 subtile units (`subtilePerTile = 256`). An entity at
subtile position `(x, y)` occupies an AABB centered on that position
with half-extents `(halfExt, halfExt)` taken from §7.0. Tile-space
coordinates derive as `tx = x / 256`, `ty = y / 256` (integer
division, no rounding). The center of tile `(tx, ty)` is
`(tx*256 + 128, ty*256 + 128)`.

### 9.2 Direction vectors and diagonal normalisation

Speeds in §7.0 are *cardinal* magnitudes. For 8-way movement:

```
diag(s) = (s * 181) / 256   // integer division (truncating)
```

The constant `181 ≈ 256 / √2`. Hard-coded table for the three speeds
Phase 1 uses:

| `s` | cardinal | diagonal |
|---:|---:|---:|
| 16 | 16 | 11 |
| 32 | 32 | 22 |

Per-direction velocity at speed `s`:

| Dir | (vx, vy) |
|---:|---|
| N | (0, -s) |
| NE | (+diag(s), -diag(s)) |
| E | (+s, 0) |
| SE | (+diag(s), +diag(s)) |
| S | (0, +s) |
| SW | (-diag(s), +diag(s)) |
| W | (-s, 0) |
| NW | (-diag(s), -diag(s)) |
| Idle | (0, 0) |

Diagonal magnitude `√(11² + 11²) = 15.56` is within 1 subtile of 16;
`√(22² + 22²) = 31.11` is within 1 subtile of 32. Diagonal is always
strictly ≤ cardinal so players cannot game speed by holding diagonals.

### 9.3 Swept-AABB wall collision

For each entity with non-zero velocity this tick, apply axis-separated
sweep: X first, then Y. This is the standard "no corner clipping"
formulation for tile grids with AABBs strictly smaller than one tile.

```
moveAndSlide(pos, vel, halfExt):
    // X axis
    if vel.X > 0:
        leadX = pos.X + halfExt + vel.X
        col   = leadX / 256
        rowLo = (pos.Y - halfExt) / 256
        rowHi = (pos.Y + halfExt - 1) / 256
        for r in [rowLo .. rowHi]:
            if Tile(col, r) == WALL:
                pos.X = col*256 - halfExt - 1
                vel.X = 0
                goto Y
        pos.X += vel.X
    else if vel.X < 0:
        leadX = pos.X - halfExt + vel.X
        col   = leadX / 256
        rowLo = (pos.Y - halfExt) / 256
        rowHi = (pos.Y + halfExt - 1) / 256
        for r in [rowLo .. rowHi]:
            if Tile(col, r) == WALL:
                pos.X = (col+1)*256 + halfExt
                vel.X = 0
                goto Y
        pos.X += vel.X

    // Y axis (symmetric, using pos.X already updated)
Y:
    if vel.Y > 0:
        ... mirrored ...
    else if vel.Y < 0:
        ...
    return pos, vel
```

Notes:

- For negative-coordinate truncation correctness, the implementation
  must use Euclidean division (`x.div_euclid(256)`-style), not Go's
  default truncated division. With the outer boundary always
  `TileWall`, however, no entity can ever reach negative coordinates;
  the test suite asserts this invariant separately (§13.7) so the sim
  can use plain `/256`.
- Player AABB half-extent is 96, so AABB width 192. Corridor width is
  one tile = 256 subtile units. Players pass through with 32 subtile
  units of clearance per side. Generator AABBs (radius 112, width 224)
  still fit through a corridor — but generators are immovable so this
  only matters at maze gen time.
- Projectiles use the same swept algorithm with `halfExt = 24`. Wall
  hits emit `entity_kill{actor: 0, target: projectile, reason: 1}`
  (`actor = 0` for environmental kills per §4.3.2 of `SPEC.md` and
  §10.2) and despawn.

### 9.4 Entity-vs-entity collision

Phase 1 entity blocking rules (per §3.3 of `SPEC.md`):

- **Players vs walls:** swept AABB as above.
- **Players vs generators:** generators are immovable and block movement.
  Implemented by treating each live generator's footprint as a
  pseudo-tile during the player sweep. Specifically, after the wall
  sweep on each axis, additionally test the entity's new AABB against
  each generator's AABB; on overlap, clamp on the axis being swept
  and zero its velocity. Tested by `TestPlayerBlockedByGenerator`.
- **Players vs players:** **do not block** (§3.3). Player AABBs may
  overlap.
- **Snipes:** not in Phase 1.

Projectiles do not collide with each other; they hit walls, players,
and generators only.

---

## 10. Combat

### 10.1 Firing

A player fires when **all** of the following hold for that tick.
These conditions are evaluated in §11 step 7, *after* §11 step 3
has applied the §10.6 turbo lock and set `Flags & FlagTurbo`
accordingly — so we gate on the effective post-step-3 turbo state,
not the raw `Input.Turbo` byte:

- `Flags & FlagDead == 0` (DEAD players cannot fire — see §10.4);
- `Input.FireDir != DirIdle`;
- `Flags & FlagTurbo == 0` (the player is not effectively turboing
  this tick — §3.4 of `SPEC.md`. A player who holds turbo but
  releases their movement key reaches this gate with `FlagTurbo`
  cleared by §10.6's turbo-cancel-via-idle rule, and *can* fire.);
- `fireCooldown == 0`;
- the number of currently-live `KindProjectile` entities in the sim
  is `< 64` (the per-match cap from §5.3 of `SPEC.md`).

On fire:

- Set `fireCooldown = 6` ticks.
- Spawn a `KindProjectile` entity at the player's centre, with
  `Facing = FireDir`, velocity vector from §9.2 at speed 32, HP = 1
  (irrelevant — projectiles don't take damage), and `lifetime = 90`.
- Emit `Event{Kind: entity_spawn, Actor: shooterID, Target: projID, Reason: 0}`.

When any of the five conditions fails, the fire request is dropped
silently — no projectile, no event, `fireCooldown` unchanged. The
in-flight cap is checked **after** the cooldown gate, so a fire
rejected only because the cap is full does not reset the cooldown
(the player can immediately re-attempt next tick if a projectile
expires).

Turbo also forces direction to match the previous tick's direction
(§10.6 below).

### 10.2 Projectile motion and hit detection (unified)

Per §3.4 of `SPEC.md`, the *first* front-of-line obstruction along a
projectile's segment registers — whether it is a wall or an entity.
Phase 1 resolves wall and entity collisions in a single pass so that
the earliest impact wins regardless of which kind it is.

For each projectile that was **not** spawned this tick (newly-spawned
projectiles defer their first motion to next tick, per §10.1 and §11
step 7), in a single resolution pass:

1. Compute the proposed motion segment from the projectile's current
   position to current + velocity.
2. Test the segment against the tile grid using swept AABB (§9.3);
   record `t_wall ∈ [0, 1]` for the earliest wall intersection (`+∞`
   if none).
3. Test the segment against every live, **non-shooter** entity whose
   `Kind` is *not* `KindProjectile` (projectiles do not collide with
   each other — see §9.4) using the swept-AABB-vs-AABB slab method;
   record `t_entity ∈ [0, 1]` and the target entity for the earliest
   entity intersection (`+∞` if none). If multiple entities tie on
   `t_entity` to within sub-subtile precision, break the tie by
   ascending `EntityID`.
4. Decide:
   - `t_wall == +∞ && t_entity == +∞`: no collision. Advance the
     projectile to the proposed position.
   - `t_wall ≤ t_entity` (wall wins, with wall preferred on exact
     tie): clamp the projectile to the impact position. Emit
     `Event{Kind: entity_kill, Actor: 0, Target: projID, Reason: 1}`
     — `Actor` is `0` (environment killed the projectile) per
     §4.3.2 of `SPEC.md` (`0 if env`). Remove the projectile.
   - `t_entity < t_wall` (entity wins): clamp the projectile to the
     impact position. Decrement the target's HP by 1 and emit
     `Event{Kind: entity_hit, Actor: shooterID, Target: victimID, Reason: 0}`.
     If the target's HP is now 0, run the death pipeline (§10.4) and
     emit `Event{Kind: entity_kill, Actor: shooterID, Target: victimID, Reason: 0}`;
     additionally, if the target is a generator, emit
     `Event{Kind: generator_destroyed, Actor: shooterID, Target: victimID, Reason: 0}`
     immediately after the `entity_kill`. Remove the projectile.

No lag compensation in Phase 1 — that's a Phase 4 build. The sim
ticks against current entity positions only.

Friendly-fire is on (FFA mode). A projectile cannot hit its own
shooter; this is enforced by the "non-shooter" filter in step 3.
(Whether turbo-running into your own projectile should hit you is a
v1.1 question; Phase 1 keeps the simple rule.)

Lifetime expiry is handled in step 6 of the tick loop (§11), **after**
movement and hit detection but **before** firing, so that a projectile
spawned in step 7 of tick `T` does not lose a tick in step 6, and a
projectile already in the world for `n ≥ 1` ticks is honoured for its
full final motion before being expired. §10.2 itself does not
decrement `lifetime`.

### 10.3 Hit detection

Merged into §10.2. Previous drafts of this spec resolved wall hits in
a motion phase and entity hits in a later phase; that ordering let a
later-tick wall remove a projectile before its earlier entity
collision was tested. §10.2 now resolves both in one swept pass.

### 10.4 Damage and death

When an entity's HP reaches 0:

- Update `Flags`: set `FlagDead`, **clear** `FlagTurbo` (the entity
  is no longer turboing — leaving the bit set would expose
  `DEAD|TURBO` in snapshots during the dead interval, which is
  semantically nonsensical), and clear every other live-state bit
  in one write. Concretely: `Flags = FlagDead`.
- Stop applying movement to it (velocity zeroed).
- For `KindPlayer`: record the death position (`deathX`, `deathY`)
  in an unexported per-player field; reset `fireCooldown` to 0;
  reset `lastDir = DirIdle` so the dead interval does not retain a
  turbo lock.
- For `KindGenerator`: mark the entity DEAD; it is removed by the
  current tick's §11 step 9 GC pass (so a destroyed generator never
  appears in subsequent `Entities()` snapshots). Generators do not
  respawn.
- For `KindPlayer`, branch on `Config.NoRespawn`:
  - **`NoRespawn == false` (Phase 1 default):** enter the respawn
    pipeline (§10.5) by setting `respawnAt = serverTick + 90`. The
    DEAD entity remains in the entity slice for the full 90-tick
    interval; on tick `D + 90` it is rewritten in place by §10.5.
  - **`NoRespawn == true` (Phase 2 setting):** do **not** schedule
    a respawn. The entity is removed from the entity store by
    **this same tick's** §11 step 9 GC pass — so even the
    death-tick snapshot does not include B, matching the strict
    reading of SPEC §8 Phase 2's "B is absent from subsequent
    snapshots" rule (any snapshot at or after the kill is free of
    B). The `entity_kill` event is still emitted and ordered before
    the GC pass, so clients see the kill in the event stream but
    not the corpse. It never reappears.

### 10.5 Respawn (simple)

Phase 1 implements a no-lives respawn loop: every player respawns
indefinitely.

- On player death, schedule `respawnAt = serverTick + 90` (i.e. 3 s).
- On the respawn tick, pick a spawn tile by the following algorithm
  (matches SPEC §3.9: "respawn at the safest available SPAWN_PLAYER
  tile (max distance from nearest live entity hostile to player)"):
  1. Build the candidate list = every `TileSpawnPlayer` whose
     center, when used as the respawn position, would place the
     respawning player's AABB clear of every non-DEAD entity AABB.
  2. **If the candidate list is empty**, defer: set
     `respawnAt = serverTick + 1` and emit no events this tick. The
     deferral may iterate; in practice projectile motion or other
     player movement frees a slot within a handful of ticks. This
     is the test path covered by `TestRespawnDeferredWhenAllBlocked`
     (§13.5).
  3. Build the live-hostiles list: every entity with `Flags &
     FlagDead == 0` whose `Kind` is `KindPlayer` (other players —
     FFA mode treats every other player as hostile) or `KindGenerator`.
     Projectiles are excluded (they are short-lived and would make
     respawn selection chase noise; SPEC §3.4 lag-compensation
     handles in-flight shots separately). Snipes will join this
     list in Phase 3 with the same rule.
  4. For each candidate spawn `S`, compute its **safety score**:
     - If `hostiles` is non-empty:
       `safety(S) = min over h ∈ hostiles of Chebyshev(S, h.tilePos)`
       (the smallest tile distance from `S` to any hostile — i.e.
       the danger comes from the *nearest* threat).
     - Otherwise (no live hostile — solo play, or this is the last
       remaining player): `safety(S) = Chebyshev(S, deathPos)` using
       the respawner's saved death position (§10.4). This keeps
       behaviour deterministic when there is no other entity to
       measure against.
  5. Sort candidates by *descending* `safety(S)` (safer first). Ties
     broken by ascending `(ty * W + tx)` so the ordering is fully
     deterministic.
  6. Pick index `entityRand(playerID).IntN(min(3, len(candidates)))`
     from the top of the sorted list — one of the three safest, with
     a deterministic per-player draw. When `len(candidates) < 3`,
     the bound shrinks accordingly; the call is never invoked with a
     zero argument because step 2 short-circuits.
- Reset the player entity in place (same `EntityID`): clear
  `FlagDead` **and** `FlagTurbo` (and every other reserved flag
  bit) in one `Flags = 0` write; set HP back to 1; set position to
  the chosen tile's centre; zero velocity; set `Facing = DirS`;
  clear `fireCooldown`; reset `lastDir = DirIdle` so the strict
  turbo lock (§10.6) treats the respawning player as freshly
  engaging on their next turbo input rather than inheriting the
  pre-death locked direction. Do **not** reset `lastInputTick`;
  it remains at its pre-death value so Phase 2's snapshot encoder
  reports the correct `your_last_input_tick` for the respawning
  player on tick `T = D + 90` and afterward.
- Emit `Event{Kind: entity_spawn, Actor: 0, Target: playerID, Reason: 0}`.

`SPAWN_INVULN` (Phase 5) is **not** applied. There is no half-tick
state where the entity is both DEAD and alive: on the respawn tick
`T = D + 90`, the player record transitions from DEAD-at-deathPos to
alive-at-newPos atomically inside the §11 step 8 respawn pass. The
tick-`T` snapshot already reflects the alive state, and the emitted
`entity_spawn` event accompanies it. Clients observe the
death-then-respawn sequence across the two events emitted on
different ticks (`entity_kill` on tick `D`, `entity_spawn` on tick
`T = D + 90`).

### 10.6 Turbo lock-direction rule

Per §3.3 of `SPEC.md`, turbo locks the player into a single
direction "so you cannot instantly 180° at full speed". Phase 1
implements the **strict** SPEC rule — no turning while turbo is held:

- Track `lastDir` per player.
- If `Input.Turbo` is true and `Input.Dir != DirIdle`:
  - If `lastDir == DirIdle` (the player just engaged turbo this
    tick), record `lastDir = Input.Dir` and use `Input.Dir` for this
    tick's velocity.
  - Otherwise (turbo was already engaged on the previous tick),
    **ignore `Input.Dir` entirely** and use `lastDir` for this tick's
    velocity. The only way to change direction during a turbo run is
    to release turbo for at least one tick.
- If `Input.Turbo` is true and `Input.Dir == DirIdle`, treat as
  turbo cancel: set `lastDir = DirIdle`, zero velocity, clear bit 2
  (`TURBO`). (Players can disengage turbo mid-run by releasing the
  direction key even if they keep turbo held.)
- If `Input.Turbo` is false, clear `lastDir = DirIdle` and accept
  `Input.Dir` directly.
- Set `Flags` bit 2 (`TURBO`) to mirror the chosen-this-tick state.

`TestTurboStrictLock` (§13.4) covers the strict-rule edge cases.

---

## 11. Tick loop

The order inside `Sim.Tick(inputs)` is fixed and must not be reordered
(the determinism tests would catch it, but explicit is better):

0. **Quiesce / ID-headroom preflight.** Before any state changes:
   - If the sim is already quiesced (a previous tick or a test
     helper has set the quiesced flag), return `(nil,
     ErrIDExhausted)` immediately. No state change, no event list,
     no `serverTick` advance.
   - Compute `upperBoundNewAllocations` = the count of live players
     (`Flags & FlagDead == 0`) — each could spawn at most one
     projectile in step 7 this tick, no other allocation site
     exists in Phase 1. If `nextID > math.MaxUint32 -
     upperBoundNewAllocations` (i.e. some allocation this tick
     could cross the limit), set the quiesced flag and return
     `(nil, ErrIDExhausted)`. No state change.
   - Otherwise the tick is guaranteed to complete without an
     allocation failure; proceed with steps 1–10. This preflight
     model removes the need for transactional rollback inside
     steps 1–9, since allocation is no longer fallible past this
     point.
1. `serverTick++`.
2. Build a per-player input map from `inputs` (later inputs for the
   same `PlayerID` overwrite earlier; players not listed default to
   idle/no-fire/no-turbo).
3. For each player whose `Flags & FlagDead == 0`, look up the entry
   in the input map: if one exists, persist its `ClientTick` into
   the player's `lastInputTick` field (this is what `LastInputTick`
   (§5) returns to Phase 2's snapshot encoder), then update
   intended velocity from the input, apply the turbo lock (§10.6),
   and decrement `fireCooldown` (saturating at zero). **`Facing`
   updates only when `Input.Dir != DirIdle`** — on idle ticks
   `Facing` retains its previous non-idle value, so SPEC §4.3.2's
   invariant that `Entity.facing ∈ {1..8}` always holds. Players
   with no matching input default to idle / no-fire / no-turbo for
   this tick; their `lastInputTick` is **not** updated. DEAD players
   are skipped entirely — velocity stays at zero, `Facing` stays at
   its death-time value (still non-idle), and `lastInputTick` is
   not updated.
4. Move each player whose `Flags & FlagDead == 0` (swept AABB vs
   walls, then vs generator AABBs). DEAD players have velocity 0
   from §10.4 and are skipped here.
5. Resolve projectile motion **and** collision per §10.2: each
   existing projectile is swept against walls *and* entities in a
   single pass; the earliest impact wins; survivors advance to the
   proposed position. This replaces the old two-pass "move all
   projectiles, then test entity hits" ordering, which could let a
   wall remove a projectile before its earlier entity hit was scored.
6. Decrement projectile lifetimes (operating on the projectiles that
   existed before this tick — i.e. the same set that step 5 just
   moved); expire any that hit zero by emitting
   `Event{Kind: entity_kill, Actor: 0, Target: projID, Reason: 2}`
   (`Actor: 0` because timeout is an environmental kill, per
   §4.3.2 of `SPEC.md`) and removing the projectile from the entity
   store. This is the **only** site that decrements `lifetime`.
   Firing happens *after* this step so that newly-spawned projectiles
   get a full 90 motion ticks before expiry.
7. Resolve player firing — spawn new projectiles for each player
   whose `Flags & FlagDead == 0`, `Flags & FlagTurbo == 0`
   (effective post-§10.6 turbo state, *not* raw `Input.Turbo` —
   the turbo-cancel-via-idle case clears `FlagTurbo` in step 3 and
   so passes this gate), and `Input.FireDir != DirIdle`, subject
   to per-player `fireCooldown` and the §10.1 64-projectile cap.
   Projectiles spawned this tick are inserted with `lifetime = 90`
   and do **not** move or decrement this tick; they begin moving on
   the next tick in step 5.
8. Process respawn timers: any player whose `respawnAt == serverTick`
   is respawned per §10.5 (which may defer to a later tick).
9. Garbage-collect entities marked for removal:
   - Destroyed generators (HP=0 with `FlagDead` as of *this* tick's
     step 5 hit) are removed from the entity store on this same
     tick's GC pass — they never appear in subsequent `Entities()`
     snapshots.
   - If `Config.NoRespawn == true`: any player entity that has
     `FlagDead` set (regardless of which tick the death happened
     on) is removed from the entity store. With `NoRespawn=true`,
     this fires on the same tick the player died, so even the
     death-tick snapshot does not include the corpse.
   - Projectile slots were already freed in steps 5 (motion/wall/
     entity collision) and 6 (lifetime expiry).
   - In the default `NoRespawn == false` mode, DEAD player slots
     persist until §10.5 rewrites them on the respawn tick — they
     are *never* removed by GC.
10. Return `(events, nil)` where `events` is the slice collected
    during steps 3–9 in collection order. Because step 0's preflight
    has already guaranteed that no allocation will fail, this step
    cannot reach the `ErrIDExhausted` path. See §8 for the headroom
    validation that makes exhaustion unreachable in practice.

Steps 3–7 are deliberately segregated so that all entities move under
a consistent world state. A player firing while moving fires *after*
moving; the projectile spawn position is the post-move centre.

---

## 12. Determinism rules

Hard rules; violation is a bug:

- No `time.Now()` or wall-clock anywhere in `internal/sim`. `Tick` is
  driven purely by inputs.
- No `crypto/rand`.
- No goroutines started by `internal/sim` code. The sim is single-
  threaded by contract; the caller may invoke `Tick` from any goroutine
  but must serialise calls.
- No global state. All state lives in `*Sim`.
- No floating-point arithmetic in the simulation hot path. The maze
  generator may call `mazeRand.Float64()` *for branch decisions only*
  (e.g. `r < 0.6` and `r < pDoor`); the result of float math never
  leaks into entity positions or velocities.
- Map iteration order: never iterate `s.entityRNGs` or any other map
  during a tick in a way that affects sim state. Maps are lookup-only.
- **Entity iteration order in tick passes.** All tick steps that
  iterate the entity store and could emit events or affect any other
  entity's state MUST visit occupied slots in **ascending `Entity.ID`
  order**, never in raw slab-slot order. The slab allocator reuses
  freed slots, so two histories that arrive at the same live-entity
  set + `nextID` can hold those entities in different slot positions;
  ID-sorted iteration is what makes the two histories evolve
  identically. Concretely: the projectile-resolution pass (§11
  step 5), the firing pass (§11 step 7), the lifetime-decrement
  pass (§11 step 6), and the respawn pass (§11 step 8) all build
  their work list by collecting candidate IDs and sorting ascending
  before iterating. Internal helpers that don't read or write the
  rest of the world (e.g. an `EntityByID` lookup) are exempt.
- Entity event order: events are appended to a slice as they happen
  inside the tick. Because the iteration order above is ID-sorted,
  the event order is a deterministic function of (a) the §11 step
  numbers and (b) ascending `Entity.ID` within each step. Tests
  assert exact event sequences for fixture scenarios.
- PCG seeding is pinned via `go.mod`'s `toolchain` directive. The
  `TestDeterminism_GoldenFingerprint` golden-hash test fails loudly
  if Go's PCG implementation ever changes.

### 12.1 Fingerprint contents

`Sim.Fingerprint()` is the replay/determinism oracle: two sims agree
at one fingerprint and they agree forever, given the same inputs.
This is only true if the hash covers **every** field that can affect
future ticks. The serialization order below is fixed; every numeric
scalar listed below is emitted in **little-endian** byte order
unless the entry says otherwise (the only exception is item 7,
which appends `pcg.MarshalBinary()` output verbatim — that blob is
big-endian internally). The SHA-256 is computed over the
concatenation of every listed entry.

1. `serverTick (u32)`.
2. `Config.Seed (u32)` — needed because per-entity PCGs in (7) are
   built lazily from `Seed`. Two sims with identical current state
   but different seeds would diverge the moment a player respawns or
   a future AI tick first consults its per-entity PCG. Width matches
   SPEC §4.3.4 `MapInit.seed`.
3. `Width (u16)`, `Height (u16)` — needed because the packed map
   bytes in (7) are row-major and have no embedded shape. Two sims
   with identical packed bytes but mismatched `(W, H)` would
   interpret tile lookups differently in subsequent ticks.
4. `Config.NoRespawn (u8: 0 or 1)` — needed because after the same
   fatal hit, a `NoRespawn=false` sim schedules respawn and keeps
   the DEAD entity for 90 ticks, while a `NoRespawn=true` sim
   removes the entity on the next tick. Two sims that share live
   state immediately post-death would diverge without this field.
5. `nextID (u32)` — the next ID the entity store will issue. Without
   this, two sims with identical live entities can diverge on the
   very next allocation if one of them previously held an extra
   short-lived projectile.
   `quiesced (u8: 0 or 1)` — the quiesced flag from §11 step 0.
   A quiesced sim ignores all subsequent input and returns
   `(nil, ErrIDExhausted)` from every `Tick`; two sims that
   disagree on this flag would diverge on the next tick even with
   identical entities and `nextID`.
6. For each occupied slot in the entity store (i.e. `Entity.ID != 0`),
   visited in ascending `ID` order:
   - Exported fields, in struct-declaration order:
     `ID (u32), Kind (u8), HP (u8), Facing (u8), Flags (u8),
      X (i32), Y (i32), VX (i16), VY (i16)`.
   - Unexported per-entity state, in this order (fields not
     applicable to the entity's `Kind` are emitted as zero):
     `fireCooldown (u8)`, `lastDir (u8)`, `lastInputTick (u16)` —
     players only;
     `respawnAt (u32)`, `deathX (i32)`, `deathY (i32)` — players only;
     `projLifetime (u16)`, `projShooterID (u32)` — projectiles only.
7. For each entity with an initialised per-entity PCG, in ascending
   `EntityID`: the 20-byte slice returned by `pcg.MarshalBinary()`
   appended **verbatim**. (Go's `math/rand/v2.PCG.MarshalBinary`
   emits `"pcg:"` (4 bytes ASCII) followed by 16 bytes of state
   encoded big-endian — `hi` then `lo`. The hash is taken over
   those exact bytes; the "little-endian scalars" rule below does
   not re-encode this blob.)
8. The packed map bytes (the sim's immutable internal copy, not the
   per-call allocation returned by `MapBytes()`).

The maze-generation `mazeRand` PRNG is **not** included: by §6 it is
consumed exclusively during `NewSim`, so its post-init state cannot
affect any subsequent tick. Per-entity PCGs (item 7) are only
included once they have been initialised on first use — but `Seed`
(item 2) is hashed unconditionally, so two sims still diverge in
their fingerprints if either is built from a different seed even
before any per-entity PCG is consulted.
`TestDeterminism_GoldenFingerprint` exercises every category above
by including a respawn (per-entity PCG, items 6 and 7), a
generator-destroy (entity removal affecting item 5 / `nextID`), and
a projectile-expiry (lifetime decrement affecting item 6) in the
scripted timeline.

---

## 13. Test plan

All tests live in `internal/sim/*_test.go`. Test names below are
exact (`TestX` / `BenchmarkX`); CI uses `go test -race -count=1`.

### 13.1 Maze gen golden seeds (`maze_test.go`)

`TestMazeGenGoldenSeeds_<seed>`: for each of 8 fixed seeds —
`0x00000001, 0xDEADBEEF, 0xCAFEBABE, 0xFEEDFACE, 0x00C0FFEE,
0x42424242, 0x80000000, 0xFFFFFFFF` — call `NewSim` with the
**pinned reference config** below, then assert that
`SHA-256(MapBytes())` matches the hex string in
`testdata/mazes/<seed>.hash`.

Pinned reference config (any change here invalidates all eight
golden hashes and requires regeneration via
`go test -run TestMazeGenGoldenSeeds -update`):
```
Config{
  Seed:         <one of the 8 seeds above>,
  Width:        60,
  Height:       40,
  PlayerIDs:    []EntityID{1, 2, 3, 4},  // four players, deterministic
  NoRespawn:    false,
  NoGenerators: false,
}
```
The four-player count is chosen because it exercises both the
generator-placement branch (default mode) and the player-spawn
acceptance order without saturating either. The hash files are
committed; the `-update` flag regenerates them in place.

### 13.2 Connectivity (`maze_test.go`)

`TestMazeConnectivity`: 50 random seeds (deterministic
`testing/quick` config). For each, generate a maze and BFS from one
`TileSpawnPlayer`; assert that **every** `TileFloor`, **every** other
`TileSpawnPlayer`, and **every** `TileSpawnGenerator` is reachable.
Matches the canonical Phase 1 gate in §8 of `SPEC.md`.

### 13.3 Maze invariants (`maze_test.go`)

`TestMazeOuterWall`: 50 seeds; every tile with `tx ∈ {0, W-1}` or
`ty ∈ {0, H-1}` is `TileWall`.

`TestMazeSpawnSeparation`: 50 seeds at the default 60 × 40 size with
≤ 8 players. Asserts the *strict* invariants: no two
`TileSpawnPlayer` tiles within 15 tiles Chebyshev, no
`TileSpawnPlayer` within 15 of a `TileSpawnGenerator`, no two
generators within 12. The default map is comfortably large enough
that the §7.7 fallback to `≥ 10` never fires.

`TestMazeSpawnSeparationSmallMap`: 50 seeds at the minimum 30 × 20
size with 6 – 8 players (the size most likely to exercise the §7.7
fallback). Asserts the *relaxed* invariants that match the fallback
path: spawn-vs-spawn and spawn-vs-generator at `≥ 10`,
generator-vs-generator at `≥ 12`. Additionally asserts that
`NewSim` succeeds (i.e. the relaxed pass yielded ≥ `len(PlayerIDs)`
spawn tiles).

`TestMazeAverageDegree`: 200 seeds at the default 60 × 40 size;
mean average-cell-degree across the sample is in the band
`[3.3, 3.7]` (centred on SPEC §3.1's 3.5 target with ±0.2 tolerance),
and at least 95 % of individual mazes are in the band `[3.1, 3.9]`.
A second variant runs 200 seeds at the minimum 30 × 20 size with
the wider per-maze band `[3.0, 4.0]` to absorb small-sample
variance. Sentinels the doorway-insertion calibration in §7.5
(`pDoor = 0.75`).

`TestMazeGeneratorNeighbourFloor`: 50 seeds; every
`TileSpawnGenerator` has at least one 4-neighbour `TileFloor` tile,
so snipes (Phase 3) will eventually be able to emit.

### 13.4 Physics (`physics_test.go`)

`TestMoveStopsAtWall_<dir>` (N, E, S, W, NE, SE, SW, NW): place a
player one subtile shy of a wall in the given direction; tick once;
assert the player's position is clamped to the wall.

`TestMoveSlidesAlongWall_<dir>` (N, E, S, W): place a player adjacent
to a wall in dir D, move in dir perpendicular to D; assert the
parallel-to-wall component advances by `s` per tick and the into-wall
component is zero.

`TestDiagonalSpeedNormalised`: assert `diag(16) == 11` and
`diag(32) == 22` constants. Then place a player in open space, hold
NE for 100 ticks; assert the resulting tile-space displacement is
within 1 tile of `(100*22/256, -100*22/256)`.

`TestNoCornerSqueeze`: construct a maze fixture with a 1-tile-wide
gap that bends at right angles. Hold diagonal toward the corner from
inside the L; assert the player does **not** pass through the gap on
the diagonal step.

`TestPlayerBlockedByGenerator`: place a generator at a known tile,
hold E toward it; assert the player stops one subtile shy of the
generator AABB and never enters it.

`TestTurboStrictLock`: engage turbo + E for one tick; in subsequent
ticks send turbo + W, turbo + N, etc.; assert the player continues
moving E for every tick where turbo is held (the alternate-direction
inputs are ignored). Then release turbo for one tick (`Input.Turbo
== false`), re-engage with N; assert the player now moves N. Also
covers the turbo-cancel-via-idle case: turbo held with
`Input.Dir == DirIdle` zeroes velocity and clears the TURBO flag on
the same tick.

`TestTurboStateClearedOnRespawn`: engage turbo + E for one tick;
kill the player while turbo is held; let them respawn. After the
respawn tick `T = D + 90`, send a turbo + W input; assert the
respawned player moves W on the next tick (not E). Verify
`FlagTurbo == 0` immediately after respawn (before the new input
arrives) and that `lastDir == DirIdle` so the strict-lock rule
treats the new turbo as a fresh engage.

### 13.5 Combat (`combat_test.go`)

`TestProjectileTravelsAtSpeed32`: fire E from `(x, y)` in open space;
assert after 8 ticks the projectile X has advanced by `8*32 = 256`
subtile units (one tile).

`TestProjectileHitsWallAtExpectedTick`: place a player so that the
wall tile's near edge is exactly `5 * 256 = 1280` subtile units east
of the player's centre; fire E. The projectile spawns at the
player's centre and must travel
`(5 * 256) - projHalfExt = 1280 - 24 = 1256` subtile units before
its leading face touches the wall. Expected impact tick:
`ceil((5 * 256 - projHalfExt) / 32) = ceil(1256 / 32) = 40`,
tolerance ±1 tick. Assert an `entity_kill{reason: 1}` event for the
projectile on that tick.

`TestProjectileHitsEntityHeadOn`: use a hand-built open-floor maze
fixture (no walls between the two players). Place player A at the
centre of tile `(1, 10)` — subtile position `(384, 2688)` — and
player B at the centre of tile `(6, 10)` — subtile position
`(1664, 2688)`. The centres are exactly `1280` subtiles apart
horizontally, both inside the outer wall. A fires E on tick
`T_fire`. With player half-extent 96 and projectile half-extent 24,
the projectile's leading face reaches B's near edge after
`ceil((1280 - 96 - 24) / 32) = ceil(1160 / 32) = 37` motion ticks.
Under the §11 spawn-then-motion ordering, motion starts on the tick
after fire, so the first `entity_hit` and `entity_kill` for B land
on tick `T_fire + 37`. Assert both events on that tick (B is 1 HP)
and `B.Flags & FlagDead != 0` immediately after.

`TestProjectileExpiresAfter90Ticks`: fire in open space with no
target on tick `T_fire`; assert the `entity_kill{reason: 2}` event
arrives on tick `T_fire + 90` (90 motion ticks after the spawn tick,
per the §11 ordering) and the projectile is absent from `Entities()`
immediately after that tick.

`TestNoFriendlyFireSelf`: place a player adjacent to its own
freshly-spawned projectile path; assert no self-hit event (the
shooter is filtered out of hit detection).

`TestProjectilesDoNotCollide`: use a hand-built open-floor fixture
sized to keep both projectiles inside their 90-tick lifetime
(speed 32 → max 90 × 32 = 2880 subtiles ≈ 11 tiles of travel). Place
A at the centre of tile `(5, 10)` firing E and B at the centre of
tile `(10, 5)` firing S. Both projectiles target the centre of
crossing tile `(10, 10)` — each must travel `5 × 256 = 1280`
subtiles, i.e. 40 motion ticks (well under the 90-tick lifetime).
Fire both on the same tick `T_fire`. Both projectiles reach the
crossing tile on tick `T_fire + 40`, so their AABBs overlap on that
tick. Assert no `entity_hit` or `entity_kill{reason: 0}` event has
a `Target` of either projectile — projectiles pass through each
other (§10.2 step 3 excludes `KindProjectile` from candidate
targets). Both projectiles continue past the crossing until they
hit walls or expire by timeout.

`TestGeneratorTakesThreeShots`: shoot a generator three times;
assert two `entity_hit` events, then a third event sequence of
`entity_hit` → `entity_kill` → `generator_destroyed`. After garbage
collection the generator is absent from `Entities()`.

`TestCannotFireWhileTurbo`: hold turbo + a movement direction and
request fire on the same tick; assert no projectile spawns and no
event emitted (`FlagTurbo` is set after §11 step 3, so the §10.1
gate blocks). Releasing turbo allows firing on the following tick.

`TestCanFireDuringTurboIdleCancel`: hold turbo with
`Input.Dir == DirIdle` (the §10.6 turbo-cancel-via-idle case) and
simultaneously request fire. Assert a projectile **is** spawned —
the turbo-cancel rule clears `FlagTurbo` in §11 step 3 before §11
step 7's firing gate, so the player is not effectively turboing.

`TestDeadPlayerCannotFire`: kill a player, then on the very next
tick (and again 30 ticks later, still inside the 90-tick respawn
interval) submit a fire `Input`; assert no projectile is spawned and
no `entity_spawn` event is emitted for either tick. After the player
respawns on tick `D + 90`, a fire input does spawn a projectile.

`TestProjectileGetsFull90MotionTicks`: fire a projectile on tick T;
assert its `entity_kill{reason: 2}` event arrives on tick `T + 90`
(not `T + 89`). The projectile is freshly-spawned on T and does not
decrement on T, so the 90th decrement falls on `T + 90`.

`TestRespawnSoloPlayerUsesDeathPosition` (`respawn_test.go`): single-
player config; in pre-roll, destroy every generator (fire enough
projectiles to bring each to HP 0) so that the live-hostiles list
in §10.5 step 3 is empty when the respawn fires. Then kill the
player; assert the respawn tick selects a `TileSpawnPlayer` whose
Chebyshev distance from the recorded death position is in the top-3
of available spawns, breaking ties as specified in §10.5. This is
the test path that exercises the "no live hostile" fallback.

`TestRespawnPicksMinDistanceOverAllHostiles` (`respawn_test.go`):
multi-player config with two live opposing players placed at known
positions, plus a known generator. Kill the respawning player.
Assert the chosen spawn maximises `min(distance to player A,
distance to player B, distance to generator)`, i.e. picks one of
the three safest tiles by the worst-case nearest-hostile rule
(SPEC §3.9). A fixture maze ensures the answer is uniquely
top-3 ordered.

`TestRespawnDeferredWhenAllBlocked` (`respawn_test.go`): construct a
fixture where every `TileSpawnPlayer` is occupied by a live
non-respawning player's AABB on the scheduled respawn tick. Assert
no `entity_spawn` event that tick, `respawnAt` advances by 1, and
the actual respawn occurs on the first tick where at least one spawn
is free. No panic from `IntN(0)`.

### 13.6 Determinism, replay, and identity (`sim_test.go`)

`TestDeterminism_SameSeedSameInputs`: run two `*Sim` instances with
the same `Config` and the same scripted input list for 1000 ticks;
assert `s1.Fingerprint() == s2.Fingerprint()` at every tick and the
returned `[]Event` slices are deep-equal at every tick.

`TestDeterminism_GoldenFingerprint`: load
`testdata/replays/baseline.inputs` (a committed binary file of
scripted inputs for 4 players over 600 ticks), run the sim, and
assert `s.Fingerprint()` after the final tick matches the hex in
`testdata/replays/baseline.hash`. This is the cross-OS / cross-arch
gate listed in SPEC §12. CI executes the test natively on **all
four** of the SPEC-named platforms:
- `ubuntu-latest` (linux/amd64).
- `ubuntu-24.04-arm` (linux/arm64) — GitHub Actions' free Linux
  arm64 runner.
- `macos-14` (darwin/arm64 Apple Silicon).
- `windows-latest` (windows/amd64).

All four runners must pass `go test -race -count=1
./internal/sim/...` (or the platform's nearest equivalent —
Windows-on-amd64 currently lacks `-race` support on `windows/arm64`
only, not `windows/amd64`, so the standard `-race` flag applies).
Cross-compiled-only builds (e.g. `GOOS=linux GOARCH=arm64 go build`)
are insufficient because they verify the test binary compiles, not
that PCG and other arithmetic produce identical bytes when
executed.

`TestEntityIDsStableUnderRespawn`: kill a player and let them respawn;
assert their `EntityID` is unchanged. Assert no other live entity
ever holds an ID that was previously assigned (no reuse of dead IDs
for *different* entities).

`TestNewSimRejectsEmptyPlayerIDs`: call `NewSim` with an empty
`PlayerIDs` slice. Assert `NewSim` returns `ErrNoPlayers` (the typed
error from §8 rule 1) and produces no `*Sim`.

`TestNewSimRejectsOutOfHeadroomPlayerIDs`: call `NewSim` with
`PlayerIDs: []{math.MaxUint32 - 10}`. Assert `NewSim` returns a typed
error referencing the headroom rule from §8 and produces no `*Sim`.

`TestTickReturnsErrIDExhausted`: construct a sim and call the
`sim.ForceExhaustedForTest(s *Sim)` helper exported from
`internal/sim/export_test.go` (compiled only for in-package
`*_test.go` consumers — no build tag, no extra CI invocation, no
production-binary impact). The helper puts the sim into a state in
which the next entity-ID allocation will fail; the exact mechanism
is an implementation detail (the §8 contract says exhaustion is
detected *before* `nextID` would cross `math.MaxUint32`, so an
in-place uint32 wrap is never observed). Drive a tick in which a
player fires (which would normally allocate a projectile ID); assert
the tick returns `(nil, ErrIDExhausted)` and `serverTick` does
**not** advance. Then drive two more `Tick` calls and assert both
also return `(nil, ErrIDExhausted)` with no state advance.

`TestNoRespawnRemovesDeadPlayer`: build a sim with
`Config.NoRespawn = true`, two players A and B. A fires E; B takes
the projectile and dies on tick `D`. Assert (a) tick `D`'s event
list contains `entity_hit` then `entity_kill` for B; (b) B is
**absent** from `Entities()` immediately after `Tick(D)` returns
(removed by the same tick's §11 step 9 GC pass); (c) B remains
absent from every subsequent `Entities()` and snapshot; (d) no
`entity_spawn` event for B is ever emitted again. This is the
strict reading of SPEC §8 Phase 2's gate: B is gone from the store
the moment the kill is processed.

`TestLastInputTickPersistsAcrossTicks`: drive a 5-tick scripted
input stream where each tick's `ClientTick` increments by 1. Between
ticks, call `Sim.LastInputTick(playerID)` and assert it equals the
`ClientTick` that was most recently consumed. After a tick where the
player submits no input, assert `LastInputTick` stays at the prior
value (it is not zeroed).

### 13.7 Property tests (`sim_test.go`)

`TestPropertyNoEntityInsideWall`: `testing/quick` with 100 random
configs (seed, valid W/H, 1–8 random `PlayerIDs`) and 500 ticks of
random valid inputs per run. After each tick, for every live entity,
assert that every corner of its AABB is in a non-`TileWall` tile and
that the entity's centre coordinates are in `[0, W*256) × [0, H*256)`.

`TestPropertyProjectileNeverPassesThroughWall`: same harness; for
each projectile, record its pre-tick and post-tick positions and
check that the segment does not cross any `TileWall` tile boundary
without producing an `entity_kill{reason: 1}` event that tick.

### 13.8 Benchmarks (`bench_test.go`)

`BenchmarkSimTick_60x40_8P`: 8 players, no projectiles in flight,
no events; 1000 ticks; `b.ReportMetric(ns/op)`.

`BenchmarkSimTick_60x40_8P_FullProjectiles`: 8 players each firing
every tick they can (cooldown-limited); 1000 ticks.

`BenchmarkMazeGen_60x40`: `NewSim` end-to-end.

Targets per §1: 1 ms/op on `ubuntu-latest` without `-race`, 5 ms/op
under `-race`. CI invokes
`go test -bench=. -run=^$ -benchtime=2s -count=3` on `ubuntu-latest`
and parses `ns/op` to fail the build if a regression > 25 % vs. the
baseline committed in `bench.baseline.json`.

---

## 14. Testdata layout

```
testdata/
├── mazes/
│   ├── 00000001.hash       # SHA-256 hex of packed map bytes
│   ├── DEADBEEF.hash
│   ├── CAFEBABE.hash
│   ├── FEEDFACE.hash
│   ├── 00C0FFEE.hash
│   ├── 42424242.hash
│   ├── 80000000.hash
│   └── FFFFFFFF.hash
└── replays/
    ├── baseline.inputs     # binary: u32 tick_count, then per-tick u8 input_count, [PlayerInput]
    └── baseline.hash       # hex of Sim.Fingerprint() after the last tick
```

The replay binary format is internal to Phase 1's test harness — it
is **not** the wire protocol (which is defined in `SPEC.md` §4.3.2).
For Phase 1 we use the simplest possible encoding: a custom Go
struct serialised via a tagged binary format that lives **only**
inside `internal/sim/sim_test.go`'s helper functions, documented in
a comment above those helpers. The bytes are committed but
regenerated by the test itself when the `-update` flag is passed.
Phase 5 will promote the format to a production `internal/sim/
replay.go` (and a CLI tool at `scripts/replay.go`); Phase 1 has no
such file.

---

## 15. Risks

- **PCG stability.** If Go ever changes `math/rand/v2.PCG` byte-for-
  byte behaviour, every golden hash breaks. Mitigation: pin
  `toolchain` in `go.mod`; the golden-fingerprint test will fail
  loudly on a Go upgrade and force a deliberate regeneration.
- **`pDoor` calibration drift.** The 0.75 doorway probability was
  chosen to centre the mean average-cell-degree on SPEC §3.1's 3.5
  target; if `TestMazeAverageDegree` flakes after
  a future change to maze gen (e.g. larger rooms reducing the wall
  pool), the band may need re-tightening rather than `pDoor`
  re-tuning. Mitigation: the band test runs 200 seeds, comfortably
  enough to detect a drift > 2 %.
- **Generator-blocks-player + tight maps.** With many generators on
  a small map, a player may be wedged behind a generator at spawn.
  Phase 1 spawns are perimeter-only and generators are interior, so
  this should not arise; verified by `TestMazeSpawnSeparation`.
- **Player AABB vs gap math.** Player half-extent 96, tile width 256:
  32 subtile units of clearance per side. A future increase to player
  hitbox > 112 would no longer fit; a build-time `static_assert`-style
  check in `config.go` (`init()` panic) guards this.
- **Determinism under `-race`.** Race-detector iteration order
  *should* not affect a single-threaded sim, but slice-of-pointer
  patterns could in principle. Mitigation: never store `*Entity` in
  per-tick locals; index by slot in the `entities` slice.

---

## 16. Open questions

These are flagged for codex review and for the author to decide
before implementation begins. Defaults are listed if no decision is
forced.

1. **Player-vs-player AABB:** §3.3 of `SPEC.md` says players don't
   block each other. Does a projectile passing through an overlapping
   pair of players hit one or both? Default: the projectile hits the
   first by ascending `EntityID` (matching the tie-break in §10.2
   step 3), consumes itself, no second hit.
2. **Replay format inside `internal/sim`:** Phase 1 currently keeps
   the replay binary inside test files. Should it be promoted to
   `internal/sim/replay.go` already? Default: no — the real replay
   tool is a Phase 5 build per `scripts/replay.go`; Phase 1 keeps it
   private to tests.
