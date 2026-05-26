# isnipes — Specification and Implementation Plan

A modern, browser-playable, multiplayer reimagining of the early-1980s
SuperSet/Novell game **Snipes** (originally a DOS title, with the networked
NetWare version `NSNIPES.EXE` associated with 1983). Playable on LAN with
negligible latency, and playable over the public Internet when latency
permits.

---

## 0. Confirmed decisions

| Area | Decision |
|---|---|
| Visual style | Modern tile-based 2D (top-down). |
| Game modes (v1) | Free-for-all PvP + PvE (players, snipes, and generators all hostile to player). |
| Server stack | Go (single static binary for both game server and room/lobby server). |
| Net authority | Server-authoritative simulation with client-side prediction and reconciliation. |
| Client stack | TypeScript + HTML5 Canvas2D, served as static assets by the Go binary. |
| Transport | WebSocket (binary frames) for in-game; JSON over WebSocket for lobby. |
| Tick rate | 30 Hz simulation, 15 Hz snapshots (every 2nd tick), 100 ms client interpolation buffer. |
| Controls (default) | **Classic** preset: arrow keys move, WASD fires (with diagonal key combos), Space = turbo. Rebindable. |

Everything in [§13 Open questions](#13-open-questions--future-work) is an
assumption I have made for v1 and is open to override.

---

## 0.5. Must match prompt — traceability

The user's prompt specifies a concrete set of requirements. The table below
maps each one to the spec section that delivers it, so future edits don't
quietly drop a requirement.

| Requirement from prompt | Spec section(s) |
|---|---|
| Runs as a browser app | §0, §7 |
| Server component to coordinate clients | §4.1, §5 |
| Simple room server for players to meet & start games | §6 (full), §8 Phase 2 (minimal subset shipped early) |
| Plays across a TCP/IP LAN, and Internet when latency allows | §1.1, §4.1, §4.3, §4.4 |
| Large maze | §3.1 |
| Generators positioned in the maze at creation | §3.1 (placement), §3.6 |
| Generators emit snipes (NPCs) | §3.5, §3.6 |
| Snipes move around and attack players in view | §3.5 (`PATROL → CHASE → ATTACK` w/ LOS) |
| Players spread around the maze | §3.1 (`SPAWN_PLAYER` tiles), §3.9 |
| Players can move and shoot | §3.3, §3.4 |
| Shots are deadly to snipes, generators, and other players | §3.2 (HP), §3.4 (friendly fire on) |
| Normal and turbo movement speeds | §3.3 |
| Turbo speed equals shot speed | §3.3 (both are `224` subtile units/tick) |
| Tests through development for robust/reliable behavior | §8 per-phase tests, §9 strategy |

---

## 1. Goals and non-goals

### 1.1 Goals
- Faithful **spirit** of Snipes: maze, generators, snipes, players, shoot
  everything, turbo movement, "shots travel at turbo speed".
- Playable in any modern browser (Chrome, Firefox, Safari) without plugins.
- Server runs as a single static Go binary; no external runtime dependencies.
- LAN-quality experience at < 30 ms RTT; Internet-playable up to ~120 ms RTT.
- Robust automated test suite at every layer; CI-gated.
- Reproducible matches: given a seed, the maze and initial entity placement
  are deterministic — enables replay tests and regression fixtures.

### 1.2 Non-goals (v1)
- Accounts, persistent stats, ranked play, friends lists.
- Mobile/touch input (keyboard only for v1; gamepad as stretch).
- 3D, custom skins/cosmetics, in-app purchases.
- Anti-cheat beyond basic server validation (no kernel-level / replays).
- **Live spectators** (third parties joining a match purely as observers)
  — stretch goal in v1.1. Note: eliminated players still get a **dead-cam**
  mode within the match they were playing (§3.9); that is in scope.

---

## 2. Background: what Snipes actually was

Sources (verified):
- [Wikipedia — Snipes (video game)](https://en.wikipedia.org/wiki/Snipes_(video_game))
- [MobyGames — Snipes (1982)](https://www.mobygames.com/game/15288/snipes/)
- [`snipes(6)` manpage (mankier)](https://www.mankier.com/6/snipes)

- Created by SuperSet Software (whose principals went on to found Novell).
  The DOS release dates from **1982** per MobyGames; the **networked
  multiplayer version `NSNIPES.EXE` is associated with 1983** and shipped
  with NetWare to exercise the LAN stack. References to "the 1983 NetWare
  game" usually mean the networked variant.
- Text-mode (CP437) game; shipped as `NSNIPES.EXE` / `NCSNIPES.EXE`.
- Player navigates a randomly generated maze containing **hives / generators**
  that emit **snipes** (NPCs). Wikipedia's summary of the objective is
  "destroy snipes and their hives, **and/or** destroy other networked
  players" — i.e. the **primary** loop is cooperative PvE against the
  hives, and PvP is an **optional** secondary mode. This spec ships a
  single FFA-with-PvE mode for v1 per the chosen scope, but the win
  conditions in §3.8 preserve the "kill all hives + snipes" cooperative
  ending alongside last-player-standing.
- **Controls (per Wikipedia / manpage):** arrow keys for movement; **W A S D**
  fire in cardinal directions; key combinations fire (and move) diagonally;
  **Space** = turbo, "extra velocity to run away from difficult situations."
- Difficulty selected via a letter (A–Z) controlling enemy types, accuracy,
  diagonal-shot wall-bounce behavior, wall-collision rules, and hive
  resistance, plus a number (1–9) controlling max concurrent snipes, hive
  count, and lives — 234 combinations.
- `nsnipes` multiplayer ran across NetWare via a shared network drive
  (file-based coordination), not direct sockets. We will not reproduce that;
  we use TCP/WebSocket instead.

---

## 3. Gameplay specification (v1)

### 3.1 The maze

> **Superseded by [`MAZE_REVAMP.md`](MAZE_REVAMP.md) (2026-05).** The maze is now
> a **wide-corridor braided maze** (6-tile corridors separated by 1-tile walls)
> on a **120 × 80** default grid, with entity sizes/speeds and tile-denominated
> distances scaled so the player spans ~2 tiles. The growing-tree description
> below is kept for history; `MAZE_REVAMP.md` is authoritative for the generator
> and the rescaled §3.2 / §3.3 / §3.7 / §5.3 numbers.

- A rectangular grid of **tiles**. Default: **120 columns × 80 rows**.
  Configurable per match (min 50×40, max 120×80).
- Each tile is one of: `WALL`, `FLOOR`, `SPAWN_PLAYER`, `SPAWN_GENERATOR`.
- Walls block movement and projectiles.
- The maze is enclosed by an outer wall of `WALL` tiles.
- **Generation algorithm** (historical — see `MAZE_REVAMP.md §2` for the current
  wide-corridor generator; deterministic given a seed):
  1. Carve corridors with a **growing-tree** algorithm (newest-cell bias 0.6,
     random bias 0.4) to give a mix of long corridors and small rooms.
  2. Randomly carve N "rooms" (rectangular openings) for breathing space:
     5–10 rooms of size 4×4 to 8×8, no overlap with map edge.
  3. Open additional doorways between adjacent cells to make the maze
     **multiply-connected** (not a pure tree). Target average degree ≈ 3.5
     so there are always escape routes.
  4. Place `SPAWN_GENERATOR` tiles in the maze interior, well separated
     (Poisson-disk sampling, min distance ≈ 12 tiles). Count is per-match.
  5. Place `SPAWN_PLAYER` tiles distributed around the perimeter regions,
     min distance ≈ 15 tiles from each other and from generators.

- Coordinates: integer tile indices `(tx, ty)`; entity positions are
  fixed-point `(x, y)` in **subtile units** (1 tile = 256 subtile units) to
  allow smooth continuous movement while keeping the simulation integer-only
  and deterministic across platforms.

### 3.2 Entities

All entities have a stable 32-bit `EntityID`, a `Kind`, position, velocity,
facing direction, and HP. **`EntityID = 0` is reserved as a sentinel**
meaning "no entity" — used by `Snapshot.your_entity_id` while in
dead-cam (§3.9), by `Event.actor` for system/environmental events
(§4.3.2), and by `Event.target = 0` for events not tied to a specific
entity. The allocator never issues `0` to a live entity; IDs start at 1.

Hitbox radii below are the MAZE_REVAMP.md values (the player spans ~2 tiles and
is ~2× a snipe); the parenthetical is the pre-revamp value.

| Kind | Description | HP | Hitbox radius (subtile units) |
|---|---|---|---|
| `PLAYER` | Human-controlled. | 1 (1-shot kill, classic feel) | 256 (was 96) |
| `SNIPE` | NPC enemy spawned by generators. | 1 | 128 (was 80) |
| `GENERATOR` | Static; emits snipes. | 3 | 256 (was 112) |
| `PROJECTILE` | A shot fired by player or snipe. | n/a | 48 (was 24) |

- 1-HP players make matches fast and tense, faithful to the original.
  Optional `hp_per_player` server-config knob for friendlier modes.

### 3.3 Movement and physics

- **Tick rate:** simulation runs at **30 Hz** (33.3 ms per tick). Determinism
  requires fixed timestep, never variable.
- **Speeds (in subtile units per tick):**
  - Player normal speed: 112 (3.5× the MAZE_REVAMP base of 32, for lively
    traversal of the wide-corridor map).
  - Player turbo speed: 224 — **identical to projectile speed**, per the
    original. Locks the player into a single direction while turbo is held
    (no instant 180s at full speed).
  - Snipe speed: 24 (slower than a non-turbo player but they have numbers).
  - Projectile speed: 224 (= player turbo).
  - **Cap:** every per-axis step stays < `subtilePerTile` (256). The wall
    sweep checks only the destination tile, so a ≥256 step could tunnel a
    1-tile wall; a true 4× would require a continuously-swept wall check.
- **Input model:** 8-way intended direction (N, NE, E, SE, S, SW, W, NW) +
  turbo flag + fire-direction-or-none. Continuous position update each tick:
  `pos += speed * unit(dir)`.
- **Wall collisions:** swept AABB against the tile grid; on collision, the
  axis that hit is zeroed (so you can slide along walls). Diagonal movement
  is normalized so it isn't √2× faster.
- **Entity-vs-entity collisions:**
  - Players do **not** block each other (avoids body-blocking grief in FFA).
  - Snipes block other snipes weakly (cheap separation steering).
  - Generators are immovable and block movement.
- **Death:** entity goes to `DEAD` state, becomes non-colliding, scheduled
  for removal/respawn.

### 3.4 Combat
- Firing: a player fires one projectile in one of 8 directions; cooldown
  **6 ticks (200 ms)**. Cannot fire while turbo'ing (per original feel —
  "use turbo to run away").
- Projectiles travel in a straight line at speed 224; despawn on wall hit
  or after 90 ticks (3 s) max lifetime.
- Hit detection: at each tick, the projectile's swept segment is tested
  against all entity hitboxes. First hit (front-of-line) registers; the
  projectile despawns. Friendly-fire is on (FFA mode).
- **Lag compensation** (server-side, in **server-tick** time only):
  - The server maintains a per-entity position **ring of 9 samples**
    indexed by `server_tick`, retaining ticks `T_now - 8` through
    `T_now` inclusive. The maximum rewind window is therefore
    `LAG_COMP_TICKS = 8` ticks (≈ 267 ms) and every tick in that window
    has a stored sample.
  - Ping/Pong (every 500 ms, bidirectional — §4.3.3) updates a
    per-connection OWT estimate (filtered EWMA, α = 0.2). The OWT
    estimate is converted to ticks: `owt_ticks = round(owt_ms / 33.33)`.
  - When the server processes a fire `Input` arriving at
    `server_tick = T_now`, it computes
    `T_view = T_now - owt_ticks - interp_ticks`, where
    `interp_ticks = 2` (the client renders ~100 ms behind real-time,
    which is ~3 ticks; we use 2 to bias slightly toward the present —
    see §4.4). It then tests the projectile's swept segment against each
    candidate target's *historical* position at `server_tick = T_view`.
  - `T_view` is clamped to `[T_now - LAG_COMP_TICKS, T_now]` =
    `[T_now - 8, T_now]`. Both endpoints have stored samples in the
    9-entry ring. A shot whose ideal rewind would exceed the max is
    tested at the oldest available sample; this caps the
    "shot-around-a-corner" surprise.
  - `client_tick` does **not** index any server-side ring; it exists
    solely so the client can replay buffered inputs on snapshot receipt
    (§4.4). The server never has to map `client_tick → server_tick`.

### 3.5 Snipes (AI)
- Spawned by generators on a per-generator timer (default 1 snipe every
  4–8 seconds, jittered per-generator). A generator's emission tick is
  suppressed when the **global** `max_snipes_total` cap (§3.7) is
  reached or when no adjacent spawn slot is free (§3.6); the cooldown
  is **not** reset on suppression, so generators resume emitting as soon
  as room opens up. There is no separate per-generator alive cap.
- State machine: `IDLE → PATROL → CHASE → ATTACK → DEAD`.
  - `PATROL`: wander randomly, biased forward, change direction at walls
    or every 30–90 ticks.
  - `CHASE`: triggered when any player enters line-of-sight (tile raycast,
    ≤ 10 tiles). Path toward the player using BFS on the tile grid,
    recomputed every 15 ticks (cheap; ≤ 60 snipes recomputing staggered).
  - `ATTACK`: when LOS exists and projectile path is clear, fire toward
    player's predicted position (lead = `dist / proj_speed`). Cooldown
    20 ticks (~660 ms).
- Snipe accuracy and aggression are tied to the level letter (see §3.7).

### 3.6 Generators
- HP 3. Spawned on `SPAWN_GENERATOR` tiles at maze creation. A generator
  itself blocks movement (§3.3), so a snipe cannot literally overlap it.
- **Snipe spawn placement:** on each spawn tick, the generator picks an
  emission slot from the 8 neighboring tiles in a deterministic order
  (N, E, S, W, NE, SE, SW, NW for that generator's `EntityID`-derived
  rotation). The first slot meeting all of (a) tile is `FLOOR`, (b) no
  entity currently occupies the spawn hitbox, (c) the snipe hitbox does
  not overlap any wall, is used. If no slot qualifies this tick, emission
  is deferred to the next spawn tick; the cooldown does **not** reset.
- The new snipe spawns with 0 velocity, facing away from the generator,
  and gets 0.5 s of post-spawn invulnerability so it doesn't immediately
  die to a player who was camping the generator with a projectile in flight.
- On HP=0: the generator is removed and awards score. No area-of-effect
  damage in v1 (would change tactical balance and is not in the original).

### 3.7 Levels (the letter+number system)
Faithful homage to the A–Z / 1–9 selector. Stored as a server config preset:

- **Letter (A–Z)** controls qualitative behavior (all numeric parameters
  defined in `internal/sim/levels.go` and unit-tested):
  (LOS radii are MAZE_REVAMP.md ×2 with the finer grid; was-value in parens.)
  - A–F: snipes patrol slow, low accuracy, short LOS (≤ 12 tiles, was 6), no lead.
  - G–M: medium speed/accuracy, LOS 16 tiles (was 8), lead = ½ of computed lead.
  - N–S: high accuracy, LOS 20 tiles (was 10), full lead shots, snipe fire
    cooldown -25 %.
  - T–Z: LOS 28 tiles (was 14), generators HP 5, snipe speed +25 %.

  (Original Snipes also had "diagonal shots bounce off walls" as a
  letter-controlled flag. v1 does **not** implement projectile ricochet;
  it is a v1.1 candidate. If/when added, the bounce rule will be a swept
  segment reflected about the impacted axis-aligned wall normal, despawn
  after 1 bounce.)
- **Number (1–9)** controls quantitative scaling:
  - Max concurrent snipes overall (`max_snipes_total`): `n × 6`
    (1 → 6, 9 → 54). This is the *only* snipe-count cap; there is no
    per-generator alive cap (see §3.5).
  - Initial generator count: `n + 2` (1 → 3, 9 → 11). Enough generators
    that even with their 4–8 s emission timers, the global cap is
    reachable (e.g. n=9: 11 generators emitting one every ~6 s saturates
    the 54-snipe cap within ~30 s).
  - Player lives: `max(1, 10 - n)` (1 → 9 lives, 9 → 1 life).

V1 ships **all 234 combinations** as derived from a small lookup table —
they're cheap parameter sets, not bespoke designs.

### 3.8 Win conditions and scoring

#### 3.8.1 End-reason enum and evaluation order

Each match end carries one **end-reason** enum value, encoded in
`MatchOver.reason` and in `Event.reason` for `match_end`. The server
evaluates these in **priority order at the end of every sim tick**;
the first that fires terminates the match and sets the reason. This
keeps the outcome deterministic even when multiple conditions become
true on the same tick.

| Value | Name | Fires when | Notes |
|---:|---|---|---|
| 0 | `PVE_COMPLETE` | All generators destroyed AND all live snipes destroyed AND ≥ 1 player still has ≥ 1 life | Most positive outcome — wins ties. |
| 1 | `LAST_STANDING` | Exactly 1 player has ≥ 1 life AND the match started with ≥ 2 players | The survivor wins regardless of score. |
| 2 | `ALL_ELIMINATED` | Every player has 0 lives | No winner. Covers simultaneous-final-death. |
| 3 | `TIMER` | `server_tick ≥ match_end_tick` (default 10-minute hard timer) AND none of the above fired this tick | Falls back to score-ranking. |
| 4 | `SERVER_ERROR` | Match-actor panic recovered (§11) | Best-effort `MatchOver{reason: SERVER_ERROR}`; no winner. |

Priority means PvE > LAST_STANDING > ALL_ELIMINATED > TIMER > SERVER_ERROR.
Examples:
- All gens destroyed on the same tick the timer expires → `PVE_COMPLETE`.
- All players die on the same tick the timer expires → `ALL_ELIMINATED`.
- Last two players kill each other simultaneously and snipes remain →
  `ALL_ELIMINATED` (no `LAST_STANDING`, since there is no survivor).

#### 3.8.2 Solo matches

Solo (1-player) matches can only end with `PVE_COMPLETE`, `ALL_ELIMINATED`
(when the sole player's lives reach 0), `TIMER`, or `SERVER_ERROR`. The
`LAST_STANDING` rule requires a starting player count ≥ 2 and is
inapplicable to solo matches by construction.
- **Score per player:**
  - Snipe killed: +1
  - Generator destroyed: +10
  - Player killed (PvP): +25
  - Death penalty: −5
- **Winner selection** (one rule per end reason — no blanket override):
  - **PvE objective complete:** all players who finished with ≥ 1 life
    remaining are winners. Among them, scoreboard order is by score
    descending; ties allowed (joint top-of-board). Eliminated players
    are ranked below winners regardless of score.
  - **All players eliminated:** no winner (`winner = 0` in `MatchOver`).
    Scoreboard still orders all players by score for display.
  - **Last-player-standing:** the surviving player wins regardless of
    score (matches the original Snipes PvP feel: outliving everyone is
    the win, even if you camped). Scoreboard orders the rest by score.
  - **Match timer:** the player with the highest score wins; ties
    allowed (joint winners). Eliminated players are eligible (they
    might have racked up high scores before dying).

### 3.9 Lives, death, respawn
- A player starts with N lives (per level number).
- On death: a 3-second respawn timer, then respawn at the safest available
  `SPAWN_PLAYER` tile (max distance from nearest live entity hostile to
  player). Brief 2-second spawn invulnerability + can't shoot during it.
- When lives reach 0, the player enters **dead-cam mode** for the remainder
  of the match: a free-flying camera over the maze, with chat enabled but
  no movement, no fire, and no entity body. Distinct from the v1.1
  "live spectator" feature (§1.2): dead-cam is only available to
  players who actually played this match.

### 3.10 Input bindings

V1 ships two presets. Default is **Classic** (faithful to the original
Snipes/`nsnipes`); **Modern** is an alternative for new players. All keys
are individually rebindable; bindings persist in `localStorage`.

#### 3.10.1 "Classic" preset (default)
- **Movement:** arrow keys (`↑` N, `↓` S, `←` W, `→` E). Holding two
  adjacent arrows yields diagonal movement (NE/SE/SW/NW).
- **Shoot:** **W A S D** fire in cardinal directions. Holding two adjacent
  fire keys yields a diagonal shot (e.g. `W+D` → NE). Holding one fire key
  while moving fires repeatedly subject to the §3.4 cooldown.
- **Turbo:** **Space** (hold).
- **Match chat:** `T` opens, `Enter` sends, `Esc` cancels.
- **Pause/menu:** `Esc` (in-match menu only; doesn't pause the simulation).

#### 3.10.2 "Modern" preset (opt-in)
- **Movement:** **W A S D** (with diagonals via adjacent-key combos).
- **Shoot:** arrow keys, same diagonal-by-combo rule.
- **Turbo:** **Shift** (hold).
- Chat / menu identical to Classic.

#### 3.10.3 Conflict resolution
- Movement and shoot key sets must not overlap; the settings UI prevents it.
- A single key cannot be bound to both fire-N and move-N; rebind UI rejects
  conflicts. The default presets satisfy this trivially.

---

## 4. Technical architecture

### 4.1 Topology

```
┌──────────────┐     WebSocket (JSON)     ┌────────────────────┐
│   Browser    │ ───────lobby────────────▶│                    │
│   client     │                          │  Lobby/room server │
│  (TS/Canvas) │ ◀──── lobby events ──────│  (Go, in-process)  │
└──────┬───────┘                          └──────────┬─────────┘
       │                                             │
       │  WebSocket (binary frames)                  │  In-process
       │     in-match traffic                        │  handoff
       ▼                                             ▼
       └──────────────▶ ┌──────────────────────────────┐
                        │  Game server (Go)            │
                        │  - 1 goroutine per match     │
                        │  - 30 Hz authoritative sim   │
                        │  - 15 Hz snapshot fanout     │
                        └──────────────────────────────┘
```

- **Single binary**, single process. Lobby and game live together; a match
  is a goroutine + a channel-driven actor with its own deterministic loop.
- Static client assets (HTML/JS/CSS/PNG) are served by the same binary at
  `/` from an embedded filesystem (`//go:embed`). No CDN required.
- Horizontal scale (later): a stateless lobby tier that routes players
  to one of N game-server nodes via a header/handle. Out of scope for v1.

### 4.2 Repository layout

```
isnipes/
├── go.mod
├── cmd/
│   └── isnipes/                # main: starts HTTP/WS, lobby, embed client
│       └── main.go
├── internal/
│   ├── sim/                    # deterministic simulation core (no I/O)
│   │   ├── maze.go
│   │   ├── entity.go
│   │   ├── physics.go
│   │   ├── combat.go
│   │   ├── ai.go
│   │   ├── levels.go
│   │   ├── snapshot.go
│   │   └── sim_test.go
│   ├── proto/                  # wire schema (binary + JSON)
│   │   ├── proto.go
│   │   └── proto_test.go
│   ├── net/                    # WebSocket transport, framing, sequencing
│   │   ├── conn.go
│   │   ├── server.go
│   │   └── net_test.go
│   ├── match/                  # match actor: bridges sim + net
│   │   ├── match.go
│   │   └── match_test.go
│   ├── lobby/                  # room/lobby server
│   │   ├── lobby.go
│   │   └── lobby_test.go
│   └── observ/                 # logs, metrics, pprof endpoints
├── web/
│   ├── index.html
│   ├── src/
│   │   ├── main.ts
│   │   ├── render.ts
│   │   ├── input.ts
│   │   ├── netClient.ts
│   │   ├── prediction.ts
│   │   ├── lobby.ts
│   │   └── proto.ts            # mirror of internal/proto schema
│   ├── tests/
│   │   ├── prediction.test.ts
│   │   └── render.golden.test.ts
│   ├── package.json
│   ├── tsconfig.json
│   └── vite.config.ts
├── testdata/
│   ├── mazes/                  # golden mazes for seed regression tests
│   └── replays/                # recorded input streams + expected ticks
├── scripts/
│   ├── load_test.go            # synthetic-client load harness
│   └── replay.go               # CLI replay tool
├── .github/workflows/ci.yml
└── SPEC.md                     # this file
```

### 4.3 Wire protocol

#### 4.3.1 Lobby messages (JSON over WS)

Single envelope:
```json
{ "t": "<type>", "v": 1, "d": { ...payload... } }
```

| Type | Direction | Purpose |
|---|---|---|
| `hello` | C→S | { nick, clientVersion, schemaChecksum } — initial handshake. `schemaChecksum` is the client's compiled-in `u32` value. The server compares against its own; mismatch is rejected here with a JSON `error{code: VERSION}` before any match WS is opened. |
| `welcome` | S→C | { playerId, serverVersion, schemaChecksum, motd }. `schemaChecksum` is echoed back so the client can sanity-check (and so the client knows what to send in the first `MatchJoin` binary frame, §4.3.2). |
| `roomList` | S→C | array of rooms (id, name, players, max, mode, level, state). |
| `createRoom` | C→S | { name, max, level }. |
| `joinRoom` | C→S | { roomId }. |
| `leaveRoom` | C→S | {}. |
| `chat` | C↔S | { roomId, text }. |
| `startMatch` | C→S | host only; { roomId }. |
| `matchStarted` | S→C | { matchId, gameSocketPath, tickRate, mapSeed, joinToken }. `joinToken` is the per-player credential required by `MatchJoin` (§4.7); each member of the room receives a distinct token. |
| `error` | S→C | { code, message }. |

#### 4.3.2 In-match messages (binary frames)

Tiny, hand-rolled little-endian binary format. **Reason:** msgpack/protobuf
are fine but a hand-rolled schema for ~10 message types is smaller, faster,
trivially fuzz-testable, and removes a third-party dependency from the
hot path. The schema is described in `internal/proto/proto.go` and mirrored
in `web/src/proto.ts`; both sides include a generated checksum for the
schema version and reject mismatches.

Frame: `[u8 type][u8 flags][u16 seq][u16 ack][u16 len][bytes payload]`.

| Type | Dir | Payload |
|---|---|---|
| `MatchJoin` (0x00) | C→S | `u32 schema_checksum, u8 token_len, bytes token` — first frame after the match-WS upgrade. `schema_checksum` matches the value the server published at `/version` (and in the lobby `welcome` reply); a mismatch yields a `Close{code: 4002, reason: "VERSION"}` (§4.3.5). The `token` is the join token issued by the lobby (§4.7). |
| `Input` (0x01) | C→S | `u16 client_tick, u8 dir, u8 turbo, u8 fire_dir` — `dir` and `fire_dir` use the **Dir8 enum** below (`0` = idle/no-fire; `1`–`8` = N, NE, E, SE, S, SW, W, NW in clockwise order). `turbo` is `0` or `1`. |
| `Snapshot` (0x02) | S→C | `u32 server_tick, u16 your_last_input_tick, u32 your_entity_id, u8 entity_count, [Entity]×n` |
| `EntityDelta` (0x03) | S→C | small delta from baseline (v1.1 optimization; not used in v1). |
| `Event` (0x04) | S→C | `u8 kind, u32 actor, u32 target, u8 reason` — kind enum below. |
| `Chat` (0x05) | C↔S | `u8 len, utf8 text` |
| `Ping` (0x06) | C↔S | `u32 ts_origin` — sender's local monotonic ms. Either side may initiate. |
| `Pong` (0x07) | C↔S | `u32 ts_origin, u32 ts_responder` — `ts_origin` echoes the initiator's value verbatim; `ts_responder` is the responder's local monotonic ms at send time. |
| `MatchOver` (0x08) | S→C | `u32 final_tick, u8 reason, u32 winner_id_or_0, u8 entry_count, [u32 player_id, i32 score, u8 lives_remaining]×n`. `reason` uses the §3.8.1 enum (`0=PVE_COMPLETE`, `1=LAST_STANDING`, `2=ALL_ELIMINATED`, `3=TIMER`, `4=SERVER_ERROR`). `winner_id` is `0` when there is no single winner (i.e. for `ALL_ELIMINATED`/`SERVER_ERROR`, and for ties on `PVE_COMPLETE`/`TIMER` — clients render the scoreboard for ties). |
| `MapInit` (0x09) | S→C | One-shot at match start (and on reconnect resync). `u32 seed, u16 width, u16 height, u8 packing, bytes packed_tiles` — see §4.3.4. |
| `Resync` (0x0A) | S→C | `u32 server_tick` — sent immediately before a `MapInit`/`Snapshot`/`Scoreboard` triple on reconnect. Tells the client to drop its prediction buffer. |
| `Scoreboard` (0x0B) | S→C | `u32 server_tick, u8 entry_count, [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]×n` — the authoritative per-player HUD state. Sent (a) immediately after `Snapshot` on reconnect/resync, (b) on every change to any player's lives or score, rate-limited to ≤ 5 Hz, and (c) once on initial `MatchJoin` so the HUD renders from frame 1. |

`Entity` = `u32 id, u8 kind, u8 hp, u8 facing, u8 flags, i32 x, i32 y, i16 vx, i16 vy`. `facing` uses the **Dir8 enum** (`1`–`8` only; entities always face a direction). `flags` bit 0 = `DEAD`, bit 1 = `SPAWN_INVULN`, bit 2 = `TURBO`, bits 3–7 reserved.

**Dir8 enum** (used by `Input.dir`, `Input.fire_dir`, `Entity.facing`):

| Value | Direction |
|---:|---|
| 0 | `IDLE` / `NO_FIRE` (only valid for `Input` fields, never `facing`) |
| 1 | N |
| 2 | NE |
| 3 | E |
| 4 | SE |
| 5 | S |
| 6 | SW |
| 7 | W |
| 8 | NW |

Any other value is a protocol error and rejects the frame.

The Snapshot's `your_last_input_tick` is **the client's own** monotonic
input tick that the server most recently consumed for this player; it is
how the client knows what to replay (§4.4). `your_entity_id` lets the
client locate itself in the entity list without name/string matching.

**Event `kind` enum** (full enumeration for v1):

| Value | Name | `actor` | `target` | `reason` |
|---:|---|---|---|---|
| 0x01 | `entity_spawn` | spawner (gen / lobby / 0) | entity | 0 |
| 0x02 | `entity_hit` | shooter | victim | 0 = body, 1 = grazing |
| 0x03 | `entity_kill` | killer (0 if env) | victim | 0 = projectile, 1 = collision, 2 = timeout |
| 0x04 | `generator_destroyed` | killer | generator | 0 |
| 0x05 | `player_join` | 0 | player | 0 |
| 0x06 | `player_leave` | 0 | player | 0 = clean, 1 = timeout, 2 = kicked |
| 0x07 | `player_dc` | 0 | player | 0 (grace started) |
| 0x08 | `player_rejoin` | 0 | player | 0 (reconnected within grace) |
| 0x09 | `match_starting` | 0 | 0 | seconds remaining |
| 0x0A | `match_started` | 0 | 0 | 0 |
| 0x0B | `match_end` | winner_or_0 | 0 | Uses the §3.8.1 end-reason enum (`0=PVE_COMPLETE`, `1=LAST_STANDING`, `2=ALL_ELIMINATED`, `3=TIMER`, `4=SERVER_ERROR`). Always followed by a `MatchOver` frame with the same `reason`. |
| 0x0C | `chat_relay` | sender | 0 | 0 = lobby, 1 = match, 2 = team (future) |
| 0x0D | `respawn_pending` | 0 | player | ticks until respawn |

Clients ignore unknown `kind` values to allow forward-compatible additions.

#### 4.3.3 Rates and reliability

- **Sim tick:** 30 Hz (33.3 ms).
- **Client → server inputs:** up to 30 per second, one per sim tick.
  WebSocket/TCP is reliable-ordered, so duplicates can't happen, but
  sequence numbers let the server cheaply discard frames that arrive out
  of intended order after a `Resync`.
- **Server → client snapshots:** **15 per second** — one snapshot every
  **second** sim tick (every 66.7 ms). Within a snapshot, only entities
  inside the player's Area-of-Interest are sent (see §5.3 for limits).
- **Client interpolation buffer:** **100 ms** behind real-time (≈ 1.5
  snapshot intervals). Tuned to absorb one missed snapshot or 30 ms of
  jitter without visible stutter.
- **Pings (bidirectional):** **both** server and client emit `Ping` at
  **2 Hz** (every 500 ms) using their own monotonic ms clock. The receiver
  immediately echoes a `Pong` carrying the original `ts_origin` plus its
  own `ts_responder`. Each side computes its own
  `RTT = now - ts_origin_in_my_recent_Pong`, then
  `OWT_estimate = RTT / 2` (filtered EWMA, α = 0.2). The server uses its
  per-connection OWT for the lag-compensation rule in §3.4; the client
  uses its own OWT for latency display and for sizing its prediction
  buffer.
- **Dead-connection detection:** a side treats the peer as dead if it has
  received **no frame of any type** for 5 seconds (i.e. ping is sufficient
  to keep alive; no need to track Pongs specifically). On the server,
  that timer fires the §4.7.1 DC-grace; on the client, it triggers a
  reconnect attempt with the same `joinToken`.

WebSocket/TCP gives us reliable-ordered delivery; we do **not** rely on
out-of-order or dropped frame semantics. App-level snapshot drop scenarios
(if a snapshot would exceed the AOI cap, server may omit far entities)
are explicitly documented where they apply (§5.3 / §11), and tests for
those paths are tagged `synthetic` (§9).

#### 4.3.4 Map transfer format

The maze is derived from `(seed, width, height, level_letter, level_number)`
but the **authoritative** tile data is sent explicitly to clients so the
client never has to re-run map generation (and so map-gen changes don't
silently desync replays).

- `MapInit` payload: `u32 seed, u16 width, u16 height, u8 packing,
  bytes packed_tiles`. The byte length of `packed_tiles` is derived from
  the frame header's `u16 len` minus the fixed-prefix size (9 bytes).
  This matches the row in §4.3.2 exactly; no separate length field.
- `packing = 1` (v1): 2 bits per tile, little-endian within each byte.
  Tile codes: `0=WALL, 1=FLOOR, 2=SPAWN_PLAYER, 3=SPAWN_GENERATOR`.
- For a 60 × 40 map: 2400 tiles × 2 bits = 600 bytes. Max v1 map
  (120 × 80) is 2400 bytes — both well under the 64 KiB frame ceiling.
- **No compression in v1.** Maps are small enough to send raw; deflate
  would save a handful of bytes at the cost of an inflation step before
  parsing. Compression may be added in v1.1 as a new `packing` value
  (e.g. `packing = 2 = deflated`) so the framing rule stays unambiguous
  — the whole `packed_tiles` segment would be deflated, with the fixed
  prefix always uncompressed.

#### 4.3.5 In-match error reporting

The in-match binary protocol does **not** define an `Error` frame. Errors
on the match WS are reported by **closing the WebSocket** with one of
the following codes (RFC 6455 application range = 4000–4999), with the
`reason` field carrying a short text label:

| Close code | Reason label | Triggered by |
|---:|---|---|
| 1000 | `"ok"` | Clean end-of-match closure after `MatchOver`. |
| 4001 | `"AUTH"` | Missing / invalid / expired `joinToken` (§4.7). |
| 4002 | `"VERSION"` | `MatchJoin.schema_checksum` does not match the server. |
| 4003 | `"MALFORMED"` | A binary frame failed schema/length validation. |
| 4004 | `"FULL"` | Match has reached the player cap. |
| 4005 | `"NOT_FOUND"` | `matchId` is unknown / already ended. |
| 4006 | `"SERVER_ERROR"` | Match goroutine panic; see §11. |
| 4007 | `"IDLE"` | No frame received from peer for 5 s; see §11. |

JSON `error{code, message}` payloads remain the mechanism on the **lobby**
WS (which is JSON-only); they are never used in-match.

#### 4.3.6 Tick numbering, sequence numbers, wraparound

- `server_tick` is `u32`; at 30 Hz it wraps after ~4.5 years and never
  matters within a single match.
- `client_tick` in `Input`/`Snapshot.your_last_input_tick` is `u16` and
  wraps every ~36 minutes. Compare with **modular** arithmetic:
  `(a - b) & 0xFFFF` cast to `i16` gives signed distance. Both sides agree
  that values within 256 of the latest are "live" and older are stale.
- `seq` and `ack` in the frame header are `u16` over the WS stream and
  used only for diagnostics/metrics (TCP makes them redundant for
  ordering); they wrap with the same modular rule.

### 4.4 Client prediction & reconciliation

Standard quake-style:
1. Client samples input each tick, applies it locally to its own player,
   stores `(client_tick, input, predicted_state)` in a ring buffer (capacity
   1 s = 30 entries).
2. Client sends the `Input` frame to server tagged with `client_tick`.
3. Server simulates authoritative tick, sends `Snapshot` containing
   `your_last_input_tick` (the per-recipient field added in §4.3.2).
4. On snapshot receipt the client looks up its buffered prediction for
   that input tick. If the server's reported position diverges beyond a
   threshold (e.g. > 4 subtile units), replay all buffered inputs from
   `your_last_input_tick + 1` to "now" to converge.
5. **Other** entities (non-self): client **interpolates** between the two
   most recent snapshots at a target time of `now - 100 ms` (~1.5 snapshot
   intervals at 15 Hz) for smooth rendering and jitter absorption.

### 4.5 Anti-cheat baselines (v1)

- All movement clamped server-side: max speed, wall collisions, no
  teleporting. Input sequence numbers must monotonically increase; out-of-
  order or duplicate inputs are dropped.
- Fire rate is server-clocked; spurious extra fires from a client are
  silently dropped.
- Score and HP are server-only; client never sends them.
- No client-side "I killed X" — kills are derived from server hit detection.

### 4.6 Determinism

- All simulation math is **integer** in subtile units; no floating point in
  the sim. Trig precomputed as a small lookup table for 8 directions.
- Single PRNG (`math/rand/v2` seeded from match seed) used in a fixed
  consumption order: maze gen → entity placement → AI decisions tagged
  with a per-entity stream so adding an entity doesn't shift later draws.
- Tick loop is fixed-step; no wall-clock drift inside the sim.
- Replay = `(seed, ordered list of inputs)`. Re-running yields identical
  per-tick state. Verified by replay tests.

### 4.7 Match-join token & reconnect

Each `matchStarted` lobby reply carries a per-player `joinToken` (16 random
bytes, base64-encoded). The client presents it as the first frame on the
match WS via `MatchJoin` (§4.3.2). Tokens are:

- **Single-match scoped:** tied to one `matchId` and one `playerId`;
  invalid for other matches.
- **Short-lived (initial use):** 60-second TTL between issuance and the
  client's first `MatchJoin`. Unused tokens expire and free the slot.

#### 4.7.1 Reconnect — single model, used by §8 Phase 5 and §11

When a match WS drops (FIN, RST, ping timeout, backpressure-induced drop),
the player's slot enters **DC-grace** for exactly **30 seconds**:

- During DC-grace: the player's entity remains in the world but applies
  no new inputs. Velocity is zeroed on the next tick (the player "stops
  in place"). The entity is still targetable and can be killed; HP and
  lives continue to apply. An `Event{kind: player_dc}` is broadcast.
- **Reconnect within 30 s** (presenting the same `joinToken` on a fresh
  match WS): server accepts, re-binds the connection to the existing
  entity, sends `Resync → MapInit → Snapshot → Scoreboard`, and
  broadcasts `Event{kind: player_rejoin}`. All client-visible state
  (position, HP, lives, score, every other player's HUD entries) is
  preserved exactly.
- **At 30 s with no reconnect:** the slot is **terminated**. Entity is
  removed with `Event{kind: player_leave, reason: timeout}`. The
  `joinToken` is invalidated. Any later `MatchJoin` with that token is
  rejected by closing the match WS with `Close{code: 4001, reason:
  "AUTH"}` (§4.3.5). The freed slot may be filled by a new player only
  if the match is still in a join-accepting state (which it is not during
  `PLAYING` — late-join is rejected per Phase 5).

There is **no AI-AFK state**; v1 keeps the reconnect contract intentionally
simple. Bots are a v1.1 candidate (§13).

#### 4.7.2 Identity

Auth is **token-based, not nick-based**: a malicious client cannot hijack
another player's slot by claiming the same nick. Nicks are display-only.

---

## 5. Server: game-server design

### 5.1 Process model

- One `main` goroutine running `http.Server` on `:8080` (configurable).
- One `lobby` goroutine driving the lobby actor.
- One **`match` goroutine per active match**, owning all state. All other
  goroutines (network readers) post messages to it via a buffered channel.
  This is the actor model — no shared mutable state, no mutexes around
  game state.
- Per-connection: 1 reader goroutine, 1 writer goroutine, both speaking
  to the lobby actor or the match actor via channels.

### 5.2 Match actor loop (pseudocode)

```go
func (m *Match) Run() {
    ticker := time.NewTicker(33333 * time.Microsecond) // 30 Hz sim
    defer ticker.Stop()
    for {
        select {
        case msg := <-m.in:
            m.handleControl(msg) // join/leave/input/chat
        case now := <-ticker.C:
            m.tick(now)                         // sim.Step()
            if m.tickIndex%2 == 0 {             // every 2nd tick → 15 Hz
                m.broadcastSnapshot()
            }
            if m.isOver() {
                m.broadcastMatchOver()
                return
            }
        }
    }
}
```

Snapshot rate is locked at **15 Hz** for v1 (see §4.3.3). All bandwidth,
interpolation, and lag-compensation numbers in this spec are derived from
that choice; do not change it without updating §0, §4.3.3, §4.4, §7.3,
and §15.

### 5.3 Resource limits

- Max concurrent matches: 64 per binary (configurable via `--max-matches`).
- Max players per match: 16 (configurable; UI defaults to 8).
- Max snipes per match: 54 (the level-9 ceiling per §3.7). At lower
  level numbers the cap is correspondingly smaller.
- Max projectiles in flight per match: 64 (rejects excess on fire).
- Max entities per snapshot: **64 entries** (`u8 entity_count`'s effective
  cap once we leave headroom).
- Per-connection send buffer: 64 frames; if full, drop the connection
  rather than back-pressure the match loop (see §11).

#### 5.3.1 AOI selection — precedence order

When more entities exist than fit in the 64-entity cap, the server
builds the snapshot's entity list in this exact priority order, stopping
when the cap is reached:

1. **Self** — the recipient's own player entity (or none, if in dead-cam).
2. **Other players** — by distance ascending, all included if possible.
3. **Recipient's own projectiles** — all included regardless of distance.
4. **Projectiles within 20 tiles** of the recipient — distance ascending.
5. **Generators within 40 tiles** — distance ascending.
6. **Snipes within 40 tiles** — distance ascending.
7. **Other entities** (further-out projectiles, snipes, generators) by
   distance ascending until the cap is reached.

(AOI radii are the MAZE_REVAMP.md ×2 values, scaled with the finer 120×80 grid;
pre-revamp they were 10 / 20 / 12 / 22 tiles.)

A **hysteresis** rule applies for entities at the boundary: an entity
included in the previous snapshot stays included until it crosses the
*outer* boundary (e.g. 24 tiles for projectiles, 44 tiles for snipes/
generators). This prevents oscillation when an entity hovers near the
threshold.

Dead-cam clients receive an **unfiltered** snapshot (§3.9 / Phase 5):
priorities 1–6 collapse to "everyone", priority 7 fills any remaining
cap by distance from the *map center*. No camera coordinate is sent
server-side.

The 64-entity cap is high enough that under normal v1 match sizes
(≤ 8 players + 9 generators + ~54 snipes peak + ≤ 16 projectiles ≈
≤ 87 entities) the cap binds only briefly during snipe storms; the
priority order ensures that the binding case still always shows the
recipient's local combat situation.

### 5.4 Configuration

- `--addr=:8080`, `--max-matches`, `--max-players-per-match`, `--motd`,
  `--log-level`, `--enable-pprof`, `--require-tls`.
- Env vars mirror flags (`ISNIPES_ADDR`, etc.).

### 5.5 Observability

- Structured JSON logs (Go `log/slog`).
- `/metrics` Prometheus endpoint: matches active, players online, ticks/s,
  snapshot bytes/s, dropped frames, rejected inputs.
- `/debug/pprof/*` mounted behind `--enable-pprof`.
- Per-match optional **replay recording** to `./replays/<matchid>.bin`
  when `--record` is set, for post-hoc bug repro.

---

## 6. Server: lobby/room-server design

### 6.1 Concepts

- **Room:** a pre-match lobby. Has an id, a host (the creator), a name,
  a max player count, a level (A–Z, 1–9), and a state
  (`OPEN`, `STARTING`, `IN_MATCH`, `CLOSED`).
- **Player session:** a connected websocket with a nick. Sessions outlive
  matches; players return to the lobby after a match ends.

### 6.2 Flow

1. Client connects to `/ws/lobby` → sends `hello{nick, clientVersion,
   schemaChecksum}` → server compares the checksum to its own and, on
   mismatch, replies JSON `error{code: VERSION}` and closes; on match,
   server replies `welcome{playerId, serverVersion, schemaChecksum,
   motd}` followed by the current `roomList`.
2. Client either `createRoom` (becomes host) or `joinRoom`.
3. Host `startMatch` → server creates a Match actor, mints a unique
   `joinToken` per room member, and replies `matchStarted{matchId,
   gameSocketPath, tickRate, mapSeed, joinToken}` to **each member
   individually** (every reply carries that member's own token).
4. Each client opens a second WS to the match endpoint (a separate
   WebSocket for clarity in v1) and sends `MatchJoin{schema_checksum,
   token}` as the first binary frame.
5. On match end, server emits `MatchOver` on the match WS, then closes
   the match WS cleanly (close code `1000`). The lobby WS stays open
   so the client returns to the room list.

### 6.3 Rules

- Nicks are unique-per-session, 1–16 chars, `[A-Za-z0-9_-]`. Server
  appends `#nnn` if a duplicate joins.
- Room IDs are 6-char base32. Joinable directly via URL
  `?room=ABCD23` so users can deep-link.
- Server enforces max concurrent rooms; if exceeded, `createRoom` errors.
- Inactivity: rooms with no members are GC'd after 30 s; matches with
  zero connected players end after 30 s.

---

## 7. Client: browser app design

### 7.1 Stack

- **TypeScript** + **Vite** for the build.
- **HTML5 Canvas 2D** for rendering. WebGL not needed at this scale
  (≤ 2,400 tiles + ≤ ~80 entities visible).
- No game engine framework (Phaser etc.) for v1 — keep dependencies
  minimal and behavior predictable.
- `vitest` for unit tests; `@playwright/test` for browser e2e.

### 7.2 Modules

- `main.ts` — bootstraps, owns the top-level state machine
  (`LOBBY` / `IN_MATCH` / `POST_MATCH`).
- `lobby.ts` — lobby UI (room list, create/join, chat).
- `netClient.ts` — WS connection, framing, sequence numbers, ping/pong.
- `prediction.ts` — input ring buffer, replay-on-correction.
- `render.ts` — canvas drawing. Camera follows the local player; smooth
  interpolation buffer for non-self entities.
- `input.ts` — keyboard handling (with rebinds in localStorage).
- `proto.ts` — pack/unpack binary frames; schema-checksum check on connect.
- `audio.ts` — small library: shoot, hit, generator-down, death, spawn.

### 7.3 Render pipeline (per frame at 60 FPS)

1. Read latest interpolated state at `t = now - 100 ms` (lerp between the
   two most recent snapshots).
2. Override the local player with predicted state from the prediction
   buffer (no interpolation lag for self).
3. Clear canvas, draw visible portion of the map (`OffscreenCanvas` cache
   of the static map; only redraw on map change).
4. Draw entities (sorted by `y` for trivial pseudo-depth).
5. Draw projectiles (short trails for legibility).
6. Draw HUD: HP / lives / score / minimap / chat overlay.
7. Schedule next `requestAnimationFrame`.

### 7.4 Asset set (v1, minimal)

- Tile sprites: `wall.png`, `floor.png`, `floor_alt.png` (32×32 px).
- Entity sprites: `player.png` (4-frame walk × 8 dirs = 32 frames sheet),
  `snipe.png` (similar), `generator.png` (static + 3 damage states),
  `projectile.png`.
- A tiny pixel font for the HUD (subset of `IBM VGA 8×16`-style).
- Sounds (≤ 50 KB each) generated with `jsfxr` for v1.

### 7.5 Accessibility

- Color-blind palette toggle (no critical info conveyed by red/green
  alone; HP also indicated by a number).
- All controls rebindable.
- High-contrast mode that thickens entity outlines.

---

## 8. Implementation phases

Each phase has a **definition of done** = its tests pass under `go test ./...`
(server) and `pnpm test` (client) in CI, plus a manual smoke test.

### Phase 0 — Project scaffolding (1–2 days)

**Build:**
- `go mod init`, `cmd/isnipes/main.go` skeleton that prints version.
- `web/` scaffold via Vite + TS.
- GitHub Actions CI: `go vet`, `go test`, `pnpm test`, `pnpm build`.
- `.editorconfig`, `gofmt`/`gofumpt`, `eslint`+`prettier`.
- `Makefile` or `Taskfile.yml` with `make dev` (runs server + client dev
  servers concurrently), `make test`, `make build`.

**Tests (gate):**
- CI runs and is green on an empty `TestSmoke` in both repos.
- `make build` produces a binary that serves `/healthz`.

### Phase 1 — Deterministic simulation core (1–2 weeks)

**Build (`internal/sim`):**
- Maze generation (growing-tree, room carving, doorways, generator and
  spawn placement).
- Fixed-point physics: movement, swept-AABB wall collision, sliding.
- Entity store (slab allocator) and tick loop.
- 8-direction input application; turbo.
- Projectile spawning, travel, expiry, wall hits, entity hits.
- Damage, death, simple respawn timer.

**Tests:**
- **Maze gen — golden seeds:** for 8 fixed seeds, the generated maze
  matches a hash committed in `testdata/mazes/<seed>.hash`. Re-runs are
  identical across OS/arch.
- **Connectivity:** every `FLOOR` is reachable from every `SPAWN_PLAYER`
  (BFS).
- **Movement:** moving into a wall doesn't pass; moving along a wall
  slides; diagonal speed normalized within 1 subtile-unit/tick of cardinal.
- **Projectile:** shot from `(x,y)` in direction `d` arrives at expected
  cell after expected tick; impacts the right entity in a head-on test.
- **Determinism:** running 1,000 ticks twice with same seed and same input
  list yields byte-identical entity state.
- **Property tests:** for random seeds, no entity ever ends a tick inside
  a `WALL`.

### Phase 2 — Headless server + minimal client + **minimal lobby** + **basic multiplayer match** (2 weeks)

The user's prompt asked specifically for a room/server component "for
players to meet and to start games". This phase ships an honest
end-to-end playable multiplayer milestone — two or more browsers can
meet in a room, start a match, and shoot each other in the maze.

What is **in** Phase 2:
- Maze drawn from the authoritative `MapInit`.
- N players (up to 8) walking, turbo-ing, and shooting projectiles
  authoritatively on the server.
- PvP-only damage: a projectile kills a player on hit; killed players
  are removed for the rest of the match (no respawn / no lives system
  yet — that's Phase 5).
- A trivial match-end condition: one player remaining (or all dead) →
  emit `MatchOver`. (Full scoring rules come in Phase 5.)
- Minimal lobby: handshake, create/join one room, host `startMatch`.
- `joinToken` issuance and `MatchJoin` validation.

What is explicitly **deferred**:
- Snipes and generators (Phase 3).
- Client prediction (Phase 4).
- Lives, respawn, dead-cam, scoring, end-of-match scoreboard (Phase 5).
- DC-grace and reconnect (Phase 5).
- Room list, chat, level selector, deep-link (Phase 6).

**Build:**
- `internal/net`: WS upgrade, binary frame codec, per-conn reader/writer.
- `internal/proto`: schema + version checksum; encoders for `MatchJoin`,
  `Input`, `Snapshot`, `Event` (subset: `entity_spawn`, `entity_kill`,
  `player_join`, `match_end`), `MatchOver`, `MapInit`, `Scoreboard`,
  `Ping`, `Pong`. (`EntityDelta`, `Chat`, `Resync` are deferred to
  later phases. `Scoreboard` is needed in Phase 2 because the HUD must
  render player nicknames at match start; lives/score are zero until
  Phase 5 wires up real values.)
- `internal/match`: match actor wires sim to network; supports 2–8
  players concurrently with PvP-only combat.
- `internal/lobby`: minimal subset only — handshake, create/join one
  room, `startMatch` emits `matchStarted{... joinToken}` per member.
- Client: connect to lobby, create-or-join, on `matchStarted` open the
  match WS with the token, render maze, render all entities from
  snapshots, send input. No prediction yet — pure snapshot display.

**Tests:**
- **Codec:** round-trip every implemented message type via
  `testing/quick` fuzz. Schema-checksum mismatch is rejected.
- **Match actor unit:** match with N=2 simulated clients, sending
  scripted input frames, produces snapshots whose `your_entity_id` is
  per-recipient and whose entity positions match a fixture.
- **PvP kill:** scripted input streams where player A's projectile hits
  player B produces an `entity_kill` event; B is absent from subsequent
  snapshots; with no players remaining alive the match emits
  `MatchOver`.
- **`MatchJoin` auth:** valid token → admitted. Wrong / expired / missing
  token → WS closed with `Close{code: 4001, reason: "AUTH"}`. Stale
  `schema_checksum` → WS closed with `Close{code: 4002, reason: "VERSION"}`.
- **Minimal lobby unit:** two mock connections; create + join + start →
  both receive `matchStarted` with the same `matchId` and a unique
  `joinToken` each; lobby rejects a second `createRoom` from the same
  player.
- **End-to-end integration (Go):** `httptest` server, three in-process
  WS clients complete lobby → match → 4-second shootout → `MatchOver`
  with the expected last-alive payload.
- **Client unit (vitest):** `unpackSnapshot` matches Go reference output
  for hard-coded byte streams across every implemented message type.
- **Browser e2e (Playwright):** two browser contexts on the same
  `httptest` server: one creates a room, the other joins, host starts,
  both render the maze with two entities, one moves and shoots, the
  other entity disappears within a few snapshots. Screenshot diff
  tolerance is broad here (≤ 5 % per-pixel, Chromium only); strict
  cross-browser golden diffs are Phase 7 / nightly.

### Phase 3 — Snipes & generators AI (1 week)

**Build:**
- Snipes state machine and BFS pathfinding on the tile grid.
- Generators: spawn timer, snipe emission, HP, destruction event.
- Level table (A–Z × 1–9) parameters applied to sim.

**Tests:**
- **AI unit:** snipe in an open room with a stationary player switches
  `PATROL → CHASE` within `LOS_RADIUS` ticks; stops chasing when LOS lost
  for ≥ 60 ticks.
- **Pathfinding:** BFS returns the optimal next-step on a hand-built maze
  fixture; recomputed at the configured cadence.
- **Generator:** at default rate, a generator emits exactly `N` snipes
  in `T` ticks before reaching its cap; emissions cease when destroyed.
- **Replay:** a recorded replay where 1 player kills 3 generators and 10
  snipes reproduces the same kill events on every run.

### Phase 4 — Client prediction & reconciliation (1–2 weeks)

**Build:**
- Client input ring buffer (1 s = 30 entries), prediction tick on local
  player.
- Reconcile-on-snapshot: replay buffered inputs from
  `your_last_input_tick + 1`.
- Interpolation buffer for non-self entities (~100 ms behind).
- Server-side lag compensation for hit detection: per-entity position
  history ring of **9 entries** retaining ticks `[T_now - 8, T_now]`,
  giving `LAG_COMP_TICKS = 8` rewind ticks (~267 ms) as defined in §3.4.
  (Note the asymmetry: the *client* input ring is 30 entries to cover
  replay over a worst-case ~1 s RTT; the *server* position history is
  9 entries to bound how far a shot can be rewound. They serve different
  purposes.)

**Tests:**
- **Prediction unit (client):** given a fake snapshot stream with a
  100 ms one-way delay, the local player's reported position is within
  1 tile of the eventual reconciled position; divergence is corrected
  within one snapshot.
- **Lag-compensation (server):** a shot fired by a player with 80 ms RTT
  hits a target that visually occupied the muzzle axis at that time —
  verified via a fixture stream of inputs and expected hits, using the
  `LAG_COMP_TICKS = 8` window from §3.4. Shots whose ideal rewind
  exceeds the window are tested at the oldest sample.
- **Realistic-network harness:** WebSocket/TCP is reliable-ordered, so
  app-level packet "loss" is not a real failure mode; tests focus on what
  actually happens on a real link:
  - **Latency:** 50 / 100 / 150 ms one-way constant delay; reconcile
    converges, no oscillation.
  - **Jitter:** Gaussian ±30 ms around a 100 ms median; interpolation
    buffer absorbs without visible stutter (≤ 1 dropped render frame /
    second under jitter).
  - **TCP stall:** insert a 500 ms read pause; on resumption, client
    catches up via reconciliation; no desync.
  - **Backpressure (drop only):** writer queue saturated → server drops
    the connection (per §11). Reconnect/resume behavior is **not** tested
    in Phase 4 — it depends on the reconnect machinery built in Phase 5
    and lives in that phase's test list.
- **Synthetic stress (tagged `synthetic`):** server intentionally omits
  snapshots / drops far-AOI entities; client still renders sensibly.
  Not gated as a CI failure — exists to validate behavior the rest of
  the spec hand-waves about.

### Phase 5 — Multiple-player match completeness (1.5 weeks)

This phase upgrades the multi-player match shipped in Phase 2 with the
full set of state transitions: scoring, lives, respawn, end-of-match,
disconnect/reconnect per §4.7.1, and **dead-cam mode** (§3.9).

**Build:**
- N-player late-join policy: allowed while a room is in `STARTING`,
  rejected during `PLAYING`.
- Per-player **lives, respawn timer, spawn invulnerability** (§3.9).
- **Dead-cam mode** for eliminated players: server marks the player
  entity removed, retains the WS connection, accepts only `Chat` and
  `Ping`/`Pong` inputs (any other in-match frame from this client is
  silently dropped), and delivers an **unfiltered** snapshot (no AOI
  trimming — dead-cam clients see the whole match, subject only to the
  64-entity cap in §5.3). The free-flying camera is purely client-side;
  the server has no per-client camera coordinate and does not need one.
  The `Snapshot.your_entity_id` field is `0` while a client is in
  dead-cam, signalling the client to use its local camera state.
- **Scoring** per §3.8: snipe/generator/player kill credits, death
  penalty, end-of-match `MatchOver` payload with the full scoreboard.
- **Disconnect / reconnect** wired to the model in §4.7.1:
  30 s DC-grace, freeze the entity, accept reconnect with same token,
  drop the slot at the 30 s mark.

**Tests:**
- **Match integration:** spin up an in-process server, connect 4 synthetic
  clients, run a scripted 30-second match (recorded inputs), assert
  scoreboard equals fixture and `MatchOver` payload is well-formed.
- **Lives / respawn:** a player with N starting lives entering dead-cam
  on the **Nth** fatal hit (each death decrements lives; reaching 0
  triggers dead-cam — see §3.9); respawn timer fires in the expected
  tick range after each non-final death; spawn invulnerability prevents
  same-tick re-kill.
- **Dead-cam:**
  - Eliminated player keeps receiving snapshots; `your_entity_id` is `0`.
  - Inputs other than `Chat` are rejected server-side while in dead-cam.
  - Match continues; remaining players' events still reach the dead-cam
    client; final `MatchOver` is delivered.
- **Disconnect within grace:** kill a client mid-match, reconnect within
  20 s of WS drop using the issued `joinToken`. The reconnecting client
  re-acquires its entity at exactly its pre-drop HP / lives / score;
  other clients see `player_dc` then `player_rejoin`.
- **Disconnect past grace:** drop and don't reconnect; at the 30 s
  boundary the slot is terminated, `player_leave{reason: timeout}` is
  emitted, and a follow-up `MatchJoin` with the same token is rejected
  with `Close{code: 4001, reason: "AUTH"}`.
- **Late join:** joining a room in `STARTING` works; joining a `PLAYING`
  match is rejected with a clear error.
- **AOI under load (PR-gated):** in a 60×40 fixture with 8 players + 50
  snipes + 30 projectiles (88 total entities; > 64-entity cap), every
  per-player snapshot satisfies the §5.3.1 priority order:
  (a) the recipient's own entity is always present;
  (b) the recipient's own projectiles are always present;
  (c) every projectile within 10 tiles of the recipient is present;
  (d) every other player is present (8 players ≪ 64);
  (e) at least one snipe and one generator within 20 tiles is present
      whenever any exist within that range;
  (f) the snapshot never exceeds the 64-entity cap;
  (g) hysteresis: an entity inside the inclusion range in tick T is still
      present in tick T+1 unless it has moved past the *outer* hysteresis
      threshold (12 tiles for projectiles, 22 tiles for snipes/generators).
- **Dead-cam AOI:** an eliminated player's snapshot contains *all*
  entities (subject to the 64-cap, ordered by distance from map center
  as a tiebreaker); `your_entity_id` is `0`.

### Phase 6 — Lobby polish & matchmaking (1 week)

Phase 2 already shipped a minimal lobby (create / join / start). Phase 6
brings it to the full feature set described in §6.

**Build:**
- Full `roomList` push protocol with live updates as rooms appear /
  fill / start / end.
- `leaveRoom`, lobby chat (`chat`), kick host action.
- Level selector (A–Z × 1–9) per room with preset previews.
- URL deep-link `?room=ABCD23` joins a room directly after nick prompt.
- Per-room inactivity / abandonment GC (per §6.3).

**Tests:**
- **Lobby unit:** create-room then join-room from 2 mock connections;
  both receive `roomList` updates within 1 tick of the change.
- **Concurrency:** 200 mock connections simultaneously create rooms;
  no panics, all rooms accounted for, room IDs unique.
- **Lifecycle:** room becomes empty → GC'd after 30 s; match with zero
  connected players ends after 30 s; both observable via logs and
  follow-up state queries.
- **Browser e2e (Playwright):** two browser contexts → one creates a room,
  the other joins via deep-link, host starts → both load the match.

### Phase 7 — Polish & feature completeness (1–2 weeks)

**Build:**
- Animations (walk cycle, muzzle flash, death poof).
- Audio (shoot, hit, scream, generator collapse, victory).
- HUD: minimap, scoreboard tab (hold Tab), end-of-match dialog.
- Settings: rebinds, color-blind toggle, master volume, server-list.
- Level letter+number selector in lobby; preset previews.

**Tests:**
- **Golden screenshot diffs (Playwright)** for: empty lobby, full lobby,
  in-match HUD, scoreboard, end screen. **Default CI gate:** ≤ 2 %
  per-pixel tolerance, **Chromium only**. **Nightly gate** (separate job,
  not blocking PR merge): ≤ 0.5 % tolerance across Chromium, Firefox,
  and WebKit. Sub-0.1 % "exact" diffs are aspirational and not enforced
  in v1.
- **Settings persistence:** rebinds and toggles round-trip via
  localStorage; cleared on opt-out.

### Phase 8 — Performance, load testing, deploy (1 week)

**Build:**
- `scripts/load_test.go`: spawns N synthetic clients on M matches,
  measures end-to-end latency and server CPU/RSS.
- Dockerfile: multi-stage, `FROM scratch` final, ~10 MB image.
- `systemd` unit example, `nginx` example for TLS termination.
- README with deploy guide.

**Tests** (split by where they run):

PR-blocking on every push (kept fast):
- **Smoke load:** 4 matches × 4 clients each (16 players) for 60 seconds;
  P99 tick budget < 10 ms; no errors; no goroutine leak after the run.
- **Bandwidth sanity:** average per-client bandwidth ≤ 12 KB/s in/out at
  the smoke-load scale.

Nightly perf job (not PR-blocking):
- **Larger load:** 64 matches × 8 synthetic clients each (512 players) sustain
  30 Hz tick on a 4-core box, P99 server tick budget < 5 ms. Aspirational
  but enforced as a *regression* test (alert if it gets worse), not a
  hard pass/fail target.
- **Bandwidth target:** ≤ 8 KB/s per client under that load (post-AOI).
- **Soak (24 h):** rotating clients; no goroutine leak
  (`/debug/pprof/goroutine` flat); RSS growth < 5 % over the window.

Rationale for the split: 24 h soaks and 512-player loads are valuable
signals but unsuitable as default CI gates — they would block every PR
on infra availability and run-time. They live in `make perf-nightly`.

---

## 9. Test strategy (cross-cutting)

| Layer | Framework | What's tested | Where it lives |
|---|---|---|---|
| Sim unit | `go test` | Maze gen, physics, AI, combat, level table | `internal/sim/*_test.go` |
| Sim property | `go test` + `testing/quick` | Invariants over random seeds | same |
| Codec fuzz | `go test -fuzz` | Frame encode/decode safety | `internal/proto/proto_test.go` |
| Match integration | `go test` | Sim + transport + actor end-to-end (incl. PvP kill, MatchOver, dead-cam) | `internal/match/match_test.go` |
| Lobby integration | `go test` | Lobby protocol via mock clients | `internal/lobby/lobby_test.go` |
| AOI correctness | `go test` | Self / projectiles always included, far entities excluded, hysteresis stable | `internal/match/aoi_test.go` (PR-gated) |
| Realistic-net harness | `go test` | Latency / jitter / stalls / reconnect / backpressure over `net.Pipe` | `internal/net/lag_test.go` |
| Client unit | `vitest` | Prediction, codec, lobby reducer | `web/tests/*.test.ts` |
| Client e2e | `@playwright/test` | Full browser flows, broad-tolerance golden images | `web/tests/e2e/*.spec.ts` |
| Load (smoke) | custom Go harness | 16 players × 60 s, PR-gating | `scripts/load_test.go` |
| Load (nightly) | custom Go harness | 512 players, soak, perf regression | `scripts/load_test.go --nightly` |
| Synthetic stress | `go test -tags synthetic` | App-level snapshot drops, AOI saturation extremes | `internal/match/synthetic_test.go` |
| Replay regression | custom Go harness | Deterministic playback of historical bugs | `testdata/replays/*` |

**What "network" tests cover.** WebSocket runs over TCP, which is
reliable-ordered. The harness therefore models the failure modes that
*actually* happen on a real link — latency, jitter, TCP stalls,
reconnects, and writer backpressure — not artificial app-level packet
loss. Tests that intentionally drop frames at the app layer are tagged
`synthetic` and live in a separate file.

**Coverage gates (CI):** ≥ 80 % statements for `internal/sim`, ≥ 70 %
elsewhere; PR fails below threshold.

**Bug-driven testing:** every production bug becomes (a) a replay
fixture in `testdata/replays/` if the sim can reproduce it, or (b) a
deterministic unit test. Then fix.

**CI vs. nightly split.** PRs are gated by the *fast* tests — unit,
integration, smoke load, broad-tolerance e2e on Chromium. Nightly runs
the heavy stuff: 24 h soak, 512-player load, strict-tolerance cross-
browser e2e. This keeps PR feedback fast without giving up the heavy
signal.

---

## 10. Build, run, deploy

- `make dev` — runs `go run ./cmd/isnipes --dev` (which proxies to Vite
  in dev mode) **and** `pnpm dev` in `web/` concurrently. Browser at
  `http://localhost:5173`; the server listens on `:8080` for WS.
- `make build` — `pnpm build` then `go build -tags embed -o isnipes
  ./cmd/isnipes`. Produces a single self-contained binary; the client
  assets are embedded with `//go:embed`.
- `./isnipes` — runs the prod binary; serves the client at `/` and WS
  at `/ws/lobby` and `/ws/match/{id}`.
- Docker: `docker build -t isnipes .` → `docker run -p 8080:8080 isnipes`.

---

## 11. Failure modes & how we handle them

| Failure | Detection | Response |
|---|---|---|
| Match goroutine panic | `recover()` in match wrapper | Best-effort: send `MatchOver{reason: server_error}` then close each WS with `Close{code: 4006, reason: "SERVER_ERROR"}` (§4.3.5). If the panic state prevents writing frames, skip the `MatchOver` and close with 4006 directly. Ship stacktrace to logs; release players to the lobby. Clients seeing close-code 4006 (with or without a preceding `MatchOver`) treat it as a non-clean match end. |
| Client send-buffer full (backpressure) | Writer goroutine non-blocking enqueue | Drop the WS connection; client can reconnect within the 30 s grace using its `joinToken` (§4.7). |
| Snapshot too large (> 64 KB) | Encoder length check | Drop entities outside the player's AOI radius first; log warning. If still too large, omit projectiles next. |
| Sim tick over budget | `time.Since(start) > 33 ms` warning | Skip the next snapshot (every-2nd-tick → 4th-tick this round) to catch up; never accumulate; emit Prom metric. |
| Schema checksum mismatch | On lobby `hello` (JSON) or match `MatchJoin` (binary) | **Lobby:** reply with JSON `error{code: VERSION, message}`. **Match:** close WS with `Close{code: 4002, reason: "VERSION"}` (§4.3.5). Client shows "please refresh". |
| Invalid / expired `joinToken` | `MatchJoin` validation | Close match WS with `Close{code: 4001, reason: "AUTH"}` (§4.3.5); player must return to lobby. |
| Reconnect within DC-grace | `MatchJoin` arrives < 30 s after WS drop, matching token | Re-bind connection to the existing entity; send `Resync` → `MapInit` → `Snapshot` → `Scoreboard`; emit `Event{kind: player_rejoin}`. |
| Reconnect after DC-grace | `MatchJoin` arrives ≥ 30 s after WS drop | Token already invalidated and slot terminated; close with `Close{code: 4001, reason: "AUTH"}` (per §4.7.1). Player returns to lobby. |
| Idle-connection timeout (no frame of *any* type received from peer for 5 s) | Per-conn reader timer, reset on every received frame | Mark connection dead; close WS with `Close{code: 4007, reason: "IDLE"}` (§4.3.5); start the 30 s DC-grace per §4.7.1. Ping at 2 Hz is sufficient to keep this timer at bay; any other frame counts too. |

---

## 12. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Internet latency too high for a fast game | Lag compensation; turbo/dodge as core mechanic; client-side prediction; allow level configs that lower projectile speed for high-ping rooms. |
| Cross-platform float drift breaks determinism | Integer math everywhere in sim; CI runs sim tests on linux/amd64, linux/arm64, darwin/arm64, windows/amd64. |
| WebSocket head-of-line blocking | Keep frames small; rely on snapshots that are tolerant of frame loss/late delivery. |
| Cheating | v1: server-authoritative + rate limits. Document that anti-cheat is not robust against motivated attackers; do not ship rated play in v1. |
| Scope creep | This spec. Anything not in §3 or §8 phases is v1.1+. |

---

## 13. Open questions / future work

These were assumed (sensibly, I hope) without checking. Easy to change later:

1. **Map size default (60×40):** larger is more strategic but tougher to
   render; smaller is more chaotic. OK?
2. **8-direction discrete movement vs. analog:** I picked 8-direction to
   stay faithful and to simplify the input model and prediction. Switch
   to analog (mouse-aim / WASD-strafe) later if desired.
3. **Lives default (per number 1–9):** generous (max 9 lives). Tunable.
4. **Bots for empty servers:** out of scope v1; v1.1 candidate.
5. **TLS / reverse proxy:** v1 documents both `--require-tls` (the binary
   serves TLS directly with cert/key flags) and "front with nginx".
6. **Account system:** not in v1. Nicknames only.
7. **Live spectator mode** (third parties watching a running match without
   playing): stretch; the dead-cam state (§3.9) already exercises the
   non-participating-camera code path that a spectator UI would reuse.
8. **Server browser across the public Internet:** v1 only knows about one
   server (the one the user connected to). A central directory is v2.
9. **Replays as user-facing feature:** the server can record; a viewer in
   the client is v1.1.

Items 3 (controls) and 8 (game-mode framing) and snapshot rate from the
original draft have been **resolved** and folded into §0 / §3 / §4.3.3.

---

## 14. Initial milestones / timeline (rough)

| Phase | Calendar weeks | Cumulative |
|---|---|---|
| 0 — scaffolding | 0.5 | 0.5 |
| 1 — sim core | 2 | 2.5 |
| 2 — server core + minimal client + **minimal lobby** | 2 | 4.5 |
| 3 — snipes & generators | 1 | 5.5 |
| 4 — prediction & reconciliation | 2 | 7.5 |
| 5 — multiplayer completeness (lives, dead-cam, reconnect, AOI) | 1.5 | 9 |
| 6 — lobby polish & matchmaking | 1 | 10 |
| 7 — polish | 2 | 12 |
| 8 — perf & deploy | 1 | 13 |

≈ 3 calendar months at one focused engineer; faster with parallelization
(client polish and server perf can overlap).

---

## 15. Appendix: Glossary

- **Tile:** a 1×1 grid cell of the maze, 32 px in the client.
- **Subtile unit:** integer position unit, 1/256 of a tile.
- **Tick:** one simulation step at 30 Hz.
- **Snapshot:** server's authoritative state at a tick, broadcast at 15 Hz.
- **AOI:** Area of Interest — entities a given player can see, used to
  trim snapshot bandwidth at scale.
- **Generator / hive:** static enemy that spawns snipes.
- **Snipe:** NPC enemy spawned by a generator.
- **Turbo:** held movement modifier; locks direction, moves at projectile
  speed, disables firing.
