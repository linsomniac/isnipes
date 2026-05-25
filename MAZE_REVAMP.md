# Maze Revamp — Wide-Corridor "Classic Snipes" Maze

Status: **planned** (approved via mock-up review 2026-05-25). This document
supersedes the maze-generation portions of `SPEC.md §3.1` and `PHASE1.md §7`
(the growing-tree perfect maze). It is the implementation plan for replacing
the current tiny-corridor maze with one that matches the look and feel of the
original SuperSet/Novell *Snipes* (`/tmp/snipes-original.jpg`).

Visual reference (the approved look):
- `docs/maze-revamp/v4-c6-zoom.png` — in-game camera zoom.
- `docs/maze-revamp/v4-c6-overview.png` — whole 120×80 map.
- `docs/maze-revamp/prototype.go.txt` — the throwaway Go prototype these were
  rendered from (stdlib-only; algorithm is the basis for the real generator).

---

## 1. Problem and goal

### 1.1 What's wrong now
The current generator (`internal/sim/maze.go::carveGrowingTree`) builds a
*perfect maze* on odd cell coordinates: **1-tile-wide corridors** separated by
**1-tile-wide walls**, multiply-connected by opening ~75% of inter-cell walls.
The player hitbox is 0.75 tiles across — `config.go::init` even asserts it
*just barely* fits a 1-tile corridor (32 subtile clearance per side). The
result is a dense lattice of cramped, tedious passages, and walls render as
solid blocks as chunky as the corridors are narrow.

