# Phase 2 — Headless server + minimal client + minimal lobby + basic multiplayer match

This document is the buildable, testable expansion of §8 Phase 2 of
[`SPEC.md`](./SPEC.md). It assumes Phase 1 (the `internal/sim` package
with the API surface in `PHASE1.md` §5) is complete and on `main`.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§4.3.2" point into `SPEC.md`; references like "P1 §10.1" point
into `PHASE1.md`.

---

## 1. Scope and definition of done

**Scope.** Ship an honest end-to-end playable PvP-only milestone:

1. A binary (`cmd/isnipes`) that serves a lobby WS (`/ws/lobby`) and
   a match WS (`/ws/match/<matchId>`).
2. A wire-protocol package (`internal/proto`) that encodes/decodes the
   §4.3.1 lobby JSON envelopes and the §4.3.2 binary frames listed in
   §1.1 below, with a stable `schemaChecksum`.
3. A minimal lobby (`internal/lobby`): handshake, **one** room at a
   time per player, host-driven `startMatch`, per-player `joinToken`
   issuance.
4. A match actor (`internal/match`) that owns one `*sim.Sim`,
   accepts `Input` frames from up to 8 connected clients, runs the
   sim at 30 Hz, broadcasts `Snapshot` at 15 Hz, and emits `Event`s
   and `MatchOver` per §4.3.2.
5. WS transport (`internal/net`): RFC 6455 upgrade, binary framing
   with the §4.3.2 header, per-conn reader/writer goroutines, the
   §4.3.5 close codes, and the §4.3.3 5-second idle drop.
6. A browser client (`web/`): connect to lobby, create/join one room,
   on `matchStarted` open the match WS with `MatchJoin`, render the
   maze from `MapInit`, render entities from `Snapshot`, send `Input`
   each animation-frame tick. **No prediction** — pure snapshot
   display.

**Definition of done.** All of the following pass on `main`:

- `go test -race ./...` is green on the four SPEC §12-mandated native
  CI runners: linux/amd64, linux/arm64, darwin/arm64, windows/amd64.
- `pnpm -C web test` is green on the same CI matrix (CI uses
  `ubuntu-latest` for the JS jobs — node is cross-arch but Playwright
  needs Chromium; Chromium is x64-only on macOS arm via Rosetta, so
  the Playwright job is **linux/amd64-only** while the unit-tests run
  on every node target).
- `pnpm -C web build` produces a static bundle under `web/dist/` that
  the Go binary embeds via `embed.FS`.
- `make build` produces a single binary; running `./isnipes
  --addr=127.0.0.1:0` and connecting two `httptest`-driven WS clients
  completes the **end-to-end integration** test in §13.7 (lobby →
  match → 4-second shootout → `MatchOver`).
- Playwright end-to-end (§13.8) passes on Chromium: two browser
  contexts complete the same lobby-to-`MatchOver` flow with screenshot
  diff tolerance ≤ 5 % per-pixel.
- Schema checksum (§6.1) is **byte-identical** between
  `internal/proto`'s compile-time constant and the value emitted by
  `web/src/proto.ts` (verified by §13.6 cross-runtime test).

### 1.1 Wire-protocol message subset

Phase 2 implements **only** these binary frames. All other types from
§4.3.2 are deferred:

| Type | Code | Direction | Phase 2? |
|---|---:|---|---|
| `MatchJoin` | 0x00 | C→S | yes |
| `Input` | 0x01 | C→S | yes |
| `Snapshot` | 0x02 | S→C | yes |
| `EntityDelta` | 0x03 | — | deferred (v1.1) |
| `Event` | 0x04 | S→C | yes — subset (§6.4 below) |
| `Chat` | 0x05 | — | deferred to Phase 6 |
| `Ping` | 0x06 | C↔S | yes |
| `Pong` | 0x07 | C↔S | yes |
| `MatchOver` | 0x08 | S→C | yes |
| `MapInit` | 0x09 | S→C | yes |
| `Resync` | 0x0A | — | deferred to Phase 5 |
| `Scoreboard` | 0x0B | S→C | yes — lives/score are zero |

Lobby JSON envelopes (§4.3.1) implemented: `hello`, `welcome`,
`roomList`, `createRoom`, `joinRoom`, `leaveRoom`, `startMatch`,
`matchStarted`, `error`. `chat` deferred to Phase 6.

---

## 2. Out of scope

Explicit non-goals for Phase 2 (each is owned by a later phase):

- Snipes, generators, AI, level table (Phase 3).
- Client prediction, interpolation buffer, lag compensation (Phase 4).
- Lives, respawn, dead-cam, scoring, end-of-match scoreboard with real
  numbers (Phase 5).
- DC-grace and reconnect (Phase 5).
- Room list browsing, chat, level selector, deep-link URL parameter,
  multi-room (Phase 6).
- AOI filtering and the §5.3.1 priority order (deferred to Phase 5,
  where snipe counts make the 64-cap actually bind).
- TLS configuration / production deploy (Phase 8).

Phase 2's match is **PvP-only and one-life-per-player**: killed
players are removed from the entity store on the same tick the kill
event fires (`Config.NoRespawn = true`). Match ends when one or zero
players remain alive. No `lives` counter exists yet; the
`Scoreboard` frame's `lives` field is always `1` for live players and
`0` for dead, with `score` always `0`.

---

## 3. Prerequisites and assumptions

- Phase 1's `internal/sim` package on `main` with the §5 public API.
- Go 1.22 or newer (pinned via `go.mod` `toolchain` directive).
- Node 20 or newer, pnpm 9 or newer.
- WebSocket library: `nhooyr.io/websocket` v1.8.x. Justification:
  zero-dep, context-aware, both client and server, BSD license,
  already widely used in Go services. Vendored via `go.mod` only;
  no further transitive deps beyond stdlib.
- No other third-party Go deps in `internal/*`. `cmd/isnipes` may
  import `nhooyr.io/websocket` directly.

---

## 4. Package and file layout

Net-new in Phase 2:

