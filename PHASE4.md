# Phase 4 — Client prediction & reconciliation

This document is the buildable, testable expansion of §8 Phase 4 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–3 are on `main`:
`internal/sim` already exposes the deterministic 30 Hz simulation
(including snipes & generators), `internal/proto` defines the binary
wire schema (`schemaChecksum = 0x42607394`), `internal/match` drives a
match actor end-to-end, `internal/net` carries frames, `internal/lobby`
mints `joinToken`s, and `cmd/isnipes` boots all of the above.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§3.4" point into `SPEC.md`; "P1 §x", "P2 §x", "P3 §x" point into
the earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Phase 4 adds the prediction/reconciliation layer that makes
isnipes feel responsive over real network paths. There are two halves:

- **Server half:** per-entity position history ring, per-connection
  one-way-time (OWT) estimate driven by `Ping`/`Pong`, and lag-
  compensated hit detection for player-fired projectiles per §3.4.
- **Client half:** a 30-entry input ring buffer for the local player,
  a TypeScript subset of the player physics so the client can predict
  its own next-tick state, reconcile-on-`Snapshot` from
  `your_last_input_tick + 1`, and a ~100 ms interpolation buffer for
  non-self entities.

Phase 4 also adds the **realistic-network test harness** (latency,
jitter, TCP stall, backpressure-drop) and the **synthetic-stress**
counterpart, both per §9.

Specifically, Phase 4 ships:

1. `internal/sim/history.go` — per-entity 9-entry position-history ring
   indexed by `server_tick`, retaining `[T_now - 8, T_now]`.
2. `internal/sim/combat.go` — lag-compensated hit-test path for player-
   fired projectiles, gated on a `LagCompTicks` argument.
3. `internal/match/owt.go` — per-connection OWT estimator (EWMA
   α = 0.2) updated from `Pong` arrivals, plus server-initiated `Ping`
   at 2 Hz per §4.3.3.
4. `internal/net/lag_test.go` — realistic-network harness over
   `net.Pipe()` modelling latency (50/100/150 ms), jitter (Gaussian
   ±30 ms), TCP stalls (500 ms), and writer backpressure.
5. `web/src/sim.ts` — TypeScript mirror of player-only physics
   (subtile coordinates, axis-separated wall sweep, generator solids).
6. `web/src/prediction.ts` — input ring buffer + reconcile-on-snapshot
   replay machinery.