### 1.2 What we want (the original's feel)
`/tmp/snipes-original.jpg` is a *zoomed-in* view of a larger map: a genuine
**maze** (movement is channeled everywhere) but with **thin walls** and **wide
corridors** (passages ≈ 2–3× the player's width). It reads as thin lines on a
dark field carving out roomy, connected chambers.

### 1.3 Approved decisions (mock-up review)
| Decision | Value |
|---|---|
| Style | Wide-corridor **braided maze** (coarse cell grid; carve, then add loops + a few merged chambers). |
| Wall thickness | 1 tile (collision stays per-tile). |
| Corridor width | **C = 6 tiles** (`pitch = 7`). |
| Default map size | **120 × 80 tiles** (= 17 × 11 cells; within the existing SPEC max — no cap bump). |
| Entity scale | Player ≈ **2 tiles**, snipe ≈ **1 tile** → **player ≈ 2× snipe** (visual + hitbox). |
| Architecture | Unchanged: per-tile collision, 2-bit tile packing, integer/deterministic sim. |

The thin-wall look is achieved by a **finer effective grid** (the player spans
~2 tiles instead of 0.75), so a 1-tile wall renders as a thin line relative to
a 6-tile corridor. This is a **mechanical rescale**, not new architecture.

---

## 2. The new generator (`internal/sim/maze.go`)

Replace `carveGrowingTree` + `carveRooms` + `insertDoorways` with a
wide-corridor maze on a **coarse cell grid**. Tiles remain `WALL`/`FLOOR`/
`SPAWN_PLAYER`/`SPAWN_GENERATOR`; only how they're produced changes.

Definitions (for corridor width `C`, `pitch = C + 1`):
- `cellsX = (W-1)/pitch`, `cellsY = (H-1)/pitch`.
- Cell `(cx,cy)` occupies tiles `x ∈ [cx*pitch+1, cx*pitch+C]`, same in `y`.
- The 1-tile wall between two adjacent cells is the strip on the shared edge.

Pipeline (single PRNG, fixed consumption order for determinism — §4.6):
1. **Carve** a spanning maze over the cell grid with a recursive-backtracker
   (DFS) — long, winding corridors. Carving a cell fills its `C×C` block to
   `FLOOR`; linking two cells carves the `C`-long, 1-tile wall strip between
   them.
2. **Braid** in loops: for each still-closed inter-cell wall, open it with
   probability `pBraid ≈ 0.18`. Removes dead-ends and guarantees escape
   routes (SPEC §3.1 intent: multiply-connected).
3. **Chambers**: merge `nRooms ≈ 5` random 2×2 cell super-blocks (carve all 4
   cells + their internal walls) into open rooms for size variety / "nests".
4. **Generators** (§2.1) and **player spawns** (§2.2) placed on the cell grid.
5. **Connectivity validation** (`connectivityOK`, unchanged) + retry via the
   existing §7.6 seed-perturbation scheme.

The outer border is `WALL` by construction (cells start at tile index 1; the
leftover `(W-1) mod pitch` tiles on the far edges stay `WALL`).

### 2.1 Generator placement (cell-based)
Replace the Chebyshev-12 tile rule with a **cell** rule: choose generator
**cells** (place the generator at the cell center) that are ≥ `genCellGap ≈ 3`
cells apart, in the interior (≥ 1 cell from the border band). Keep the
`computeGeneratorCount` count formula but re-derive its area cap from cell
count rather than `genInterior/144`.

### 2.2 Player-spawn placement (cell-based)
Choose **perimeter-band cells** (≤ `perimeterCellBand ≈ 2` cells from the
edge), ≥ `spawnCellGap ≈ 3` cells apart and from generator cells; place spawn
at the cell center. Keep the strict→fallback distance relaxation and the
`extras` respawn pool.

Centering entities in `C×C` cells guarantees ≥ (C−2)/2 tiles of wall clearance
even for the 2-tile player, so the "no entity ends a tick inside a WALL"
property (§5) holds trivially.

---

## 3. Constant rescale

Scale factor **k = 2** for distances/speeds/map (60→120 linear); entity
half-extents are set directly for the player-≈-2-tiles / player-≈-2×-snipe
targets. All values tunable; these are the proposed defaults.

### 3.1 Entity hitboxes (subtile half-extents)
| Entity | File | Old | New | Diameter (tiles) |
|---|---|---:|---:|---:|
| Player | `config.go:146` | 96 | **256** | 2.0 |
| Snipe | `levels.go:17` | 80 | **128** | 1.0 |
| Generator | `config.go:147` | 112 | **256** | 2.0 |
| Projectile | `config.go:148` | 24 | **48** | 0.375 |

Player 256 / snipe 128 = **2×**. Update the `config.go:162` build-time
assertion from "player fits a 1-tile corridor" to "player diameter ≤ `C` tiles
(min passage width)".

### 3.2 Speeds (subtile/tick, × k)
| Speed | File | Old | New |
|---|---|---:|---:|
| Player normal | `config.go:139` | 16 | **32** |
| Player turbo | `config.go:140` | 32 | **64** |
| Projectile | `config.go:141` | 32 | **64** |
| Snipe base | `levels.go:18` | 12 | **24** |
| Snipe brutal | `levels.go:19` | 15 | **30** |

Invariant preserved: turbo == projectile (64). Snipe (24) < player normal (32).
Projectile lifetime stays **90 ticks** (range scales with speed automatically:
~22 tiles, matching the 2× map).

### 3.3 Tile-denominated distances (× k)
| Constant | File | Old | New |
|---|---|---:|---:|
| LOS A–F / G–M / N–S / T–Z | `levels.go:58,61,64,68` | 6 / 8 / 10 / 14 | **12 / 16 / 20 / 28** |
| AOI inner projectile | `aoi.go:12` | 10 | **20** |
| AOI inner snipe/gen | `aoi.go:13` | 20 | **40** |
| AOI hysteresis projectile | `aoi.go:14` | 12 | **24** |
| AOI hysteresis snipe/gen | `aoi.go:15` | 22 | **44** |

`ai.go:124` BFS `maxDepth = LOSRadius + 4` auto-scales (consider `+8`).
Tick-based values (`bfsRecomputeEvery`, emit cooldowns, fire cooldown, respawn,
spawn-invuln) are **unchanged** — they are time, not distance.

### 3.4 Default map size
Change 60×40 → **120×80** in all three defaults: `config.go:133-134`,
`lobby.go:572-573`, `match.go:281-284`. SPEC min/max (`config.go:152-155`)
stay 30×20 … 120×80 (new default = current max). The generator must handle the
min gracefully (few cells) — add a test; raise the min later if desired.

---

## 4. Client render (`web/src/render.ts`)

1. **Per-kind entity size** — `render.ts:242` currently draws every entity at
   `TILE_PX * 0.4`. Replace with a per-kind radius derived from the hitbox so
   render == collision: player `1.0·TILE_PX`, snipe `0.5·TILE_PX`, generator
   `1.0·TILE_PX`, projectile `~0.19·TILE_PX`. This is what makes the player
   visibly ~2× a snipe.
2. **Zoom** — with a 120×80 map, consider lowering `TILE_PX` (e.g., 32 → 24)
   so a useful slice shows on a typical screen; the camera is already generic
   (`computeCamera` clamps to map bounds). Pick the value that matches
   `docs/maze-revamp/v4-c6-zoom.png`.
3. Minimap (`hud.ts:159`) maps world-subtile coords into a rect — **no change**
   (scales automatically). `OffscreenCanvas` map cache grows 4× in area —
   fine.

No proto change: `MapInit` already carries `u16 width/height` + 2-bit packing;
120×80 = 2400 bytes, within the frame ceiling and the existing schema checksum.

---

## 5. Tests to update / add

Server (`go test ./...`):
- `maze_test.go::TestMazeGenGoldenSeeds` — regenerate hashes with `-update`
  (new generator ⇒ new bytes). Keep the 8 pinned seeds.
- `TestMazeAverageDegree` (`:271`) — the "avg degree ≈ 3.5" metric is specific
  to the old 1-tile cell graph; **replace** with a wide-corridor-appropriate
  check (e.g., loop fraction > 0 from braiding, and corridor-width invariant:
  every FLOOR run perpendicular to a corridor is ≥ C or part of a chamber).
- `TestMazeConnectivity` (`:63`), `TestMazeOuterWall` (`:116`),
  `TestMazeGeneratorNeighbourFloor` (`:330`) — should still pass; verify.
- `TestMazeSpawnSeparation` (`:143`) + `…SmallMap` (`:210`, 50×30) — distances
  are now cell-based; update expectations and add a 120×80 case.
- `property_test.go` — "no entity ends a tick inside a WALL" must hold with the
  256-half-extent player; centering in cells guarantees it.
- Determinism / replay tests — must stay green (reproducibility, not values);
  update any fixture that bakes 60×40.
- `testdata/proto/mapinit_baseline.bin` — regenerate if it encodes a 60×40 map
  (schema checksum `testdata/proto/checksum.txt` is unchanged — schema is the
  same).
- `testdata/perf_baseline.json` / `bench_test.go` — re-baseline (4× tiles;
  expected to stay well within budget).

Client (`pnpm test` + Playwright):
- `render` golden / unit tests and `web/tests/e2e/golden.spec.ts-snapshots` —
  regenerate for the new entity sizes and (if changed) `TILE_PX`.

---

## 6. Implementation phases (each: tests green + manual smoke = DoD)

**Phase A — Sim generator + constants.** Rewrite `maze.go` (§2); apply the
§3.1–§3.3 sim constants (`config.go`, `levels.go`); default 120×80
(`config.go`); fix the `config.go` assertion. Update/regenerate the `sim`
tests (§5). DoD: `go test ./internal/sim/...` green; determinism + property
tests pass.

**Phase B — Server wiring.** Defaults in `lobby.go` / `match.go`; AOI bands in
`aoi.go` (§3.3); regenerate proto fixtures if needed; match/snapshot tests.
DoD: `go test ./...` green; a match starts and plays on the new maze (smoke).

**Phase C — Client render.** Per-kind entity radius + zoom tuning
(`render.ts`, §4); regenerate render goldens + e2e snapshots. DoD:
`pnpm test` + e2e green; in-app view matches `docs/maze-revamp/v4-c6-zoom.png`.

**Phase D — Docs.** Update `SPEC.md` (§3.1 algorithm + default size, §3.2
hitboxes, §3.3 speeds, §3.7 LOS, §5.3 AOI) and `PHASE1.md §7` to describe the
wide-corridor generator; note this file as the source of the change.

**Phase E — Verify.** Run the binary, start a match, screenshot, and compare
against the approved mock-up (`/run` or `/verify`).

---

## 7. Risks / notes
- **Determinism:** the rewrite changes PRNG draw order ⇒ golden hashes change
  (regenerate). Keep one PRNG stream in a documented, fixed order
  (maze carve → braid → chambers → generators → spawns), per §4.6.
- **Player fits everywhere:** 2-tile player in 6-tile corridors/chambers, with
  entities cell-centered, leaves ≥ 2 tiles clearance — the build assertion +
  property test guard this. The narrowest passage in this model is a full
  corridor (C tiles); there are no sub-corridor doorways.
- **Perf:** 9,600 tiles (4× today). BFS/LOS/AOI/packing all remain trivial;
  re-baseline the bench.
- **Scope boundary:** if more cells / a larger world is wanted later, raise the
  SPEC map cap above 120×80 — out of scope here.