```
cmd/
└── isnipes/
    ├── main.go             # flag parsing, embed.FS, http.Server, lobby + match registry
    └── main_test.go        # smoke: --version, --healthz boot

internal/
├── proto/
│   ├── proto.go            # constants, schema-checksum source of truth
│   ├── lobby.go            # §4.3.1 JSON envelopes (encode/decode)
│   ├── frame.go            # §4.3.2 binary frame header + length validation
│   ├── messages.go         # per-message-type encoders/decoders
│   ├── checksum.go         # schemaChecksum computation (deterministic)
│   ├── doc.go
│   ├── proto_test.go
│   ├── lobby_test.go
│   ├── frame_test.go
│   ├── messages_test.go
│   └── checksum_test.go
│
├── net/
│   ├── server.go           # http.ServeMux wiring, /ws/lobby and /ws/match upgrades
│   ├── conn.go             # per-conn reader/writer goroutines, bounded send queue
│   ├── close.go            # §4.3.5 close-code helpers
│   ├── doc.go
│   ├── server_test.go      # httptest-driven WS-roundtrip tests
│   └── conn_test.go
│
├── match/
│   ├── match.go            # Match actor, Run() loop, control-channel API
│   ├── registry.go         # MatchRegistry: create/lookup/teardown
│   ├── snapshot.go         # builds Snapshot from *sim.Sim
│   ├── scoreboard.go       # Scoreboard frame builder (Phase 2: zeroed values)
│   ├── joinauth.go         # MatchJoin validation
│   ├── doc.go
│   ├── match_test.go
│   ├── snapshot_test.go
│   └── joinauth_test.go
│
├── lobby/
│   ├── lobby.go            # Lobby actor + handler glue
│   ├── room.go             # Room struct + state machine
│   ├── token.go            # joinToken issuance, ttl tracking
│   ├── doc.go
│   ├── lobby_test.go
│   ├── room_test.go
│   └── token_test.go
│
└── observ/
    ├── healthz.go          # /healthz handler (carried over from Phase 0)
    └── observ_test.go

web/
├── package.json
├── pnpm-lock.yaml
├── tsconfig.json
├── vite.config.ts
├── index.html
├── src/
│   ├── main.ts             # top-level state machine
│   ├── lobby.ts            # lobby UI + WS handling
│   ├── netClient.ts        # binary WS client, framing, ping/pong
│   ├── proto.ts            # mirror of internal/proto; schema checksum
│   ├── render.ts           # Canvas2D map + entity render
│   ├── input.ts            # keyboard handling per §3.10 Classic preset
│   ├── matchClient.ts      # match WS lifecycle; sends Input, applies Snapshot
│   └── version.ts          # build-time injected schemaChecksum string
├── tests/
│   ├── proto.test.ts       # checksum + round-trip against committed fixtures
│   ├── render.test.ts      # tile-by-tile output for a known MapInit fixture
│   ├── input.test.ts       # keymap → Input.dir/fire_dir/turbo unit tests
│   └── e2e/
│       └── shootout.spec.ts  # Playwright: lobby → match → MatchOver
└── public/
    └── (sprites, sounds — minimal placeholder PNGs/WAVs)

testdata/
├── proto/
│   ├── checksum.txt        # the canonical schemaChecksum hex value
│   ├── snapshot_baseline.bin    # one committed Snapshot frame, hand-built
│   ├── snapshot_baseline.json   # parsed equivalent (for vitest equality)
│   ├── mapinit_baseline.bin
│   └── mapinit_baseline.json
└── e2e/
    └── shootout_inputs.json     # scripted inputs for the integration test
```

No file under `internal/proto/` imports `net/http`, `nhooyr.io/websocket`,
or any I/O. `internal/proto/` is a **pure** library by contract
(verified by §13.4 import-graph test). `internal/sim/` continues to
have **no** imports from Phase 2 packages — Phase 2 depends on sim,
not vice versa.

---

## 5. Schema checksum

The `schemaChecksum` (u32, little-endian on the wire) is the
deterministic oracle that detects "client and server are running
incompatible binary schemas". It is computed identically on both
sides; mismatch closes the WS with `Close{code: 4002, reason:
"VERSION"}` (§4.3.5).

### 5.1 Definition

`schemaChecksum = SHA-256(schemaDescriptor) interpreted as
little-endian u32 from bytes [0:4]`.

`schemaDescriptor` is a UTF-8 string built by concatenating the
following lines (each terminated by `\n`), **in this exact order**:

```
isnipes-schema/v1
frame-header: [u8 type][u8 flags][u16 seq][u16 ack][u16 len][bytes payload]
dir8: 0,1,2,3,4,5,6,7,8
flag-bits: DEAD=0x01,SPAWN_INVULN=0x02,TURBO=0x04
entity: u32 id, u8 kind, u8 hp, u8 facing, u8 flags, i32 x, i32 y, i16 vx, i16 vy
msg 0x00 MatchJoin C2S u32 schema_checksum, u8 token_len, bytes token
msg 0x01 Input C2S u16 client_tick, u8 dir, u8 turbo, u8 fire_dir
msg 0x02 Snapshot S2C u32 server_tick, u16 your_last_input_tick, u32 your_entity_id, u8 entity_count, [Entity]*n
msg 0x04 Event S2C u8 kind, u32 actor, u32 target, u8 reason
msg 0x06 Ping CS u32 ts_origin
msg 0x07 Pong CS u32 ts_origin, u32 ts_responder
msg 0x08 MatchOver S2C u32 final_tick, u8 reason, u32 winner_id_or_0, u8 entry_count, [u32 player_id, i32 score, u8 lives_remaining]*n
msg 0x09 MapInit S2C u32 seed, u16 width, u16 height, u8 packing, bytes packed_tiles
msg 0x0B Scoreboard S2C u32 server_tick, u8 entry_count, [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]*n
event-kinds: 0x01 entity_spawn, 0x02 entity_hit, 0x03 entity_kill, 0x04 generator_destroyed, 0x05 player_join, 0x06 player_leave, 0x07 player_dc, 0x08 player_rejoin, 0x09 match_starting, 0x0A match_started, 0x0B match_end, 0x0C chat_relay, 0x0D respawn_pending
close-codes: 1000 ok, 4001 AUTH, 4002 VERSION, 4003 MALFORMED, 4004 FULL, 4005 NOT_FOUND, 4006 SERVER_ERROR, 4007 IDLE
```

**Implementation-binding.** The Go source in `internal/proto/checksum.go`
contains this exact string as a `const`. The TypeScript source in
`web/src/proto.ts` contains the same string. `TestSchemaChecksumValue`
(§13.4) asserts that the resulting u32 matches the hex committed in
`testdata/proto/checksum.txt`. Any change to the descriptor must be
accompanied by a checksum file update **and** a Phase 2 minor-version
bump in `schemaDescriptor`'s first line (e.g. `isnipes-schema/v2`).

### 5.2 Why not Go reflection over the message structs

Because TypeScript can't reflect on Go types. A hand-written canonical
descriptor is the only way both runtimes can agree on bytes without a
codegen step. Tests pin the bytes.

---

## 6. Wire protocol — Phase 2 byte-exact specification

This section pins the on-wire encoding of every Phase 2 message. The
encoders and decoders in `internal/proto/messages.go` and
`web/src/proto.ts` implement exactly this; the round-trip tests in
§13.4 enforce it.

All scalars are **little-endian**. Signed integers use two's complement.
UTF-8 strings have no NUL terminator; the preceding length field gives
the byte count.

### 6.1 Frame header (binary, match WS only)

```
[u8 type][u8 flags][u16 seq][u16 ack][u16 len][bytes payload]
                                                  ^^^^^^^^^^^^
                                                  exactly `len` bytes
```

- `type`: the message code from §4.3.2 (0x00..0x0B).
- `flags`: reserved; senders write `0`, receivers ignore.
- `seq`: monotonically increasing per-direction frame counter, mod
  2^16. Wrap is fine; used only for diagnostics (§4.3.6). Starts at
  `0` for the first frame sent on the match WS.
- `ack`: most recently received `seq` from the peer. The first frame
  carries `ack = 0xFFFF` as the "haven't received anything yet"
  sentinel.
- `len`: payload byte count, max `65 535`. Frames exceeding this are
  invalid and trigger `Close{4003 MALFORMED}`.
- `payload`: exactly `len` bytes; structure depends on `type`.

The header is fixed at **8 bytes**. The match WS uses RFC 6455 **binary**
frame messages; one WS message = one logical app frame (no fragmentation
across WS frames in Phase 2; receivers reject WS-fragmented payloads
with `Close{4003 MALFORMED}`).

### 6.2 Lobby JSON envelope (`/ws/lobby`)

Every lobby message is a UTF-8 JSON object:

```json
{ "t": "<type>", "v": 1, "d": { ...payload... } }
```

`v` is the **lobby envelope** version, distinct from the binary
`schemaChecksum`. Phase 2 ships `v = 1`. The server rejects any other
value with `error{code: VERSION, message: "lobby envelope version
unsupported"}` and closes the lobby WS with code `1003`
(`unsupported data` per RFC 6455).