7. `web/src/interp.ts` — non-self entity interpolation buffer.
8. `web/src/netClient.ts` — minimal WebSocket client with framing,
   ping/pong, and OWT EWMA (for HUD only — server lag-comp uses the
   server's own estimate).
9. `testdata/sim/physics_constants.txt` — committed file holding the
   physics constants both `internal/sim/config.go` and `web/src/sim.ts`
   read; the parity test in `web/tests/sim_constants.test.ts` and in
   `internal/sim/physics_test.go` asserts both sides agree.

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./internal/sim/...` is green on the four
   SPEC §12-mandated native CI runners (linux/amd64, linux/arm64,
   darwin/arm64, windows/amd64). Phase 1's
   `TestDeterminism_GoldenFingerprint` and Phase 3's
   `TestDeterminism_GoldenFingerprint_Level9` continue to pass byte-
   for-byte against the existing `baseline.hash` and `phase3_pve.hash`
   fixtures — Phase 4 does **not** regenerate them. (Lag-comp is
   per-connection state and is not part of the simulation
   fingerprint; see §17.)
2. `go test -race -count=1 ./internal/match/...` is green: OWT
   plumbing, server-side `Ping` ticker, and the lag-compensated hit
   path round-trip through the match actor.
3. `go test -race -count=1 ./internal/net/...` is green: the
   realistic-network harness in `internal/net/lag_test.go` passes the
   latency/jitter/stall/backpressure scenarios.
4. `go test -tags synthetic -race -count=1 ./...` is green: the
   `synthetic`-tagged extreme tests (snapshot omissions, AOI saturation)
   document client tolerance without gating PRs.
5. `pnpm -C web test` (vitest) is green: `prediction.test.ts`,
   `interp.test.ts`, `sim_constants.test.ts`, `netClient.test.ts`
   exercise the client-side modules. The Phase 2 parity check
   (`proto.test.ts` ↔ `internal/proto/checksum.go`) continues to
   match `0x42607394`.
6. `BenchmarkSimTick_60x40_8P_Level9` (P3) continues to clear its
   ≤ 3 ms/op gate on `ubuntu-latest`. Phase 4's per-tick cost (history
   ring write) is bounded; a new `BenchmarkHistorySnapshot` measures
   the ring write at ≤ 5 µs/op for 256 entities.
7. `TestPredictionConvergesUnderLatency` (vitest): with a synthetic
   server stream applying a fixed 100 ms one-way delay, the local
   player's predicted position converges to the reconciled position
   within one snapshot (~66.7 ms) of any input change. Final
   divergence ≤ 4 subtile units at every tick (the §4.4 threshold).
8. `TestLagCompShotHitsAtRewoundPosition` (Go): a fixture with a
   target at `(x0, y0)` at tick `T0`, target moves to `(x1, y1)` by
   `T0 + 5`. A fire input arrives at `T = T0 + 5` from a player with
   `OWT = 80 ms` (≈ 2 ticks at 30 Hz) → rewind window
   `T_view = T - 2 - 2 = T0 + 1`. Lag-comp test hits the target;
   present-time test does not.
9. `TestLagCompClampedToOldestSample` (Go): a fire with `OWT = 500 ms`
   (≈ 15 ticks) clamps to the oldest available sample at
   `T_now - LAG_COMP_TICKS` and the hit test uses that sample.
10. `TestRealisticNetHarness_Jitter` and `_TCPStall` in
    `internal/net/lag_test.go` close-on-success: the in-process match
    runs through Gaussian-jitter and 500 ms TCP-stall network shims
    without desync (no client-server position divergence > one tile
    once the link recovers).
11. The TypeScript physics mirror is parity-tested against the Go
    reference: `web/tests/sim_constants.test.ts` and
    `internal/sim/physics_test.go::TestPhysicsConstantsTextFile`
    both read `testdata/sim/physics_constants.txt` and assert their
    in-language constants match.
12. Coverage ≥ 80 % statements for `internal/sim` (unchanged P1/P3
    gate), ≥ 70 % for `internal/match` and `internal/net`.

---

## 2. Out of scope

Explicitly **not** built in Phase 4:

- **Reconnect / `MatchJoin` resume / `Resync` frames** — that is the
  Phase 5 contract per §4.7.1. Phase 4 tests treat a backpressure-
  drop as terminal (the client is closed; reconnect is its own
  test list in Phase 5).
- **Lives, respawn, dead-cam, scoreboard semantics** — Phase 5.
- **`EntityDelta` (0x03) frames** — v1 ships full snapshots only; the
  delta wire path is a v1.1 optimisation.
- **Audio / sprites / canvas rendering** — the client modules built
  here (`prediction.ts`, `interp.ts`, `netClient.ts`, `sim.ts`) are
  pure logic with unit tests. The render pipeline (`render.ts`) and
  asset set land in Phase 7.
- **Lag compensation for snipe-fired projectiles** — snipes have no
  client, no OWT, no lag to compensate. Snipe projectiles continue
  to use the present-time hit path (existing P3 behaviour).
- **Mouse/keyboard input glue and keybind UI** — that is the Phase 7
  client polish. Phase 4 generates synthetic input streams in tests.

Phase 4 does **not** change the binary wire schema: `Ping`, `Pong`,
`Snapshot.your_last_input_tick` are all present in the schema from
Phase 2, and the `schemaDescriptor` string in
`internal/proto/checksum.go` is **byte-identical** to today's. The
committed `testdata/proto/checksum.txt` value (`0x42607394`) MUST not
change.

---

## 3. Prerequisites and assumptions

- Phase 1's `internal/sim` public API in `PHASE1.md` §5 plus Phase 3's
  additions (`KindSnipe`, level table, snipe AI). The sim continues
  to be the deterministic source of truth.
- Phase 2's `internal/proto` schema (`Snapshot` already carries
  `YourLastInputTick`; `Ping`/`Pong` already encode/decode).
- Phase 2's `internal/net` server echoes `Ping` → `Pong` on receipt.
  Phase 4 extends this with a server-initiated `Ping` ticker.
- Go 1.22+; `math/rand/v2` PCG (used only on the server, not in lag
  comp).
- Node.js ≥ 20 for vitest. No new third-party Go deps; one new
  client devDependency permitted (`fake-indexeddb` is NOT needed —
  `localStorage` and timers are mocked with vitest's built-in
  `vi.useFakeTimers()`).
- `LAG_COMP_TICKS = 8` (§3.4) and `interp_ticks = 2` (§3.4) are
  defined as named constants in `internal/sim/history.go`.
- `client_tick` is `u16` and wraps modular per §4.3.6; both server
  and client compare with `(a - b) & 0xFFFF` cast to `i16`.

---

## 4. Package and file layout

New files in Phase 4:

```
internal/sim/
├── history.go               # NEW — per-entity position-history ring
├── history_test.go          # NEW
├── physics_test.go          # NEW — TestPhysicsConstantsTextFile

internal/match/
├── owt.go                   # NEW — per-connection OWT EWMA + Ping ticker
├── owt_test.go              # NEW

internal/net/
├── lag_test.go              # NEW — realistic-network harness
├── synthetic_test.go        # NEW — `//go:build synthetic` extreme cases

web/src/
├── sim.ts                   # NEW — TS mirror of player physics
├── prediction.ts            # NEW — input ring buffer + reconcile
├── interp.ts                # NEW — non-self interpolation buffer
└── netClient.ts             # NEW — WebSocket client w/ ping/pong/OWT

web/tests/
├── sim_constants.test.ts    # NEW — Go/TS physics parity
├── prediction.test.ts       # NEW
├── interp.test.ts           # NEW
└── netClient.test.ts        # NEW

testdata/sim/
└── physics_constants.txt    # NEW — Go/TS parity oracle

testdata/replays/
└── phase4_pred.{inputs,hash}  # NEW — see §19
```

Existing files modified by Phase 4:

```
internal/sim/
├── combat.go    # adds LagCompTicks arg; uses History.At(T_view) when
│                # shooter is a player AND LagCompTicks > 0
├── sim.go       # NewSim wires entityHistory; Tick step 4.9 writes a
│                # post-movement snapshot to the history ring before
│                # projectile motion (§6.2); Tick exposes
│                # FireWithLagComp() for the match actor

internal/match/
├── match.go     # boots OWT ticker; on Input(fire), pulls OWT for the
│                # shooter conn and passes it to sim
├── snapshot.go  # unchanged on the wire — YourLastInputTick already
│                # plumbed by P2; verified by integration tests here

internal/net/
├── server.go    # on Pong receipt, dispatches conn-update to OWT
│                # estimator; starts a 500 ms Ping ticker per conn

internal/proto/
└── checksum.go  # UNCHANGED — schemaDescriptor must remain byte-
                 # identical (0x42607394)
```

The `web/src/proto.ts` mirror is unchanged.

---

## 5. Public API additions

### 5.1 Server (`internal/sim`)

```go
// LagCompTicks is the maximum rewind window for lag-compensated hit
// detection, in sim ticks. Per §3.4 == 8 (≈ 267 ms at 30 Hz).
const LagCompTicks = 8

// InterpTicks is the offset added to OWT-derived rewind to account
// for client-side interpolation per §3.4 / §4.4 == 2 ticks.
const InterpTicks = 2

// HistoryDepth is the number of samples the per-entity ring keeps,
// retaining ticks [T_now - LagCompTicks, T_now] inclusive (== 9).
const HistoryDepth = LagCompTicks + 1

// FireOptions augments the queued fire input with optional lag-comp.
// Zero value = no lag compensation (present-time hit detection).
type FireOptions struct {
    // OWTTicks is the per-shooter one-way-time in sim ticks.
    // Caller computes this from match-actor OWT state (§7.4).
    // OWTTicks is clamped to [0, LagCompTicks] inside the sim.
    OWTTicks uint8
}

// Sim.Tick now accepts inputs that carry per-player FireOptions.
// PlayerInput gains an opaque LagComp field; callers that do not
// care about lag-comp leave it zero (present-time behaviour).
type PlayerInput struct {
    PlayerID   EntityID
    Dir        Dir
    Turbo      bool
    FireDir    Dir
    ClientTick uint16

    // Phase 4 addition (zero == no lag comp).
    LagComp FireOptions
}
```

The sim API never exposes its raw history ring; lag-comp resolution
is encapsulated in `combat.go::resolveProjectile`. Tests that exercise
the history ring use unexported helpers from `export_test.go`.

### 5.2 Server (`internal/match`)

```go
// owt.go
type OWTEstimator struct {
    // private: alpha 0.2 EWMA in ms.
}

func NewOWTEstimator() *OWTEstimator
func (o *OWTEstimator) ObservePong(rttMs uint32)
func (o *OWTEstimator) OWTMs() uint32       // current OWT estimate
func (o *OWTEstimator) OWTTicks() uint8     // OWTMs rounded to ticks, clamped to LagCompTicks
```

`MatchConfig` is unchanged on the wire side; internally the match
actor maintains `map[ConnID]*OWTEstimator`.

### 5.3 Client (`web/src/`)

```ts
// sim.ts
export interface MazeView {
  W: number; H: number;
  at(tx: number, ty: number): TileCode;
}

export interface PlayerKinematic {
  x: number; y: number;       // subtile centre coords
  vx: number; vy: number;
  halfExt: number;
}

export function stepPlayer(
  p: PlayerKinematic,
  inp: { dir: Dir; turbo: boolean },
  maze: MazeView,
  solids: ReadonlyArray<{x: number; y: number; halfExt: number}>,
): PlayerKinematic;

// prediction.ts
export class PredictionBuffer {
  constructor(capacity?: number);              // default 30
  push(clientTick: number, input: ClientInput, state: PlayerKinematic): void;
  reconcile(serverTick: number, lastInputTick: number,
            serverState: PlayerKinematic): { replayed: number; diverged: number };
  predicted(): PlayerKinematic;                // latest predicted state
  reset(): void;
}

// interp.ts
export class InterpBuffer {
  constructor(targetLagMs: number);            // default 100
  push(serverTick: number, recvWallMs: number, entities: EntityState[]): void;
  sample(nowWallMs: number): EntityState[];    // returns interpolated state
  clear(): void;
}

// netClient.ts
export class NetClient {
  constructor(url: string, token: string, schemaChecksum: number);
  connect(): Promise<void>;
  sendInput(i: ClientInput): void;
  onSnapshot(fn: (s: Snapshot) => void): void;
  onEvent(fn: (e: Event) => void): void;
  onMatchOver(fn: (m: MatchOver) => void): void;
  owtMs(): number;                             // current client-side OWT EWMA
  close(): void;
}
```

`ClientInput` mirrors the wire `Input` payload (§4.3.2): `dir`,
`turbo`, `fireDir`, `clientTick`. The client never sends
`LagComp` — that is server-side state.

---

## 6. Server: position-history ring

`internal/sim/history.go` introduces a small fixed-capacity ring per
live entity. The ring holds 9 samples covering `[T_now - 8, T_now]`
inclusive, indexed by `server_tick & 0x7` (or `% HistoryDepth` — see
§6.1).

### 6.1 Ring layout

```go
type histSample struct {
    tick uint32  // exact server_tick this sample was written at
    x, y int32   // subtile centre at end of step 4.5 (post-movement)
    flags uint8  // DEAD/SPAWN_INVULN flags at sample time
}

type entityHistory struct {
    // ring length = HistoryDepth = 9.
    samples [HistoryDepth]histSample
    head    uint8    // index of the newest sample; the next write
                     // goes to (head+1) % HistoryDepth.
    count   uint8    // number of valid samples ∈ [0, HistoryDepth]
}
```

Each entry stores its own `tick` so a lookup can verify the sample
genuinely covers the requested tick (a sample whose tick differs from
the requested by ≥ HistoryDepth indicates the ring rolled over and
the requested tick is unrecoverable; lookup returns the oldest valid
sample).

### 6.2 Sampling rule (in `sim.go::Tick`)

The history ring is written **once per tick per live entity**,
**after movement resolution but before projectile motion**. The
existing P3 tick loop:

```
0   preflight
1   serverTick++
2   build input map
3   apply input (fires queued)
4   move players
4.5 step snipe AI
4.55 snipe movement (post-AI)
4.56 snipe-vs-snipe weak block
4.6 generator emission
4.9 NEW — write history snapshot for every live entity   ← Phase 4
5   resolve projectile motion + collision
6   decrement projectile lifetimes
7   player firing
8   respawn timers
9   GC
10  return events
```

Step 4.9 iterates `entityStore.liveIDsSorted()` and writes
`(serverTick, X, Y, Flags)` for each. Players, snipes, generators,
and projectiles all participate; only generators are immobile, but
recording them uniformly costs nothing and simplifies the lookup.

**Why between 4.5/4.6 and 5:** §3.4 says the lag-comp test is "the
projectile's swept segment against each candidate target's
*historical* position at server_tick = T_view." The most natural
T_view sample for a target is its position at the *end* of T_view's
movement phase — i.e. exactly what step 4.9 stores.

### 6.3 Sample lookup

```go
// At returns the sample at tick t for entity e. If t is older than
// the oldest sample the ring holds, At returns the oldest valid
// sample (the §3.4 clamp). ok is false only when the entity has no
// samples at all (newly-spawned, has never seen a step 4.9 write).
func (h *entityHistory) At(t uint32) (histSample, bool)
```

Per §3.4: "A shot whose ideal rewind would exceed the max is tested
at the oldest available sample." This rule is implemented by `At()`
directly; callers do not need to clamp.

### 6.4 Lifecycle

- **Spawn:** an entity's `entityHistory` is created with `count = 0`
  in `entityStore.alloc()`. Its first sample is written at the end of
  the spawn tick's step 4.9.
- **Death:** `entityHistory` is *not* immediately cleared on
  `killEntity`. The samples persist through GC (§13 step 9) for
  exactly one more tick so a projectile fired on the same tick as
  the kill can still rewind and hit. The GC step itself removes the
  entry once the entity slot is freed.
- **Player respawn:** P5 territory, but Phase 4 sets the rule: on
  respawn, the entity's `entityHistory.count` is reset to 0 so old
  samples from the pre-death body cannot be rewound into a hit on
  the freshly-spawned (and `FlagSpawnInvuln`-flagged) entity. The
  `spawn_invuln` filter from P3 is preserved at the rewound-position
  check too: a sample whose `flags` had `SPAWN_INVULN` set rejects
  the hit (§8.3).

### 6.5 Memory budget

`entityHistory` is `9 × 12 bytes payload + 2 bytes head/count = 110`
bytes per live entity. At the 256-entity P1 cap that is ≈ 28 KiB,
fixed cost per match, allocated up-front.

---

## 7. Server: ping / pong + OWT

### 7.1 Ping ticker (server → client)

`internal/net/server.go` already echoes a received `Ping` as `Pong`.
Phase 4 adds a **server-initiated** `Ping` every **500 ms** per §4.3.3.
The ticker is rooted in the per-connection writer goroutine:

```go
ticker := time.NewTicker(500 * time.Millisecond)
for {
    select {
    case <-ticker.C:
        tsOrigin := uint32(time.Now().UnixMilli())
        // remember tsOrigin → owt estimator (the same value will
        // appear in the matching Pong response).
        ping := proto.Ping{TsOrigin: tsOrigin}
        // ...send frame
    case ...
    }
}
```

The client-initiated `Ping` is preserved (server echoes via the
existing P2 path).

### 7.2 Pong reception

When a `Pong{ts_origin, ts_responder}` arrives at the server reader:

1. Compute `rttMs = now() - ts_origin` (using the server's own
   monotonic ms clock; `ts_origin` was originated by the server, so
   the value is in the server's clock domain).
2. Feed `rttMs` into the per-connection `OWTEstimator.ObservePong`.

`ts_responder` is informational only on the server side (the
roundtrip math uses `ts_origin` exclusively to avoid clock-domain
issues). The client uses `ts_responder` to compute its own client-
side RTT in symmetric fashion (§13).

### 7.3 EWMA

```go
func (o *OWTEstimator) ObservePong(rttMs uint32) {
    owt := rttMs / 2
    if o.first {
        o.smoothedMs = owt
        o.first = false
        return
    }
    // α = 0.2, integer-only: new = old*4/5 + sample/5
    o.smoothedMs = (o.smoothedMs*4 + owt) / 5
}
```

Integer math is intentional — the value is converted to ticks before
use and small fractional differences are below the per-tick
quantisation anyway.

### 7.4 OWT → ticks

```go
func (o *OWTEstimator) OWTTicks() uint8 {
    // 33.33 ms/tick → round(ms / 33.33) == round(ms * 3 / 100)
    ticks := (o.smoothedMs*3 + 50) / 100
    if ticks > LagCompTicks {
        return LagCompTicks
    }
    return uint8(ticks)
}
```

Clamped to `LagCompTicks` (= 8) so it never indexes past the ring.
A connection with no observed `Pong` yet returns `0` (OWT estimator
in its first-observation state) — present-time hit detection.

### 7.5 Initial conditions

A brand-new `OWTEstimator` returns `OWTTicks() == 0` until the first
`Pong` arrives. Server-initiated `Ping` fires at the 500 ms boundary
after `MatchJoin`, so the first `OWTTicks > 0` value appears around
the second sim tick after the connection is established.

---

## 8. Server: lag-compensated hit detection

### 8.1 Where lag-comp kicks in

Lag-comp applies **only** to projectiles whose `Shooter` is of kind
`KindPlayer`. Snipe-fired projectiles use the present-time path.
Self-fire (a shooter's projectile against the shooter's own AABB) is
already filtered by P1 and unchanged.

### 8.2 T_view formula and clamp

When `resolveProjectile` examines a candidate `target` at sim tick
`T_now` for a player-fired projectile:

```
owt_ticks = projectile.OWTTicks   // captured at fire time
interp_ticks = InterpTicks         // == 2
T_view = T_now - owt_ticks - interp_ticks
T_view = max(T_view, T_now - LagCompTicks)
```

`T_view` is computed once per `(projectile, tick)`; if the same
projectile flies for multiple ticks, each tick recomputes T_view
(the formula yields `T_now - owt_ticks - 2` clamped — i.e. it slides
forward with `T_now`). In practice this means the projectile's
swept-segment tests target positions a fixed `owt+2` ticks behind
"current" at each motion step. This matches the §3.4 wording
"historical position at server_tick = T_view".

### 8.3 Candidate filtering at rewound positions

```go
for each candidate target:
    sample, ok := store.history(target.ID).At(T_view)
    if !ok { use present position }
    if sample.flags & FlagSpawnInvuln != 0 { skip }
    if sample.flags & FlagDead != 0 { skip }
    test swept segment against AABB centred at (sample.x, sample.y)
```

The `FlagSpawnInvuln` filter at the rewound position is critical:
without it a fire input that pre-dates a respawn-invuln target could
rewind into a moment when the target was a different (now-dead)
instance.

### 8.4 Fire-time OWT capture

When a player's `Input{fire_dir != 0}` is processed in step 7 of
tick T_now, the spawned `projectileState` records the shooter's
**current** `OWTTicks` value. Subsequent ticks of motion use this
captured value, not the live OWT (which may EWMA-drift mid-flight).
The capture is a single `uint8` on `projectileState`.

### 8.5 Determinism

Lag-comp introduces a per-connection state (`OWTTicks`) that is not
part of the deterministic sim seed. Phase 1's
`TestDeterminism_GoldenFingerprint` and Phase 3's `_Level9` fixtures
exercise the **present-time** path (`OWTTicks = 0` for all inputs)
and continue to pass without regeneration.

A new test `TestDeterminism_LagCompFixedOWT` (§18.1) demonstrates
that two sims fed identical inputs **including identical OWTTicks**
produce byte-identical fingerprints. Determinism is preserved over
the joined `(sim seed, input stream, OWT stream)` triple.

### 8.6 Out-of-range / first-tick edge cases

- A projectile fired during the entity's spawn tick has no history
  sample yet for any other entity at T_view. `At()` returns the
  oldest valid sample with `ok = true` once at least one sample
  exists; if literally no samples exist, the test falls back to
  present positions. (In practice this only happens before tick 1.)
- A projectile fired against a target that died T_view+k ticks ago
  but whose `entityHistory` was preserved one extra tick (§6.4):
  `At(T_view)` returns the pre-death sample (with `FlagDead` unset
  on that sample if T_view < death_tick), so the hit registers. This
  is the §3.4 "shot around a corner" semantics — the shooter should
  succeed if the target was alive in their view.

---

## 9. Client: input ring buffer (`web/src/prediction.ts`)

### 9.1 Capacity

30 entries, indexed by `client_tick & 0x1F` (modulo 32 → use
`% RING_CAPACITY` with RING_CAPACITY = 32; we hold 30 valid entries
to leave headroom). The ring is keyed on the **client's own**
monotonic `client_tick` (u16, §4.3.6).

### 9.2 Entry layout

```ts
interface PredEntry {
  clientTick: number;          // u16 modular
  input: ClientInput;          // dir, turbo, fireDir at sample time
  state: PlayerKinematic;      // predicted state AFTER applying input
}
```

The predicted `state` is computed by `sim.ts::stepPlayer` applied to
the previous entry's state plus this entry's input.

### 9.3 Per-tick lifecycle

```
each 33.33 ms:
  read keyboard → ClientInput
  prev = ring.last() ?? localSpawnState
  newState = stepPlayer(prev.state, input, maze, generatorSolids)
  ring.push({ clientTick, input, state: newState })
  net.sendInput({ clientTick, ...input })
```

The render loop draws `ring.last().state` for the local player (no
interpolation lag).

### 9.4 Reconcile-on-snapshot

When a `Snapshot` arrives:

```ts
const { serverTick, yourLastInputTick, yourEntityId, entities } = snap;
const selfServer = entities.find(e => e.id === yourEntityId);
if (selfServer == null) return; // dead-cam handled in §11.4

const buffered = ring.find(e => e.clientTick === yourLastInputTick);
if (buffered == null) {
  // Server consumed an input we no longer have buffered (ring
  // wrap or first-snapshot). Hard-reset to server state.
  ring.reset({ state: selfServer, clientTick: yourLastInputTick });
  return;
}

const dx = selfServer.x - buffered.state.x;
const dy = selfServer.y - buffered.state.y;
if (Math.abs(dx) <= DIVERGE_THRESHOLD &&
    Math.abs(dy) <= DIVERGE_THRESHOLD) return;     // §4.4 step 4

// Diverged. Replay all inputs strictly after yourLastInputTick.
let cur = { state: selfServer, clientTick: yourLastInputTick };
for (const e of ring.iterAfter(yourLastInputTick)) {
  const next = stepPlayer(cur.state, e.input, maze, solids);
  e.state = next;
  cur = e;
}
```

`DIVERGE_THRESHOLD = 4` subtile units (per §4.4 "e.g. > 4 subtile
units").

### 9.5 `client_tick` wraparound

The ring stores entries with their full `clientTick` value modulo
`0x10000` (u16). Lookups use the §4.3.6 modular comparator: an entry
is "after" `yourLastInputTick` iff
`((entry.clientTick - yourLastInputTick) & 0xFFFF)` is in
`[1, 256]`. Older-than-256 entries are stale and skipped.

### 9.6 Cold-start

The very first `Snapshot` arrives before the client has issued any
inputs. `ring.empty == true` → set the local state to the server's
self entity verbatim; no replay needed.

---

## 10. Client: player physics mirror (`web/src/sim.ts`)

### 10.1 Constants

The TypeScript module exports:

```ts
export const SUBTILE_PER_TILE = 256;
export const PLAYER_SPEED = 16;
export const PLAYER_TURBO_SPEED = 32;
export const PLAYER_HALF_EXT = 96;
export const FIRE_COOLDOWN_TICKS = 6;
```

These values **MUST** equal the corresponding constants in
`internal/sim/config.go`. Both sides read `testdata/sim/physics_
constants.txt` in parity tests; see §14.

### 10.2 `stepPlayer`

Mirrors `internal/sim/physics.go::moveAndSlide` for **player kind
only**. The algorithm is identical: axis-separated swept-AABB sweep
against (a) wall tiles via tile-index iteration, (b) generator AABBs
from a supplied solids list.

Inputs:
- `prev: PlayerKinematic` — previous tick's `(x, y, vx, vy)`.
- `inp: { dir, turbo }` — current tick's intent.
- `maze: MazeView` — read-only wall lookup.
- `solids: AABB[]` — generator AABBs; the client tracks these from
  the most recent `Snapshot` (generators are static).

Output: next tick's `PlayerKinematic`.

### 10.3 What's deliberately NOT mirrored

- Snipes (the client treats them as non-blocking; they don't collide
  with the player in the authoritative sim either).
- Other players (P3/SPEC: players pass through each other).
- Projectiles (they only damage; they do not block movement).
- AI, generators' damage state, scoring, lives.

Anything not in §10.1/§10.2 is server-only and reconciled via
snapshots.

### 10.4 No PRNG on the client

The client's `stepPlayer` has no random source. Movement is fully
deterministic given `(prev, input, maze, solids)`.

---

## 11. Client: non-self interpolation buffer (`web/src/interp.ts`)

### 11.1 Target lag

Renders entity state at `t_render = nowWallMs - 100` (default;
configurable for tests).

### 11.2 Snapshot retention

The buffer keeps the **two most-recent** snapshots plus any newer
in-flight. At render time:

```ts
// Find the two snapshots straddling t_render.
const sBefore = buf.last(s => s.wallMs <= tRender);
const sAfter  = buf.first(s => s.wallMs >  tRender);
if (sBefore == null || sAfter == null) {
  // Cold-start or end-of-stream: use the freshest snapshot
  // present directly (no lerp).
  return buf.latest()?.entities ?? [];
}
const alpha = (tRender - sBefore.wallMs) / (sAfter.wallMs - sBefore.wallMs);
```

`alpha ∈ [0, 1]` is computed in **floating point**; the resulting
interpolated `(x, y)` is rounded to the nearest integer subtile.
This is acceptable because the result is purely rendered (never fed
back into a hit test); the spec's "no floats in the sim" applies to
the authoritative sim, not the renderer.

### 11.3 Per-entity matching

Entities are matched across snapshots by `id`. If an entity is in
`sBefore` but not `sAfter`, it is faded over the next 100 ms via a
client-only ghost flag (rendering concern; Phase 7 will use it).

### 11.4 Dead-cam interaction

When `Snapshot.your_entity_id == 0` the recipient is in dead-cam
(P5). Phase 4 simply omits the self entity from the interp buffer
and lets the renderer (P7) overlay a camera. The interp buffer logic
is unchanged.

### 11.5 Stale cleanup

Snapshots older than `(t_render - 1000 ms)` are dropped from the
buffer to bound memory; the buffer holds at most ~30 snapshots
(15 Hz × 2 s).

---

## 12. Client: `netClient.ts`

### 12.1 Responsibilities

- Open a `WebSocket` to `gameSocketPath`.
- Send `MatchJoin{schema_checksum, token}` as first frame.
- Frame encoding/decoding via the existing `proto.ts` mirror.
- Maintain a 2 Hz client-initiated `Ping` ticker (§4.3.3); record
  `(ts_origin, t_send)` for each outgoing ping, derive RTT from the
  matching `Pong.ts_origin` echo.
- EWMA OWT estimator identical to §7.3 (α = 0.2, integer ms). The
  client OWT is HUD-only; the server uses its own.
- Detect 5-second idle (no frame received) → close + emit a
  `client_dc` event for the lobby UI (Phase 7 reconnect glue lives
  in Phase 5).

### 12.2 Wiring

```
NetClient ─ on Snapshot ─→ PredictionBuffer.reconcile + InterpBuffer.push
          ─ on Event    ─→ event subscribers
          ─ on MatchOver→ matchOver subscribers
          ─ on Ping     ─→ immediate Pong
          ─ on Pong     ─→ OWT EWMA update
```

The client never decodes `EntityDelta` (0x03) — v1 only emits
`Snapshot`.

### 12.3 Sequence-number behaviour

§4.3.6 says `seq`/`ack` are diagnostics-only over TCP. The client
increments `seq` per outgoing frame and ignores incoming `seq`
beyond logging. It does not gap-detect.

---

## 13. Sim tick loop changes

Updated tick step list (Phase 4 inserts step 4.9 and modifies step 5
to consult history when the projectile has `OWTTicks > 0`):

```
0   preflight
1   serverTick++
2   build input map
3   apply input (fires queued with OWT capture per §8.4)
4   move players
4.5 step snipe AI
4.55 snipe movement
4.56 snipe-vs-snipe weak block
4.6 generator emission
4.9 NEW — write history snapshot for every live entity (§6.2)
5   resolve projectile motion + collision (uses history.At(T_view)
    for player-fired projectiles with OWTTicks > 0; present-time
    otherwise)
6   decrement projectile lifetimes
7   player firing (capture OWTTicks at fire time)
8   respawn timers
9   GC (entity history retained one extra tick, then cleared)
10  return events
```

**Ordering rationale.** Step 4.9 runs after all movement so the
sample reflects each entity's final position for the tick. This
position is then the "ground truth" any lag-comp query for that
tick will resolve to. Step 5 is the only consumer of the history
during normal operation.

---

## 14. Cross-language parity oracle (`testdata/sim/physics_constants.txt`)

Single-source-of-truth file format:

```
isnipes-physics/v1
subtile_per_tile=256
player_speed=16
player_turbo_speed=32
player_half_ext=96
fire_cooldown_ticks=6
projectile_speed=32
projectile_lifetime=90
generator_half_ext=112
projectile_half_ext=24
```

**Server parity test (`internal/sim/physics_test.go`):**

```go
func TestPhysicsConstantsTextFile(t *testing.T) {
    raw, err := os.ReadFile("../../testdata/sim/physics_constants.txt")
    if err != nil { t.Fatal(err) }
    got := parsePhysicsConstants(string(raw))
    want := map[string]int{
        "subtile_per_tile": subtilePerTile,
        "player_speed":     playerSpeed,
        // ... full set
    }
    for k, v := range want {
        if got[k] != v {
            t.Fatalf("constant %q: file=%d, go=%d", k, got[k], v)
        }
    }
}
```

**Client parity test (`web/tests/sim_constants.test.ts`):**

```ts
import { readFileSync } from "node:fs";
import * as Sim from "../src/sim";

it("matches testdata/sim/physics_constants.txt", () => {
  const raw = readFileSync("../testdata/sim/physics_constants.txt", "utf8");
  const file = parsePhysicsConstants(raw);
  expect(file.subtile_per_tile).toBe(Sim.SUBTILE_PER_TILE);
  expect(file.player_speed).toBe(Sim.PLAYER_SPEED);
  // ... full set
});
```

Either side changing a constant without updating the .txt file
breaks both parity tests in CI.

---

## 15. Match-actor changes (`internal/match`)

### 15.1 OWT plumbing

`match.go` gains:

```go
type Match struct {
    // ... existing P1/P2/P3 fields
    owt map[ConnID]*OWTEstimator
}

func (m *Match) onPongReceived(connID ConnID, rttMs uint32) {
    m.owt[connID].ObservePong(rttMs)
}

func (m *Match) currentInputOWT(playerID EntityID) uint8 {
    conn, ok := m.playerConn[playerID]
    if !ok { return 0 }
    est, ok := m.owt[conn]
    if !ok { return 0 }
    return est.OWTTicks()
}
```

When the match actor consumes an `Input` from the conn-input
channel, it stamps `PlayerInput.LagComp.OWTTicks = m.currentInputOWT
(playerID)` before passing the slice into `sim.Tick`.

### 15.2 Server-initiated Ping

Phase 2's `internal/net/server.go` reader handles inbound `Ping`;
Phase 4 adds an **outbound** `Ping` ticker in the writer goroutine
(§7.1). The match actor does not need to be aware of this — the net
layer schedules the pings; the matching pong arrives at the reader
and is routed via the match actor's existing `onPongReceived` hook.

### 15.3 No additional events on the wire

`Ping`/`Pong` and `Snapshot.YourLastInputTick` are already in the
P2 schema; Phase 4 only adds *meaning* to fields that previously
existed. No new event kinds, no new frame types.

---

## 16. Realistic-network test harness (`internal/net/lag_test.go`)

### 16.1 Architecture

The harness instantiates a `net.Pipe()` between a synthetic client
and the existing `internal/net.Server`. A configurable **shim
function** wraps each direction of the pipe:

```go
type NetShim struct {
    OneWayDelay func() time.Duration   // sample per frame
    DropRate    float64                // 0 in real tests
    StallUntil  time.Time              // pause delivery until this t
}
```

`OneWayDelay` returns a constant for the latency test, a Gaussian
sample (mean 100 ms, σ 30 ms) for the jitter test, etc.

### 16.2 Scenarios

| Scenario | Setup | Pass condition |
|---|---|---|
| `TestNet_FixedLatency_100ms` | shim → 100 ms one-way | Reconcile converges within 1 snapshot (~66.7 ms); divergence ≤ 4 subtile units. |
| `TestNet_FixedLatency_150ms` | shim → 150 ms one-way | Same with extended convergence window (≤ 2 snapshots). |
| `TestNet_Jitter_30ms` | shim → Gaussian(100, 30) | No client-side stutter (≤ 1 frame's worth of interpolation-buffer underrun per second across 30 seconds). |
| `TestNet_TCPStall_500ms` | shim → pause delivery for 500 ms mid-match | After stall ends, reconcile recovers; final position matches authoritative within 4 subtile units. |
| `TestNet_BackpressureDrop` | writer queue (64) artificially saturated; shim halts reads on the C side | Server closes the WS with §11 backpressure rule; test asserts `Close{code: 4007 IDLE}` or §11 drop semantics. Reconnect is **not** tested here (P5). |

### 16.3 Synthetic-tagged tests (`internal/net/synthetic_test.go`)

Build tag `synthetic`. These are NOT PR-gated but are run nightly:

- `TestSynthetic_SnapshotOmissions`: server intentionally drops
  every third snapshot. Client should still render sensibly via
  interp; gap between successive applied snapshots ≤ 200 ms.
- `TestSynthetic_AOICapSaturation`: 100 entities forced into the
  cap; assert §5.3.1 priority order is preserved.

---

## 17. Determinism rules (additions)

Phase 1–3 determinism rules remain. Phase 4 narrows the scope of
"the deterministic core":

- **Sim determinism:** `Sim.Tick(inputs)` remains byte-deterministic
  given `(seed, inputs)`. Phase 4's history ring is a function of
  prior sim state and therefore deterministic.
- **Lag-comp inputs:** `PlayerInput.LagComp.OWTTicks` is a per-call
  argument like any other input. Two sims fed identical
  `(seed, inputs, OWTTicks)` produce identical fingerprints.
  `TestDeterminism_LagCompFixedOWT` exercises this.
- **OWT is per-connection state**, separate from the simulation. It
  is **not** part of `Sim.Fingerprint()`. The fingerprint format
  does not change in Phase 4; existing P1/P3 fixtures continue to
  pass against their committed hashes.
- **Replay fixtures (`baseline`, `phase3_pve`, `phase4_pred`)** all
  use `OWTTicks = 0` in their input stream, exercising the present-
  time path. Lag-comp behaviour is exercised by Go unit tests in
  `combat_test.go` with synthetic OWT values.
- **Client physics determinism:** `web/src/sim.ts::stepPlayer` is
  pure (no PRNG, no time) and unit-tested for parity with the Go
  reference; see §14.

---

## 18. Test plan

All Go tests in `internal/{sim,match,net}/*_test.go`. All client tests
in `web/tests/*.test.ts`. CI uses `go test -race -count=1` and
`pnpm -C web test`.

### 18.1 History ring (`internal/sim/history_test.go`)

- `TestHistory_RingWriteWraps`: write 12 samples (count > 9); the
  ring retains the most-recent 9. Sample tick numbers are correct.
- `TestHistory_At_PresentTick`: after writing samples for ticks
  100..108, `At(105)` returns the sample with `tick == 105`.
- `TestHistory_At_ClampsToOldest`: `At(95)` after writing 100..108
  returns the sample at tick 100 (the oldest valid).
- `TestHistory_At_EmptyReturnsFalse`: a fresh `entityHistory` →
  `_, ok := At(5)`; `ok == false`.
- `TestHistory_DeadFlagPropagates`: a sample written with
  `FlagDead` is preserved across At() calls.

### 18.2 OWT estimator (`internal/match/owt_test.go`)

- `TestOWT_FirstObservation`: first `ObservePong(100)` →
  `OWTMs == 50` (RTT / 2).
- `TestOWT_EWMAConverges`: 100 consecutive `ObservePong(200)` →
  `OWTMs` converges to 100 within 30 observations.
- `TestOWT_TickConversion`: `smoothedMs = 67` → `OWTTicks == 2`;
  `smoothedMs = 50` → `OWTTicks == 2` (50 ms = 1.5 ticks rounds to 2);
  `smoothedMs = 16` → `OWTTicks == 0`.
- `TestOWT_TicksClamped`: `ObservePong(1000)` →
  `OWTTicks == LagCompTicks` (clamped, not 15).
- `TestOWT_ZeroBeforeFirstPong`: brand-new estimator →
  `OWTTicks == 0`.

### 18.3 Lag-compensated combat (`internal/sim/combat_test.go`)

- `TestLagCompShotHitsAtRewoundPosition`: see §1 DoD #8 above.
- `TestLagCompPresentTimeMisses`: same setup but `OWTTicks = 0` →
  no hit (present-time path).
- `TestLagCompClampedToOldestSample`: see DoD #9.
- `TestLagCompSnipeFireUnaffected`: a snipe-fired projectile with
  `OWTTicks = 4` injected (impossible in production but exercises
  the gate) is **not** lag-compensated — the gate is `shooter ==
  KindPlayer`.
- `TestLagCompSpawnInvulnAtRewoundTick`: a target that spawned 6
  ticks ago (still `SPAWN_INVULN` at T_view) → skipped, no hit.
- `TestLagCompSelfFireStillBlocked`: a player firing at their own
  current position with `OWTTicks = 4` → no hit (existing self-fire
  filter is independent of rewind).
- `TestDeterminism_LagCompFixedOWT`: two sims with identical
  `(seed, inputs, OWTTicks)` produce per-tick byte-identical
  fingerprints across 600 ticks.

### 18.4 Sim integration (`internal/sim/sim_test.go`)

- `TestHistoryWrittenEveryTick`: after `Sim.Tick()` × 20, each live
  entity has 9 valid samples for ticks T-8..T (exposed via
  `export_test.go`).
- `TestHistoryClearedOnDeath`: kill a player → its `entityHistory`
  is GC'd after one tick of grace per §6.4.

### 18.5 Match integration (`internal/match/match_test.go`)

- `TestMatch_PingPongDrivesOWT`: connect a synthetic client, send
  a `Pong{ts_origin=1000}` when the match's monotonic clock is at
  1080 → match's per-conn OWT EWMA observes `RTT=80, OWT=40`.
- `TestMatch_FireUsesOWT`: synthetic conn with stamped OWT = 60 ms
  (2 ticks). The match actor's queued `PlayerInput` for the next
  fire carries `LagComp.OWTTicks == 2`.
- `TestMatch_OWTPerConn`: two conns at different RTTs; their
  `currentInputOWT` values differ; cross-conn fires use the right
  OWT per shooter.
- `TestMatch_ServerSendsPingAt2Hz`: scrape outbound frames over 2
  simulated seconds; expect 4 ± 1 `Ping` frames.

### 18.6 Realistic-network harness (`internal/net/lag_test.go`)

Tests enumerated in §16.2.

### 18.7 Synthetic stress (`internal/net/synthetic_test.go`,
build tag `synthetic`)

Tests enumerated in §16.3. Not PR-gated.

### 18.8 Client prediction (`web/tests/prediction.test.ts`)

- `TestPrediction_RingWraps`: push 35 entries; ring holds the most-
  recent 30.
- `TestPrediction_NoSnapshotYet`: `ring.predicted()` returns the
  last predicted state.
- `TestPrediction_ConvergesUnderLatency` (DoD #7): synthetic
  `Snapshot` stream delayed 100 ms; divergence ≤ 4 subtile units at
  every tick.
- `TestPrediction_ReconcileReplaysFromLastInputTick`: feed an
  input ring with 5 entries; deliver a snapshot for input #2 with a
  diverged self-state. Inputs #3, #4, #5 are replayed; ring entries
  for those ticks are updated.
- `TestPrediction_HardResetOnMissingBufferedTick`: snapshot's
  `your_last_input_tick` is older than the ring's oldest entry → the
  client hard-resets to the server state (no replay).
- `TestPrediction_ClientTickWraparound`: client_tick wraps from
  0xFFFF → 0x0000 mid-replay; modular comparator selects the
  correct entries.

### 18.9 Interpolation buffer (`web/tests/interp.test.ts`)

- `TestInterp_LerpBetweenTwoSnapshots`: 2 snapshots at wall ms 0
  and 66; sample at `nowWallMs - 100` mid-way returns lerped state.
- `TestInterp_SingleSnapshotCold`: only 1 snapshot present →
  `sample` returns it verbatim.
- `TestInterp_StaleSnapshotsDropped`: feed 50 snapshots over a
  simulated 4 s; buffer retains ~30.
- `TestInterp_EntityDisappears`: entity present in snapshot N,
  absent in N+1; interp tags it as `ghost` for one render.

### 18.10 Net client (`web/tests/netClient.test.ts`)

- `TestNetClient_HandshakeSendsMatchJoin`: connect → first frame
  is `MatchJoin{schema_checksum, token}`.
- `TestNetClient_PingTicker`: with fake timers, advance 2 s → 4
  client-initiated `Ping`s sent.
- `TestNetClient_OWTAfterPong`: send `Pong{ts_origin=1000}` while
  client clock reads 1080 → `owtMs == 40`.
- `TestNetClient_5sIdleTimeoutCloses`: no frame received for 5 s →
  WS closed; reconnect is Phase 5.

### 18.11 Physics parity (`web/tests/sim_constants.test.ts` and
`internal/sim/physics_test.go`)

See §14.

### 18.12 Benchmarks (`internal/sim/bench_test.go`)

- `BenchmarkHistorySnapshot`: write 256 entity samples; target
  ≤ 5 µs/op.
- `BenchmarkSimTick_60x40_8P_Level9` (P3): re-run; verify the new
  step 4.9 cost is absorbed inside the existing 3 ms budget.

---

## 19. Testdata

```
testdata/
├── proto/checksum.txt           # UNCHANGED (= 0x42607394)
├── mazes/                       # P1 hashes, unchanged
├── sim/
│   └── physics_constants.txt    # NEW — Go/TS parity oracle (§14)
└── replays/
    ├── baseline.{inputs,hash}        # P1/P3 — UNCHANGED
    ├── phase3_pve.{inputs,hash}      # P3 — UNCHANGED
    └── phase4_pred.{inputs,hash}     # NEW — 4 players × 600 ticks,
                                      # OWTTicks=0 throughout, exercises
                                      # the history-ring write path. Hash
                                      # is the Phase-3 fingerprint format
                                      # (no Phase-4-specific bytes added).
```

`phase4_pred.inputs` records a varied 4-player scripted input set
including fires, turbo, and direction reversals; it does not exercise
lag comp (OWT = 0). The fingerprint hash file is regenerable via the
`-update` flag on `TestDeterminism_GoldenFingerprint`.

---

## 20. Risks

- **Server step 4.9 cost.** 256-entity history write at 30 Hz must
  not push `BenchmarkSimTick_60x40_8P_Level9` past 3 ms/op.
  Mitigation: each ring write is 4 int32 writes; estimated 0.1 µs
  per entity, 30 µs total at 256 entities — well below budget.
  `BenchmarkHistorySnapshot` measures this directly.
- **OWT EWMA convergence on jittery links.** α = 0.2 takes ~30
  observations (~15 s at 2 Hz) to fully converge. Mitigation:
  reasonable — first-observation seeds the EWMA directly, so OWT is
  approximately right within one ping interval (500 ms). Tests cover
  the convergence rate.
- **Client physics drift from server.** Any change to
  `internal/sim/physics.go` movement that is not mirrored in
  `web/src/sim.ts` will silently desync; reconcile will kick in but
  feel laggy on every tick. Mitigation: the parity oracle file
  (§14) catches constant drift; behaviour drift is caught by the
  `TestPrediction_ConvergesUnderLatency` test running the client
  against a real server in vitest's `node` runtime.
- **u16 client_tick wraparound mid-match.** A 36-minute match crosses
  the wrap. Mitigation: the §9.5 modular comparator is unit-tested.
- **Backpressure → drop is harsh.** Phase 4's harness closes the WS
  on backpressure-drop; without Phase 5 reconnect the user just sees
  "disconnected". That is the documented v1 behaviour (§11) and
  acceptable for Phase 4.
- **History-after-death window.** §6.4's "one extra tick" rule could
  in principle let a stale projectile hit a respawned entity at the
  same slot. Mitigation: respawn resets `entityHistory.count = 0`
  (§6.4); the `SPAWN_INVULN` filter at rewound positions (§8.3)
  closes the same window.

---

## 21. Open questions

These are flagged for codex review and for the author to decide
before implementation begins. Defaults are listed if no decision is
forced.

1. **Lag-comp every-tick vs. fire-time-only.** §3.4 reads
   ambiguously; this spec interprets it as "every projectile-motion
   tick of a player-fired projectile rewinds by the captured
   `OWTTicks + InterpTicks`." Default: every tick, capped by ring
   depth. Switching to fire-time-only would be a one-flag tweak.
2. **EWMA α value.** §3.4 mandates 0.2. Some netcode references use
   1/8 (= 0.125). Default: stick to 0.2; tests pin the exact value
   via `TestOWT_EWMAConverges`.
3. **Client interpolation lag (100 ms).** Tunable at runtime via the
   `InterpBuffer` constructor; the default 100 ms is what §4.4
   specifies. Adjusting it later for a "competitive" preset (50 ms
   buffer, more pop) is v1.1 if asked.
4. **Predicted-state divergence threshold.** §4.4 says "e.g. > 4
   subtile units". This spec hard-codes 4. Larger thresholds reduce
   reconciliation churn but allow visible rubber-banding. Default: 4.
5. **Reading `testdata/sim/physics_constants.txt` from vitest.**
   The path is relative to `web/`. CI works directories from the
   repo root; this requires either a `__dirname`-based resolver or
   a build step that copies the file into `web/testdata/`. Default:
   `__dirname`-based resolver — simpler, no copy step.

---

## 22. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./internal/sim/...` green; P1 and P3 fingerprint goldens unchanged | CI |
| 2 | `go test -race -count=1 ./internal/match/...` green | CI |
| 3 | `go test -race -count=1 ./internal/net/...` green (incl. realistic-net harness) | CI |
| 4 | `go test -tags synthetic -race -count=1 ./...` green | CI |
| 5 | `pnpm -C web test` green (incl. parity, prediction, interp, netClient) | CI |
| 6 | `BenchmarkSimTick_60x40_8P_Level9` ≤ 3 ms/op; `BenchmarkHistorySnapshot` ≤ 5 µs/op | CI bench gate |
| 7 | `TestPredictionConvergesUnderLatency` shows divergence ≤ 4 subtile units under 100 ms delay | §18.8 |
| 8 | `TestLagCompShotHitsAtRewoundPosition` and `_PresentTimeMisses` both pass | §18.3 |
| 9 | `TestLagCompClampedToOldestSample` passes | §18.3 |
| 10 | `TestRealisticNetHarness_Jitter` and `_TCPStall` pass | §18.6 |
| 11 | Physics-constants parity test passes Go ↔ TS | §14, §18.11 |
| 12 | Coverage ≥ 80 % `internal/sim`; ≥ 70 % `internal/match` and `internal/net` | CI |
| 13 | `schemaChecksum` unchanged from Phase 2 (`0x42607394`) | `TestSchemaChecksumValue` |

Items 1–13 are gate-able in CI.

The wire protocol is unchanged from Phase 3, so the existing Phase 2
vitest mirror under `web/tests/proto.test.ts` continues to pass
without modification.