#### 6.2.1 Phase 2 lobby message payloads

```text
hello.d         = { nick: string(1..16), clientVersion: string, schemaChecksum: u32 }
welcome.d       = { playerId: string,  serverVersion: string, schemaChecksum: u32, motd: string }
roomList.d      = { rooms: [ { id, name, players, max, mode, level, state } ] }
createRoom.d    = { name: string(1..32), max: int(2..8), level: { letter: "A"..."Z", number: int(1..9) } }
joinRoom.d      = { roomId: string }
leaveRoom.d     = {}
startMatch.d    = { roomId: string }
matchStarted.d  = { matchId: string, gameSocketPath: string, tickRate: int(=30), mapSeed: u32, joinToken: string(base64, 24 chars) }
error.d         = { code: string, message: string }
```

`playerId` is the server's per-session identity (UUIDv7 stringified).
`roomId` is the 6-character base32 used by §6.3.

The `mode` and `level` fields exist now for forward compatibility
with Phase 3; in Phase 2 they are echoed back unchanged but ignored by
the match actor.

`error.code` values used in Phase 2:

| Code | Meaning |
|---|---|
| `VERSION` | Schema/envelope version mismatch |
| `NICK_TAKEN` | Server rewrote the nick with a `#nnn` suffix (informational; not fatal) |
| `BAD_REQUEST` | Malformed JSON or missing required field |
| `NOT_HOST` | Non-host tried to `startMatch` |
| `NO_ROOM` | Player not currently in a room |
| `ROOM_FULL` | `joinRoom` against a room at `max` |
| `ROOM_GONE` | `joinRoom` against an unknown / closed room |
| `ALREADY_IN_ROOM` | `createRoom` while already in a room |

Server emits `error` and **does not close** the lobby WS on any of the
above (the client recovers). Only the two protocol-fatal cases
(`VERSION`, malformed JSON beyond recovery) close the lobby WS.

### 6.3 Match binary messages

For each message below the **Payload** column lists exact field layout
in order. The "Phase 2 invariants" sub-bullet pins any extra rules
that apply only to Phase 2 (e.g. fields that must be zero).

#### 6.3.1 `MatchJoin` (0x00, C→S)

Payload:
```
[u32 schema_checksum][u8 token_len][bytes token]
```

- `token_len`: 1..32 inclusive. `0` or `>32` → `Close{4003 MALFORMED}`.
- `token`: the per-player `joinToken` from `matchStarted.d.joinToken`
  (24-char base64 of 16 random bytes — see §7.3).
- `schema_checksum`: must equal the server's; otherwise
  `Close{4002 VERSION}`.

**Phase 2 invariants.** `MatchJoin` **must** be the first frame on the
match WS. Any other first frame → `Close{4003 MALFORMED}`. Subsequent
`MatchJoin` frames on the same connection → `Close{4003 MALFORMED}`.

#### 6.3.2 `Input` (0x01, C→S)

Payload:
```
[u16 client_tick][u8 dir][u8 turbo][u8 fire_dir]
```

- `client_tick`: free-running u16, wraps every ~36 min (P1 §5.3 also
  uses u16 for `LastInputTick`).
- `dir`, `fire_dir`: Dir8 in `0..8`; out-of-range value →
  `Close{4003 MALFORMED}`. (This is stricter than P1's silent
  sanitization, because P1's `Tick` may be called by trusted
  internal callers; here the input crosses the trust boundary.)
- `turbo`: `0` or `1`; other → `Close{4003 MALFORMED}`.

The match actor passes each accepted `Input` to `sim.Sim.Tick` on the
**next** tick (§9.3 below). Inputs arriving for an already-dead
player are accepted at the WS layer and silently discarded by the
match actor.

#### 6.3.3 `Snapshot` (0x02, S→C)

Payload:
```
[u32 server_tick]
[u16 your_last_input_tick]
[u32 your_entity_id]
[u8 entity_count]
[Entity × entity_count]
```

Each `Entity` is exactly 21 bytes:
```
[u32 id][u8 kind][u8 hp][u8 facing][u8 flags][i32 x][i32 y][i16 vx][i16 vy]
```

- `your_entity_id`: the recipient's player `EntityID`, or `0` if the
  recipient is dead (one-life-per-player in Phase 2; once dead, stays
  `0` until `MatchOver`).
- `your_last_input_tick`: the `client_tick` of the most recent `Input`
  the server has applied to this player (per P1 `Sim.LastInputTick`).
  `0` if the server has not yet applied any input from this player.
- `entity_count`: 0..64. With max 8 players + max 64 in-flight
  projectiles, an upper bound of 72 entities can exist; Phase 2 ships
  **all entities** in every snapshot (no AOI filtering) and relies on
  the fact that in practice §10.1's 64-projectile cap + 8 players
  means the 64-entity-per-snapshot cap of `u8 entity_count` is
  binding only momentarily. **If the live entity count exceeds 64**,
  the snapshot builder emits the first 64 in ascending `EntityID`
  order and logs a `WARN` once per match — this is correct under
  Phase 2's "everyone sees everything" model and the snipe content
  that drives entity counts above 64 doesn't exist until Phase 3
  anyway. The §5.3.1 AOI priority order is intentionally **not**
  implemented in Phase 2.

**Phase 2 invariants.**
- The order of entities in the payload is ascending `EntityID` (matches
  P1 §12's determinism rule).
- `Entity.flags` may have only the bits in `{FlagDead, FlagTurbo}`
  set; `FlagSpawnInvuln` is never set in Phase 2 (no invuln logic).

#### 6.3.4 `Event` (0x04, S→C)

Payload:
```
[u8 kind][u32 actor][u32 target][u8 reason]
```

Phase 2 emits **only** these event kinds:

| Hex | Name | Emitted from |
|---:|---|---|
| 0x01 | `entity_spawn` | P1 sim events (player respawn — not in Phase 2 since `NoRespawn=true`; projectile spawn) |
| 0x02 | `entity_hit` | P1 sim events (projectile hit on a player) |
| 0x03 | `entity_kill` | P1 sim events |
| 0x05 | `player_join` | Match actor on `MatchJoin` success |
| 0x06 | `player_leave` | Match actor on WS close |
| 0x0A | `match_started` | Match actor at tick 0 |
| 0x0B | `match_end` | Match actor at match termination (just before `MatchOver`) |

`generator_destroyed` (0x04), `player_dc`/`rejoin` (0x07/0x08),
`match_starting` (0x09), `chat_relay` (0x0C), `respawn_pending` (0x0D)
are **not** emitted in Phase 2. `entity_spawn` for the projectile
case **is** emitted because the P1 sim already produces it.

Clients drop unknown `kind` values silently per SPEC §4.3.2.

#### 6.3.5 `Ping` (0x06, C↔S) and `Pong` (0x07, C↔S)

`Ping` payload: `[u32 ts_origin]`. `Pong` payload:
`[u32 ts_origin][u32 ts_responder]`. Both use the sender's monotonic
clock in milliseconds; wrap on overflow is acceptable (RTT computed
modulo 2^32 ms ≈ 49 days).

- Either side may initiate; the receiver immediately echoes a `Pong`.
- The cadence in Phase 2 is **server → client only**, every 500 ms
  (§4.3.3). Client `Ping`s are accepted and echoed but the client is
  not required to send them in Phase 2 (it will in Phase 4 for the
  client-side RTT measurement). This keeps the Phase 2 client trivial.
- A side considers the peer dead if no frame of any type has arrived
  in 5 s. The match WS closes with `Close{4007 IDLE}` per §4.3.5.

#### 6.3.6 `MatchOver` (0x08, S→C)

Payload:
```
[u32 final_tick]
[u8 reason]
[u32 winner_id_or_0]
[u8 entry_count]
[ { u32 player_id, i32 score, u8 lives_remaining } × entry_count ]
```

Phase 2 `reason` values: `1` (`LAST_STANDING`), `2`
(`ALL_ELIMINATED`), or `4` (`SERVER_ERROR`). The other values from
§3.8.1 are not reachable in Phase 2:

- `PVE_COMPLETE` (0): requires generators, not in Phase 2.
- `TIMER` (3): Phase 2 does **not** ship a match timer. A match is
  guaranteed to end via `LAST_STANDING` or `ALL_ELIMINATED` (or
  `SERVER_ERROR`) because every player has 1 life and there are no
  respawns.

`score` is always `0` and `lives_remaining` is `0` for dead, `1` for
the last-standing winner. The full scoring system is Phase 5.

After `MatchOver` the match actor closes the match WS with `Close{1000
"ok"}` for every still-connected client.

#### 6.3.7 `MapInit` (0x09, S→C)

Payload:
```
[u32 seed][u16 width][u16 height][u8 packing][bytes packed_tiles]
```

- `seed`, `width`, `height` match `Config.{Seed, Width, Height}`.
- `packing` = `1` (2 bits per tile, little-endian within each byte,
  row-major — matches P1 §7.9 and P1 `Sim.MapBytes()` byte-for-byte).
- The byte length of `packed_tiles` is `len - 9` (frame header `len`
  minus the 9-byte fixed prefix).

`MapInit` is sent **once**, immediately after the match actor accepts
`MatchJoin` (§9.1 below). It is **not** re-sent on Phase 2 (no
resync, no reconnect).

#### 6.3.8 `Scoreboard` (0x0B, S→C)

Payload:
```
[u32 server_tick]
[u8 entry_count]
[ { u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score } × entry_count ]
```

In Phase 2:
- `entry_count` is the count of players who have completed `MatchJoin`,
  in order of join (ascending `joinTick`, then ascending `playerId`).
- `lives` is `1` while the player is alive, `0` after death.
- `score` is always `0`.

Cadence: emitted once at match start (right after `MapInit` to every
client that has just `MatchJoin`ed), and once whenever a player
transitions alive → dead. Rate-limit to 5 Hz still applies (§4.3.2)
but is not stressed by Phase 2's event volume.

### 6.4 Phase 2 event-emission rules

For each P1 sim event, the match actor translates it to a Phase 2
`Event` frame as follows:

| P1 event | Translation |
|---|---|
| `entity_spawn` for a `KindProjectile` | Forwarded verbatim. |
| `entity_spawn` for a `KindPlayer` | **Not emitted** — only happens on respawn, which `NoRespawn=true` disables. |
| `entity_hit` | Forwarded verbatim. |
| `entity_kill` with player target | Forwarded verbatim. |
| `entity_kill` with projectile target | Forwarded verbatim (wall hit or timeout). |
| `generator_destroyed` | Not emitted (no generators). |

Additionally, the match actor emits:
- `player_join{actor=0, target=playerId, reason=0}` on successful
  `MatchJoin`.
- `player_leave{actor=0, target=playerId, reason=R}` on WS close:
  `R=0` clean, `R=1` timeout (idle 5 s drop). `R=2` (`kicked`) is
  unused in Phase 2.
- `match_started{actor=0, target=0, reason=0}` once, at the same tick
  as the first `Snapshot`.
- `match_end{actor=winner_id_or_0, target=0, reason=<§3.8.1 enum>}`
  immediately before `MatchOver` is sent.

Event ordering within one snapshot interval: events accumulate from
the moment the previous `Snapshot` was sent until the next one. They
are delivered as **separate** binary frames, in their P1-emission
order, **interleaved before** the `Snapshot` frame so the client can
update its state machine in lockstep with the snapshot tick. (The
match actor's `broadcast` step is event-batch then snapshot, per §9.2.)

---

## 7. Lobby

### 7.1 State machine

Per-player session states:

```
NEW → CONNECTED → (in_room) ROOM_IDLE → MATCH_STARTING → MATCH_LIVE
                                                       ↘ MATCH_ENDED → ROOM_IDLE
                                                       ↘ MATCH_ABORTED → ROOM_IDLE
```

A player session starts in `NEW`; sending `hello` advances to
`CONNECTED`; `createRoom` or `joinRoom` advances to `ROOM_IDLE`;
host `startMatch` advances every member of the room to
`MATCH_STARTING`; once the match actor confirms the match is live,
members are in `MATCH_LIVE`; once `MatchOver` returns (or the match
fails to start within 30 s), members return to `ROOM_IDLE`.

A player in `MATCH_LIVE` who has the match WS open is still tracked
by the lobby — leaving the lobby WS during a match is allowed in
Phase 2 (the match continues until `MatchOver`; the player is
effectively `player_leave{reason=0}` from the match's perspective if
they also close the match WS).

### 7.2 Room state machine

```
OPEN → STARTING → IN_MATCH → CLOSED
   ↘ CLOSED (manual close / GC)
```

- `OPEN`: host present; other players may `joinRoom` up to
  `max`. Host may `leaveRoom`, which transitions the room to
  `CLOSED` (Phase 2 has no host-handoff; the simplest semantics).
- `STARTING`: triggered by host `startMatch`. Lobby mints
  `joinToken`s and creates the match actor. While in this state, no
  new joins are accepted (`joinRoom` returns `ROOM_GONE`).
- `IN_MATCH`: at least one player's `MatchJoin` has succeeded. Room
  transitions to `CLOSED` on `MatchOver`.
- `CLOSED`: room is removed from the registry after a 5-second
  grace (during which any straggler `joinRoom` returns `ROOM_GONE`).

### 7.3 `joinToken`

Token format: 16 cryptographically random bytes (Go: `crypto/rand`),
base64 (RFC 4648 §5 url-safe, no padding). Result is 22 chars; we
encode-with-padding for transport (so 24 chars including `==`).

TTL: 60 seconds from issuance. The lobby stores each issued token in a
`map[token]*pendingMatchJoin` keyed by token. On `MatchJoin`:
- Token not in map → `Close{4001 AUTH}`.
- Token expired (timestamp older than 60 s) → `Close{4001 AUTH}` and
  evict.
- Token's `matchId` does not match the URL `matchId` → `Close{4001
  AUTH}` and evict.
- Otherwise: remove from pending map (single-use), bind to the WS,
  proceed.

The lobby evicts tokens at the 60-second mark via a single janitor
goroutine that scans the map every 10 seconds. No per-token timer.

### 7.4 Identity

A `hello` produces a fresh `playerId` (UUIDv7) for the session. There
is no persistent identity in Phase 2 — refreshing the page gets you
a new ID. Nicks are display-only; the server appends `#nnn` if a
duplicate joins, and includes the assigned nick in the `welcome`
reply.

### 7.5 Idle timeouts

- An open lobby session with no message of any type in 60 seconds is
  closed by the server (lobby WS close code `1000 "idle"`).
- A room with **no members** is GC'd after 30 seconds (§6.3 of SPEC).
- A room in `STARTING` for more than 30 seconds (because no client
  ever sends `MatchJoin`) transitions to `CLOSED`; the match actor
  terminates with `MatchOver{reason=SERVER_ERROR}` if it has started.

---

## 8. Match-join token & first-frame handshake

### 8.1 The match WS URL

`/ws/match/<matchId>` — `matchId` is a 24-char base64 of 16 random
bytes, matched by `^[A-Za-z0-9_-]{22,24}$` server-side. Unknown
`matchId` → HTTP 404 (the upgrade is rejected before WS handshake).

### 8.2 Handshake sequence

```
1. C → S:  WS upgrade to /ws/match/<matchId>
2. S:      accept upgrade; arm 5 s "first-frame" timer
3. C → S:  binary frame  MatchJoin{schema_checksum, token}
4. S:      validate: checksum equal? token in map? token's matchId
           equals URL matchId? token TTL not expired?
              failure: Close{4001 AUTH} or Close{4002 VERSION}
5. S:      bind WS to player slot in match actor
6. S → C:  Event{kind=player_join, target=playerId}
7. S → C:  MapInit{seed, w, h, packing, packed_tiles}
8. S → C:  Scoreboard{server_tick=current, ...}
9. S → C:  Snapshot{...}
10. S → C: Event{kind=match_started}  (only for the first joiner; subsequent
           joiners only get steps 6..9, since the match is already live)
```

If the client does not send any frame within 5 seconds of WS open →
`Close{4007 IDLE}`. If the first frame is not `MatchJoin` →
`Close{4003 MALFORMED}`.

### 8.3 Slot population

The match starts in a `WAITING_FOR_JOINS` substate. As each player's
`MatchJoin` succeeds, the actor allocates them an `EntityID` from
the `cfg.PlayerIDs` list reserved at construction time, then runs
`NewSim` once **all** rooming players have joined OR the 10-second
"match warmup" timer expires (then start with whoever has joined, as
long as ≥ 2 players have joined; otherwise `MatchOver{reason=ALL_ELIMINATED}`
and tear down).

`NewSim` is constructed with `Config{Seed: mapSeed, Width: 60,
Height: 40, PlayerIDs: <joined slots>, NoRespawn: true, NoGenerators:
true}` — matching the Phase 2 PvP-only contract.

---

## 9. Match actor

### 9.1 Lifecycle

```
NEW → WAITING_FOR_JOINS → LIVE → ENDED
```

The match actor owns:
- A `*sim.Sim` (created on transition to `LIVE`).
- A control inbox `chan controlMsg` (capacity 256). Producers: WS
  reader goroutines, lobby (for `Close` requests), timers.
- A per-player slot map keyed by `EntityID`.
- A `*ticker` running at 30 Hz.

`Match.Run()` is a single goroutine. Per the actor model, no other
goroutine reads or writes sim state directly; everything goes through
the inbox.

### 9.2 Tick loop pseudo-code

```go
func (m *Match) Run() {
    ticker := time.NewTicker(33333 * time.Microsecond)
    defer ticker.Stop()
    for {
        select {
        case msg := <-m.in:
            m.handleControl(msg)   // join, input, ws-close, abort
        case <-ticker.C:
            if m.state != LIVE { continue }
            inputs := m.drainPerPlayerInputs()
            events, err := m.sim.Tick(inputs)
            if err != nil {
                m.abort("sim error: " + err.Error())
                continue
            }
            m.broadcastEvents(m.synthesizeFrameEvents(events))
            if m.serverTick%2 == 0 {
                m.broadcastSnapshot()
            }
            if reason, winner, done := m.evaluateMatchEnd(); done {
                m.broadcastMatchEnd(reason, winner)
                return
            }
            m.serverTick++
        }
    }
}
```

`drainPerPlayerInputs` collects, at most, one `Input` per `EntityID`
per tick — the **latest** received between this tick and the previous
one (per §4.3.3 "up to 30 inputs per second"). A client that sends 2+
inputs in the same 33.3 ms tick has all but the last silently
discarded; this matches P1's `Tick`'s "later inputs for the same
PlayerID overwrite earlier" rule (P1 §11 step 2).

### 9.3 Match-end evaluation

Phase 2 evaluates §3.8.1 outcomes in priority order at the end of
every tick, but only the reachable subset:

1. `LAST_STANDING` — fires when **exactly one** player has
   `Flags&FlagDead == 0`, AND the match started with ≥ 2 players.
2. `ALL_ELIMINATED` — fires when **zero** players have
   `Flags&FlagDead == 0`.
3. `SERVER_ERROR` — fires from the `defer recover()` in `Run()`
   on a panic.

`PVE_COMPLETE` and `TIMER` are not reachable in Phase 2.

Edge case: in a **1-player** Phase 2 match, `LAST_STANDING` cannot
fire (the rule requires ≥ 2 starting players); the only way the match
ends is `ALL_ELIMINATED` (the lone player dies). Solo Phase 2 matches
are reachable only via the §8.3 "10-second warmup expired" path with
exactly one joiner — by design, this **declines** to start in Phase
2 and the lobby tears down with no match ever entering `LIVE`. The
warmup-expired solo path therefore short-circuits to a `MATCH_ABORTED`
notification (lobby `error{code: NO_OPPONENT}`) instead of running an
unwinnable PvP match.

### 9.4 Match registry

`internal/match/registry.go` exposes:

```go
type Registry struct{ /* unexported */ }
func NewRegistry(cfg RegistryConfig) *Registry
func (r *Registry) Create(matchID string, cfg MatchConfig) (*Match, error)
func (r *Registry) Lookup(matchID string) (*Match, bool)
func (r *Registry) RemoveEnded(matchID string)
func (r *Registry) Close(ctx context.Context) error
```

`RegistryConfig` includes `MaxConcurrentMatches int` (default 64,
configurable via `--max-matches`). Exceeding the cap returns
`ErrTooManyMatches` and the lobby surfaces `error{code: ROOM_FULL}`
(reused — there is no separate `MATCH_FULL` Phase 2 code) to the
host that called `startMatch`.

---

## 10. Transport / `internal/net`

### 10.1 Server bootstrap

`internal/net/server.go` exposes:

```go
type Server struct{ /* unexported */ }
func NewServer(cfg ServerConfig) *Server
func (s *Server) Handler() http.Handler   // ServeMux with /ws/lobby, /ws/match/{id}, /healthz, /version, /metrics
func (s *Server) Close() error
```

`ServerConfig`:
```go
type ServerConfig struct {
    Lobby           LobbyHandler
    MatchRegistry   *match.Registry
    StaticFS        fs.FS    // embed.FS rooted at web/dist
    MaxFrameBytes   int      // default 65 535
    HandshakeTimeout time.Duration // default 5s
}
```

### 10.2 Per-connection

`internal/net/conn.go` runs **one reader** and **one writer**
goroutine per WS:

- **Reader**: drains WS messages, deframes per §6.1, validates
  `len`, posts a typed message to the consumer's inbox channel.
  On any deframing failure → `Close{4003 MALFORMED}` and exit.
- **Writer**: drains a bounded send queue (capacity 64 frames per
  §5.3 of SPEC) and writes to the WS. If the queue is full when the
  match actor tries to push, the match actor **drops the connection**
  (`Close{1011, "backpressure"}`) rather than backpressure the tick
  loop. In Phase 2 this is logged but the player is not re-invited;
  Phase 5's reconnect machinery is what makes this recoverable.

The reader's idle timer is set to **5 seconds** (§4.3.3); receiving
any frame resets it. On timeout → `Close{4007 IDLE}`.

### 10.3 Close-code helpers

`internal/net/close.go` provides:

```go
type CloseReason int
const (
    CloseOK            CloseReason = 1000
    CloseAuth          CloseReason = 4001
    CloseVersion       CloseReason = 4002
    CloseMalformed     CloseReason = 4003
    CloseFull          CloseReason = 4004
    CloseNotFound      CloseReason = 4005
    CloseServerError   CloseReason = 4006
    CloseIdle          CloseReason = 4007
)

func (r CloseReason) Label() string  // "ok", "AUTH", ...
```

### 10.4 Bandwidth ceiling (sanity)

At 15 Hz snapshots × 8 players × (8 header + 11 prefix + 8 × 21 entity)
≈ 15 × 8 × 195 = **23.4 KB/s server→clients aggregate**, well within
typical broadband. Phase 2 does not enforce a global bandwidth cap; it
relies on the per-connection 64-frame queue to catch pathological cases.

---

## 11. Browser client

### 11.1 Modules and contracts

- `proto.ts` exports `SCHEMA_CHECKSUM: number` (the literal from
  `testdata/proto/checksum.txt`), `encodeMatchJoin`, `encodeInput`,
  `decodeFrame`. Throws `ProtocolError` on any of the §6.3 close-code
  triggers.
- `netClient.ts` exposes a single `MatchSocket` class that takes the
  `gameSocketPath` and `joinToken`, sends `MatchJoin` on `open`, and
  emits typed events for each decoded incoming frame.
- `render.ts` exposes `Renderer(canvas, mapInit)` and a per-frame
  `draw(snapshot, eventsSinceLast, yourEntityId)` API. The renderer
  builds an `OffscreenCanvas` cache of the static map on
  `mapInit` arrival; subsequent draws blit it then draw entities.
- `input.ts` listens to keyboard events per the §3.10 Classic preset
  and emits an `Input` object each animation frame. `client_tick` is
  the page-load-relative tick counter modulo 2^16.

### 11.2 Render constraints

- Canvas resolution: 1280 × 800 logical pixels, scaled by `devicePixelRatio`.
- One tile = 16 logical pixels (so a 60×40 map fills 960×640 with HUD
  margin).
- Entities draw as 14 × 14 squares (no sprite art is shipped in Phase
  2; SVG/PNG sprites are Phase 7). Player colour: HSL by `(EntityID *
  60) mod 360`. Projectiles draw as 4×4 squares.
- No animations, no interpolation — entities snap to their latest
  snapshot position. This is **expected**; smooth interpolation is
  Phase 4.

### 11.3 Top-level state machine

```ts
type AppState =
  | { kind: 'lobby_connecting' }
  | { kind: 'lobby_ready', roomList: Room[], myNick: string }
  | { kind: 'room', room: Room, isHost: boolean }
  | { kind: 'match_starting', matchInfo: MatchStartedPayload }
  | { kind: 'in_match', match: MatchState }
  | { kind: 'post_match', summary: MatchOverPayload }
  | { kind: 'error', message: string }
```

Each state has a single React-style render function; no framework is
used. State transitions are explicit.

---

## 12. Determinism and reproducibility

Phase 2 inherits P1's determinism guarantees for `internal/sim`. New
requirements introduced by Phase 2:

- The match actor's `serverTick` is **the same** as `sim.ServerTick()`
  at all times (the match actor never increments its own counter
  independently). This is asserted by `TestMatchTickInSync` (§13.5).
- Snapshot byte output is a pure function of `(serverTick, sim state,
  player's lastInputTick, your_entity_id, alive-flag)`. Two snapshots
  for the same recipient at the same tick must be byte-identical.
  Verified by `TestSnapshotByteRepro` (§13.4).
- The schema checksum is byte-identical across architectures; verified
  by `TestSchemaChecksumValue` on every CI runner (§13.4).
- The match-join token generator uses `crypto/rand` (non-deterministic
  by design — tokens **must** be unpredictable). The map of issued
  tokens is, however, deterministically iterable: the lobby uses a
  `sync.Map` for concurrency but never iterates it in a way that
  affects observable state.

---

## 13. Test plan

All tests live alongside the source they test. CI invokes `go test
-race -count=1 ./...` and `pnpm -C web test`. Test names below are
exact (`TestX` / `BenchmarkX` / Playwright `test('X')`).

### 13.1 Proto: frame header & length validation (`internal/proto/frame_test.go`)

- `TestFrameHeaderRoundTrip`: encode/decode 100 random headers using
  `testing/quick`. Every type/seq/ack/len combination round-trips
  byte-for-byte.
- `TestFrameHeaderRejectsOversizedLen`: payload `len = 65 535` is
  accepted; `len + reader buffer remaining < 65 535 + 8` → reader
  returns `ErrTruncated`.
- `TestFrameHeaderRejectsZeroLenWithNonZeroPayload`: header reports
  `len=0` but `8 < buffer size` and `buffer[8] = 0xFF` →
  `ErrMalformed` (this is a defensive sanity check; receivers stop
  reading at byte 8 when `len=0`, so the trailing byte is never
  observed in production, but the test ensures the deframer doesn't
  silently consume more than `len` bytes).

### 13.2 Proto: every Phase 2 message type round-trips (`internal/proto/messages_test.go`)

- `TestRoundTrip_<Msg>` (one per type in §1.1): encode → decode →
  encode produces identical bytes. Inputs are hand-built and exercise
  edge values: `entity_count=0`, `entity_count=64`, `token_len=1`,
  `token_len=32`, `Snapshot.your_entity_id=0`, `MatchOver.entry_count=0`.
- `TestQuickRoundTrip_Snapshot`: `testing/quick` fuzz with arbitrary
  entity slices; round-trip identity.
- `TestDecodeRejectsBadDir`: `Input` frames with `dir > 8` or
  `fire_dir > 8` return `ErrMalformed`.
- `TestDecodeRejectsBadTurbo`: `Input.turbo` in `{2..255}` returns
  `ErrMalformed`.

### 13.3 Proto: schema checksum (`internal/proto/checksum_test.go`)

- `TestSchemaChecksumValue`: the computed `u32` equals the hex in
  `testdata/proto/checksum.txt`. CI runs on all 4 OS/arch runners.
- `TestSchemaChecksumIsLittleEndianBytes0_3`: `checksum.bytes[0:4]`
  matches `binary.LittleEndian.Uint32` of those bytes — pins the
  byte order so a future SHA-256 implementation can't silently
  reorder.

### 13.4 Proto: byte fixtures cross-runtime (`internal/proto/messages_test.go` + `web/tests/proto.test.ts`)

- `TestMapInitFixtureMatches`: encode a `MapInit{seed=0xDEADBEEF,
  w=60, h=40, packing=1, packed_tiles=<P1 baseline maze for that seed>}`
  and assert bytes equal `testdata/proto/mapinit_baseline.bin`.
- `TestSnapshotFixtureMatches`: same for a hand-built snapshot
  payload.
- **Vitest mirror** in `web/tests/proto.test.ts`: decode the same
  `.bin` fixtures and assert the decoded JSON equals
  `testdata/proto/<name>_baseline.json`. Together, these tests pin
  the bytes to specific values **and** validate cross-runtime
  agreement.
- `TestProtoImportGraph`: `go list -f '{{ join .Imports "\n"}}'
  github.com/<org>/isnipes/internal/proto` returns only stdlib
  imports. CI parses this to enforce the §4 "no I/O" invariant.

### 13.5 Match actor unit (`internal/match/match_test.go`, `snapshot_test.go`)

- `TestMatchActorTickRate`: feed 100 simulated ticks via a test
  ticker; the actor's `serverTick` reaches 100 ± 1.
- `TestMatchActorBroadcastsSnapshotEvery2Ticks`: with two fake
  clients, `serverTick=4` produces `Snapshot` frames at ticks 2 and 4
  (15 Hz).
- `TestMatchTickInSync`: `m.serverTick == m.sim.ServerTick()` after
  every `tick()`.
- `TestMatchEndOnLastStanding`: 2-player scripted scenario where A
  shoots B; the actor emits `Event{match_end, actor=A.ID,
  reason=LAST_STANDING}` followed by `MatchOver{reason=1, winner=A.ID}`,
  then closes both WS with `1000 ok`.
- `TestMatchEndOnAllEliminated`: 2-player scenario where both die on
  the same tick (a mutual point-blank kill). `match_end` carries
  `reason=ALL_ELIMINATED` and `winner=0`.
- `TestMatchEndOnSinglePlayerDies`: 1-player match (which we never
  enter in practice — see §9.3 — but the actor accepts the config).
  The lone player dies → `MatchOver{reason=2, winner=0}`.
- `TestMatchSurvivesPanic`: inject a panic via a hook into the tick
  loop; the `defer recover()` produces `Event{match_end,
  reason=SERVER_ERROR}` and `MatchOver{reason=4, winner=0}`.
- `TestSnapshotEntityOrderingByID`: 8 players assigned non-contiguous
  IDs; emitted snapshot has entities in ascending ID order.
- `TestSnapshotPerRecipientYourEntityId`: each of 4 clients receives a
  `Snapshot` whose `your_entity_id` matches their own player ID, while
  the rest of the payload (sorted entity list) is identical across
  clients.
- `TestSnapshotByteRepro`: build the same `Sim` twice, drive the same
  inputs; per-recipient `Snapshot` bytes are identical.

### 13.6 Match-join auth (`internal/match/joinauth_test.go`)

- `TestMatchJoinAcceptsValidToken`: lobby issues token, client
  presents → success, slot bound.
- `TestMatchJoinRejectsExpiredToken`: lobby issues token, wait 61 s
  (test uses an injectable clock), present → `Close{4001 AUTH}`.
- `TestMatchJoinRejectsWrongMatchId`: token for match A is presented
  on WS for match B → `Close{4001 AUTH}`.
- `TestMatchJoinRejectsReusedToken`: present once successfully, then
  open a second WS with the same token → `Close{4001 AUTH}` (tokens
  are single-use).
- `TestMatchJoinRejectsBadChecksum`: token valid, checksum off by one
  → `Close{4002 VERSION}`.
- `TestMatchJoinRejectsBadTokenLen`: `token_len = 0` or `> 32` →
  `Close{4003 MALFORMED}`.
- `TestMatchJoinRejectsLateFirstFrame`: open WS, send no frame for 6 s
  → `Close{4007 IDLE}`.
- `TestMatchJoinRejectsWrongFirstFrame`: open WS, send `Input` as the
  first frame → `Close{4003 MALFORMED}`.

### 13.7 Lobby unit (`internal/lobby/lobby_test.go`, `room_test.go`)

- `TestLobbyHelloWelcome`: two synthetic clients exchange handshakes;
  each gets a distinct `playerId`, both echo the same
  `schemaChecksum`.
- `TestLobbyCreateAndJoin`: client A `createRoom`; client B
  `joinRoom`. Both see `roomList` updates with the room in their
  respective post-update queries.
- `TestLobbyNickCollisionAppendsSuffix`: two clients send `hello`
  with the same nick; second's `welcome.nick` is `Same#001`.
- `TestLobbyStartMatchHostOnly`: B sends `startMatch` for a room
  hosted by A → `error{code: NOT_HOST}`.
- `TestLobbyStartMatchEmitsPerMemberToken`: 4 members, host
  `startMatch` → all 4 receive `matchStarted` with distinct
  `joinToken` values; same `matchId`.
- `TestLobbyJoinRoomFullRejects`: room at `max`; another join →
  `error{code: ROOM_FULL}`.
- `TestLobbyAlreadyInRoomRejects`: A in room R, A `createRoom` →
  `error{code: ALREADY_IN_ROOM}`.
- `TestLobbyIdleSessionClosed`: WS open, no message for 61 s (test
  clock) → close `1000 "idle"`.
- `TestLobbyTokenExpiry`: token issued, never used, 60-second clock
  advance → token evicted from pending map.

### 13.8 Net transport (`internal/net/server_test.go`, `conn_test.go`)

- `TestServerServesHealthz`: GET `/healthz` returns `200 OK` and body
  `"ok"`.
- `TestServerServesVersion`: GET `/version` returns JSON
  `{"server":"...","schemaChecksum":"0x<8hex>"}`.
- `TestServerRejectsLargeFrame`: send a binary frame with `len =
  65535` + 1 byte trailing → reader rejects, conn closes with
  `Close{4003 MALFORMED}`.
- `TestConnSendQueueFullClosesConn`: stall the writer; flood the
  match actor's send call with 65+ snapshots → conn closes with
  `1011 "backpressure"`.
- `TestConnIdleTimeoutClosesConn`: send `MatchJoin`, then no further
  frames for 5 s → `Close{4007 IDLE}`.

### 13.9 End-to-end integration (`cmd/isnipes/main_test.go`)

- `TestE2E_LobbyToMatchOver`:
  1. Start the binary via `httptest.NewServer` with a temporary
     `--addr` and `--max-matches=4`.
  2. Open 2 lobby WS clients; `hello` from each; A `createRoom`; B
     `joinRoom`.
  3. A `startMatch`; both receive `matchStarted` with distinct tokens.
  4. Both open match WS; send `MatchJoin`; receive `MapInit`,
     `Scoreboard`, `Snapshot`, `Event{match_started}`.
  5. Send a scripted input stream from `testdata/e2e/shootout_inputs.json`
     that has A firing E toward B; after ~120 sim ticks, both
     receive `Event{entity_kill, target=B.ID}`.
  6. Within 1 tick after the kill, both receive `Event{match_end,
     reason=LAST_STANDING, actor=A.ID}` and `MatchOver{reason=1,
     winner=A.ID}`. Both match WS close with `1000 ok`.
  7. Lobby WS for both remain open and show the room state
     transitioning OPEN → IN_MATCH → CLOSED in the room list.

Total wall-clock for the integration test: < 8 seconds (it ticks the
match via an injectable test ticker, not real time).

### 13.10 Browser e2e (`web/tests/e2e/shootout.spec.ts`)

Playwright test that mirrors §13.9 in the browser:

1. `npm run dev` style: spawn the Go binary on a random port, point
   `web/dist/` at it, and have it serve the embedded static assets.
2. Open two Chromium browser contexts. Each loads the app at
   `http://localhost:<port>/`.
3. Context A types `Alice`, clicks **Create Room**, clicks **Start**.
4. Context B types `Bob`, clicks **Join Room** with the room's ID.
5. Context A sends WASD inputs to fire at Bob via Playwright's
   keyboard API.
6. After ≤ 8 seconds of wall-clock, Context A renders a
   "VICTORY — Alice" overlay, Context B renders a
   "DEFEAT — last standing: Alice" overlay.
7. Per-context screenshot diffed against a committed golden under
   `web/tests/e2e/__screenshots__/`; tolerance ≤ 5 % per-pixel.

Run target: Playwright with `chromium` only in Phase 2; multi-browser
goldens are Phase 7.

### 13.11 Client unit (`web/tests/proto.test.ts`, `render.test.ts`, `input.test.ts`)

- `proto.test.ts`:
  - `decodes mapinit_baseline.bin` → equals `mapinit_baseline.json`.
  - `decodes snapshot_baseline.bin` → equals `snapshot_baseline.json`.
  - `encodeInput({dir: 'NE', turbo: true, fireDir: 'IDLE',
     clientTick: 12345})` produces a fixture-matching byte array.
  - `SCHEMA_CHECKSUM` equals the hex in `testdata/proto/checksum.txt`.
- `render.test.ts`:
  - Given a fixture `MapInit` + `Snapshot`, the canvas produces a
     hash that matches a committed `render_baseline.hash`.
  - The fixture uses `OffscreenCanvas` so the test is headless;
     vitest config enables `jsdom` + `canvas` polyfill.
- `input.test.ts`:
  - Pressing `↑` produces `{dir: 'N', fireDir: 'IDLE', turbo: false}`.
  - Pressing `↑ + →` produces `{dir: 'NE', ...}`.
  - Pressing `↑ + Space` produces `{dir: 'N', turbo: true}`.
  - Pressing `W + D` while holding `↑` produces
     `{dir: 'N', fireDir: 'NE', turbo: false}`.
  - Pressing `W` and `↑` simultaneously (both intend "north" — a
     binding conflict that the settings UI prevents but the runtime
     must not crash on) produces `{dir: 'N', fireDir: 'N',
     turbo: false}` and emits a console warning.

---

## 14. Testdata layout

```
testdata/
├── proto/
│   ├── checksum.txt                 # the 8-char hex schemaChecksum
│   ├── mapinit_baseline.bin         # bytes for one canonical MapInit
│   ├── mapinit_baseline.json        # decoded equivalent
│   ├── snapshot_baseline.bin
│   └── snapshot_baseline.json
├── e2e/
│   └── shootout_inputs.json         # the integration test's scripted inputs
└── render/
    └── render_baseline.hash         # SHA-256 of the vitest canvas output

web/tests/e2e/__screenshots__/
├── shootout-alice-victory.png
└── shootout-bob-defeat.png
```

All binary fixtures are committed; `-update` flags on the relevant
tests regenerate them.

---

## 15. Determinism rules

Hard rules; violation is a bug:

- The match actor's tick loop is driven by an **injectable ticker**
  in tests (`type Ticker interface{ C() <-chan time.Time; Stop() }`);
  production uses `time.NewTicker`. This is so §13.5 / §13.9 don't
  consume real wall-clock seconds.
- The match actor never calls `time.Now()` from inside `tick()`.
  Wall-clock is only used at the boundaries (token TTL, idle timer)
  and is obtained from an injectable `clock` per the same pattern.
- `internal/proto`, `internal/match`, `internal/lobby` use **no**
  goroutines other than the one explicitly documented in their public
  API (`Match.Run`, `Lobby.Run`). Hidden goroutines are forbidden
  because they make the actor model leaky.
- `Snapshot` byte output is a pure function of the inputs listed in
  §12; not of the wall-clock or of insertion order into intermediate
  maps. The snapshot builder enumerates entities via
  `sim.Sim.Entities()` (which returns them ID-sorted per P1 §5) and
  fills the per-recipient `your_entity_id` based on a lookup, never
  a map iteration over recipients.

---

## 16. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./...` is green on all 4 CI runners | CI |
| 2 | `pnpm -C web test` is green (Chromium) | CI |
| 3 | `pnpm -C web build` succeeds; `web/dist/` is embedded | CI |
| 4 | `make build` produces a single statically-linked binary | CI |
| 5 | Schema checksum is byte-identical Go↔TS | `TestSchemaChecksumValue` + vitest mirror |
| 6 | `MapInit`, `Snapshot` round-trip from committed `.bin` fixtures | §13.4 |
| 7 | `internal/proto` imports only stdlib | `TestProtoImportGraph` |
| 8 | Match actor reaches `MatchOver{LAST_STANDING}` from a scripted PvP shootout | §13.5, §13.9 |
| 9 | Lobby issues unique tokens, rejects re-use, expires after 60 s | §13.6, §13.7 |
| 10 | Match WS rejects: bad checksum (4002), bad token (4001), bad first frame (4003), idle (4007) | §13.6, §13.8 |
| 11 | Playwright Chromium e2e completes lobby→match→`MatchOver` with screenshot diff ≤ 5 % | §13.10 |
| 12 | Bandwidth at 8-player full-fire steady state < 50 KB/s aggregate | derived from §10.4; logged once per match |
| 13 | Server handles `--max-matches=64` simultaneous matches without panic for a 30-second soak | manual smoke (recorded in PR) |

Items 1–11 are gate-able in CI. Item 12 is a logged metric; item 13
is a manual sanity check on the PR.

---

## 17. Risks

- **WebSocket library churn.** `nhooyr.io/websocket` is BSD-licensed
  and stable, but a future Go-stdlib `net/websocket` could be
  attractive. Mitigation: the entire WS dependency is wrapped in
  `internal/net/conn.go`; swapping libraries is a one-file change.
- **Schema-checksum drift between Go and TS.** The string descriptor
  in §5.1 is duplicated in two languages. Mitigation: the
  cross-runtime fixture tests (§13.4) catch the moment they
  disagree, on every CI run.
- **Per-player Snapshot allocation cost.** With 8 players × 15 Hz =
  120 snapshots/sec, naively `make`-ing a fresh byte slice per
  snapshot is 120 short-lived allocations/sec — fine, but each
  snapshot's `your_entity_id` field means we cannot dedup the entity
  body across recipients trivially. Mitigation: precompute the
  shared "Entity bytes" once per tick, then per recipient just
  rewrite the 4-byte `your_entity_id` and 4-byte `server_tick` prefix.
  Implementation note for §9.2's `broadcastSnapshot`.
- **Idle timer flakiness in CI.** Tests that depend on the 5-second
  idle timer in `internal/net` are flake-prone on slow runners.
  Mitigation: all idle / TTL tests use the injectable clock; no test
  uses `time.Sleep(5 * time.Second)`.
- **Playwright cross-runner instability.** Phase 2 runs Playwright on
  `ubuntu-latest` Chromium only. The screenshot golden lives under
  Linux's font rendering, which doesn't reproduce on macOS or
  Windows. Mitigation: keep the Playwright job in CI Linux-only
  through Phase 6; multi-OS golden coverage is Phase 7's problem.
- **Schema-frozen ABI for in-progress matches.** A binary rebuilt
  mid-deploy that changes the schema checksum will break any client
  currently mid-match. Mitigation: deploy procedure (in Phase 8) is
  rolling; Phase 2 itself accepts the limitation since matches are
  ≤ ~3 minutes.

---

## 18. Open questions

1. **Spectator support.** SPEC §1.2 names live-spectator a v1.1
   feature. Phase 2 punts. Default: not built.
2. **Server-side recording of a Phase 2 match for replay.** SPEC §5.5
   mentions per-match replay recording behind `--record`. Default:
   not built in Phase 2; deferred to Phase 5 (which has the full
   event set worth recording).
3. **Authoritative `nick` storage across reconnects.** Phase 2 has no
   reconnect, so `nick` is per-session only. Phase 5 will need to
   persist the nick alongside the `joinToken` for the DC-grace
   window. Default: defer.
4. **`Pong.ts_responder` reference clock.** The spec says "monotonic
   ms". Default: `time.Since(processStart).Milliseconds()` truncated
   to u32 on the server; `performance.now() | 0` on the client.
5. **Embedded `web/dist/` vs reverse-proxy.** Phase 2 ships the
   static bundle embedded via `embed.FS` (single-binary deploy).
   Phase 8 will reconsider for CDN deployment. Default: embed for
   now.
