# Phase 5 — Multi-player match completeness

This document is the buildable, testable expansion of §8 Phase 5 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–4 are on `main`:
`internal/sim` already exposes the deterministic 30 Hz simulation
(player + snipes + generators), the §6 position-history ring, and the
§10.5 respawn-tile selector; `internal/proto` defines the binary wire
schema (`schemaChecksum = 0x42607394`); `internal/match` drives a
match actor end-to-end with per-connection OWT estimators;
`internal/net` carries frames with server-initiated Ping; `internal/
lobby` mints `joinToken`s; and `cmd/isnipes` boots all of the above.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§3.9" point into `SPEC.md`; "P1 §x", "P2 §x", "P3 §x", "P4 §x"
point into the earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Phase 5 upgrades the multi-player match shipped in Phase 2
(with the AI extensions of Phase 3 and the network smoothing of
Phase 4) into a complete match lifecycle: lives, respawn,
**spawn invulnerability**, scoring, dead-cam, reconnect within the
30 s DC-grace, late-join policy, the §3.8.1 end-reason precedence
order with a 10-minute hard timer, and the §5.3.1 AOI selection with
hysteresis. Specifically, Phase 5 ships:

1. `internal/sim/lives.go` — per-player **lives**, **score**, and
   **spawnInvuln** state plumbed through the sim's death/respawn
   pipeline. `playerState` gains `livesRemaining`, `score`,
   `spawnInvulnUntil` (server tick), and `eliminated` (zero-lives
   sentinel).
2. `internal/sim/sim.go` — Step 7 (`killEntity`) decrements lives,
   scores the killer per §3.8 deltas, sets `eliminated` when lives
   reach 0, and skips the respawn schedule for eliminated players.
   Step 8 (respawn fire) sets `FlagSpawnInvuln` and stamps
   `spawnInvulnUntil = serverTick + spawnInvulnTicks`. A new step
   8.5 clears `FlagSpawnInvuln` when `serverTick >= spawnInvulnUntil`.
3. `internal/match/match.go` — `Slot` gains `LivesRemaining`,
   `Score`, `DeadCam`, `DC`, `DCDeadlineTick`. The match actor
   handles three new control messages: `ctlDC` (WS dropped),
   `ctlReconnect` (fresh WS presents the same token within grace),
   and `ctlDeadCamInput` (post-elimination input). The §3.8.1
   end-reason precedence is implemented in full, including the
   10-minute `TIMER` fallback and the lives-based
   `ALL_ELIMINATED` / `LAST_STANDING` rules.
4. `internal/match/aoi.go` — per-recipient snapshot builder
   implementing §5.3.1 (priority 1–7), the 12-tile / 22-tile
   hysteresis bands, and the dead-cam unfiltered variant.
5. `internal/match/reconnect.go` — DC-grace tracking, token-replay
   admission within 30 s, and `Resync` → `MapInit` → `Snapshot` →
   `Scoreboard` re-send on accept.
6. `internal/net/server.go` — on WS close mid-match, dispatches
   `ctlDC` to the match actor with the slot's `PlayerID`. On a
   subsequent `MatchJoin` with a token whose match-state is
   `DC-grace`, the net layer routes to `ctlReconnect` instead of
   the standard `ctlJoin`.

Phase 5 also wires the **per-tick scoreboard delta detector** that
emits a fresh `Scoreboard` frame whenever any player's `lives` or
`score` changes, rate-limited to ≤ 5 Hz (one Scoreboard per 6 sim
ticks at 30 Hz).

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./internal/sim/...` is green on the four
   SPEC §12-mandated native CI runners (linux/amd64, linux/arm64,
   darwin/arm64, windows/amd64). The Phase 1
   `TestDeterminism_GoldenFingerprint` baseline and the Phase 3
   `TestDeterminism_GoldenFingerprint_Level9` fixture are
   **regenerated** here to include the new `playerState.score`,
   `livesRemaining`, `spawnInvulnUntil`, and `eliminated` fields in
   the SHA-256 ordering (§18.1). Both new goldens are committed.
2. `go test -race -count=1 ./internal/match/...` is green: lives,
   respawn, score, dead-cam, reconnect, AOI, and the timer/last-
   standing/all-eliminated end-reason precedence each have direct
   tests.
3. `go test -race -count=1 ./internal/net/...` is green: WS drop
   mid-match routes to `ctlDC`; same-token reconnect within 30 s
   restores the slot; reconnect past 30 s returns `Close{4001 AUTH}`.
4. `pnpm -C web test` is green: no client-side wire changes in
   Phase 5, but `web/tests/proto.test.ts` continues to verify
   `schemaChecksum = 0x42607394` and the existing P4 prediction /
   interp / netClient tests still pass.
5. A new committed replay fixture
   `testdata/replays/phase5_4p_pvp.{inputs,hash}` reproduces byte-
   for-byte on the four CI runners. It encodes a scripted 4-player
   PvP match on a non-PvE level (no snipes, no generators) at level
   `A1` (`PlayerLives = 9`) and runs ~2000 ticks (~66 s) including
   multiple kills, respawns, spawn-invulnerability windows, and one
   player reaching zero lives → dead-cam.
6. `BenchmarkMatchTick_8P_Level9` (8 active players + 11 generators
   + level-9 snipe cap + lives/score/AOI active): ≤ **4 ms/op** on
   `ubuntu-latest` without `-race`, ≤ **16 ms/op** under `-race`.
   The +1 ms over P3's 3 ms cap covers per-recipient AOI sorting
   (8 × 64-entity heap, ~16 µs each) and per-tick scoreboard delta
   detection (~5 µs).
7. `TestMatch4PlayerScoreboardE2E` (`internal/match`): a 4-client
   scripted 30 s match drives the scoreboard through 5+ kill events,
   1+ respawn cycle, and at least one player reaching zero lives;
   the final `MatchOver` payload matches a committed
   `testdata/replays/phase5_4p_pvp.matchover.json` fixture.
8. `TestDeadCamSnapshotIsUnfiltered` (`internal/match`): a recipient
   in dead-cam receives a snapshot whose entity list contains every
   live entity within the 64-cap (priorities 1–6 collapsed),
   filled by distance from the map centre (priority 7 tiebreak).
   `your_entity_id == 0`.
9. `TestReconnectWithinGrace` (`internal/net`): kill a client at
   tick T0 (mid-match); reconnect at T0 + 20 s with the same
   `joinToken` on a fresh WS; the new WS receives the §4.7.1
   `Resync` → `MapInit` → `Snapshot` → `Scoreboard` sequence and
   the entity's `(X, Y, HP, lives, score)` match exactly the values
   captured at the moment of drop. The pre-existing players see one
   `player_dc` event followed by one `player_rejoin` event.
10. `TestReconnectPastGraceRejected` (`internal/net`): drop and do
    not reconnect; at the 30 s boundary the slot is terminated,
    `player_leave{reason: timeout}` is broadcast, and a follow-up
    `MatchJoin` with the same token gets `Close{code: 4001, reason:
    "AUTH"}`.
11. `TestLateJoinRejected` (`internal/match`): a `MatchJoin` issued
    while the match is in `StateLive` is rejected with
    `Close{4001 AUTH}` (the late-join path is the same admission gate
    as the unknown-token path).
12. `TestAOIPriorityCapAndHysteresis` (`internal/match`): a fixture
    seeded with 8 players + 9 generators + 50 snipes + 30
    projectiles (97 entities total; > 64 cap) verifies every
    snapshot satisfies §5.3.1 priorities (a)–(g) for every
    recipient.
13. `TestEndReasonPrecedence` (`internal/match`): four sub-tests
    drive simultaneous end-conditions and assert the right
    precedence wins each time —
    `PvECompleteWinsAllElimTie`, `AllElimOverTimer`,
    `LastStandingOnlyAtStartCountGE2`, `TimerFiresAt10Min`.
14. Coverage ≥ 80 % statements for `internal/sim` (unchanged P1/P3
    gate), ≥ 75 % for `internal/match` (raised from 70 % since
    Phase 5 lands a large chunk of match-actor surface), ≥ 70 % for
    `internal/net`.
15. `schemaChecksum` unchanged from Phase 2 (`0x42607394`); no
    `proto/checksum.go` edits.

---

## 2. Out of scope

Explicitly **not** built in Phase 5:

- **Full lobby polish** — `roomList` live push deltas, lobby chat,
  kick-host, `?room=ABCD23` deep-links: that is Phase 6 per §8.
  Phase 5 keeps the Phase 2 lobby as-is.
- **`EntityDelta` (0x03) frames** — the snapshot path remains full
  snapshots only. Delta-encoding is a v1.1 optimisation (§4.3.2 row).
- **Audio / sprite / canvas rendering** — Phase 7. Phase 5's
  client-visible behaviour is verified through the existing proto
  decode tests; no new render surface lands here.
- **AI-AFK / bot fill** — §4.7.1 explicitly says "no AI-AFK state";
  bots are a v1.1 candidate.
- **Live spectator** — distinct feature per §1.2; dead-cam is
  available only to players who *played* this match.
- **Server-side anti-cheat beyond §4.5 baselines** — extended cheat
  detection is v1.1 (§13).
- **Lag-comp for snipe-fired projectiles** — unchanged from P4;
  snipes still use present-time hit detection.

Phase 5 does **not** change the binary wire schema. `MatchOver`,
`Scoreboard`, `Resync`, `MapInit`, `Event`, and the close-code set
in §4.3.5 are all present from Phase 2; this phase only populates
their fields with the values the spec requires. The
`schemaDescriptor` string in `internal/proto/checksum.go` is
**byte-identical** to today's.

---

## 3. Prerequisites and assumptions

- Phase 1's `internal/sim` public API in `PHASE1.md` §5 plus Phase 3's
  level table plus Phase 4's history ring. The sim continues to be
  the deterministic source of truth.
- Phase 2's `internal/proto` schema (`MatchOver`, `Scoreboard`, and
  `Resync` already encode/decode round-trip).
- Phase 2's `internal/net` server already authenticates `MatchJoin`
  and posts `ctlJoin` / `ctlClose` to the match actor. Phase 5
  layers `ctlDC` and `ctlReconnect` over the existing dispatch.
- `LevelParams.PlayerLives` is already populated by §3.7's level
  table (`max(1, 10 - number)`). Phase 5 just consumes it.
- Go 1.22+; `math/rand/v2` PCG. No new third-party Go deps.
- `spawnInvulnTicks = 60` (2 s at 30 Hz) per §3.9. The existing
  `internal/sim/config.go` constant of the same name is **changed
  from 15 to 60**. The Phase 3 value (15) was forward-compat; §3.9
  binds 2 s, and 60 ticks at 30 Hz delivers that exactly.
- `MatchTimerTicks = 18000` (10 minutes at 30 Hz) per §3.8.1's
  default. Hard-coded in `internal/match/match.go`.
- `DCGraceTicks = 900` (30 s at 30 Hz) per §4.7.1.
- `ScoreboardMinIntervalTicks = 6` (5 Hz cap at 30 Hz) per §4.3.2's
  `Scoreboard` row.
- `AOIHysteresisProj = 12` tiles; `AOIHysteresisSnipeGen = 22` tiles
  per §5.3.1's hysteresis bullet.
- `AOIInnerProj = 10` tiles; `AOIInnerSnipeGen = 20` tiles per
  §5.3.1.
- `AOIMaxEntries = 64` per §5.3.

---

## 4. Package and file layout

New files in Phase 5:

```
internal/sim/
├── lives.go                # NEW — lives/score/eliminated helpers
├── lives_test.go           # NEW

internal/match/
├── aoi.go                  # NEW — §5.3.1 priority builder
├── aoi_test.go             # NEW
├── reconnect.go            # NEW — DC-grace + token-replay admission
├── reconnect_test.go       # NEW
├── deadcam.go              # NEW — dead-cam slot machinery
├── deadcam_test.go         # NEW
├── score_delta.go          # NEW — per-tick scoreboard delta detector
├── score_delta_test.go     # NEW

testdata/replays/
├── phase5_4p_pvp.inputs           # NEW
├── phase5_4p_pvp.hash             # NEW
└── phase5_4p_pvp.matchover.json   # NEW
```

Existing files modified by Phase 5:

```
internal/sim/
├── config.go    # spawnInvulnTicks 15 → 60; new FlagDeadCam not added
│                # (dead-cam is match-actor state, NOT a sim flag — see §6.4)
├── entity.go    # playerState gains livesRemaining/score/
│                # spawnInvulnUntil/eliminated fields
├── sim.go       # killEntity awards score + decrements lives;
│                # respawn sets FlagSpawnInvuln + spawnInvulnUntil;
│                # new step 8.5 clears the flag when timer elapses
├── fingerprint.go  # extend the SHA-256 ordering to cover the new
│                   # playerState fields (regenerate goldens)

internal/match/
├── match.go         # Slot gains LivesRemaining/Score/DeadCam/DC/
│                    # DCDeadlineTick; new ctlDC, ctlReconnect, ctlDeadCamInput
│                    # control msgs; tick() drains scoreboard deltas;
│                    # evaluateMatchEnd switches to lives-based rule and
│                    # adds TIMER and SERVER_ERROR.
├── snapshot.go      # per-recipient builder delegates to aoi.go;
│                    # dead-cam path uses aoiDeadCamFor() instead.
├── joinauth.go      # admission gate now consults the reconnect table
│                    # before falling back to the bySession path.
├── registry.go      # garbage-collects matches in StateEnded after a
│                    # 5-second linger window so reconnect tokens have a
│                    # chance to fail cleanly.

internal/net/
├── server.go        # on WS close mid-match: SubmitDC instead of
│                    # SubmitClose; on subsequent MatchJoin: try
│                    # reconnect first, fall back to standard join.

internal/proto/
└── checksum.go      # UNCHANGED — schemaDescriptor must remain byte-
                     # identical (0x42607394).
```

---

## 5. Public API additions

### 5.1 Sim (`internal/sim`)

```go
// LivesRemaining returns the player's remaining lives (≥ 0).
// Returns 0 for unknown IDs and for snipes/generators/projectiles.
func (s *Sim) LivesRemaining(id EntityID) uint8

// Score returns the player's current score (may be negative due to
// the §3.8 death penalty). Returns 0 for non-player IDs.
func (s *Sim) Score(id EntityID) int32

// Eliminated reports whether the player has reached 0 lives. Once
// true, the value never returns to false (the slot remains in the
// sim's player map for fingerprint determinism even if the entity
// has been GC'd from the slab).
func (s *Sim) Eliminated(id EntityID) bool

// SpawnInvulnUntil returns the server tick at which the player's
// FlagSpawnInvuln will be cleared. Returns 0 if the player is not
// currently invulnerable.
func (s *Sim) SpawnInvulnUntil(id EntityID) uint32

// Scores returns a snapshot of every player's (LivesRemaining,
// Score, Eliminated). Returned map is freshly allocated; safe to
// retain. Iteration order in the returned map is non-deterministic
// — callers needing a fixed order must sort by EntityID.
func (s *Sim) Scores() map[EntityID]PlayerScore

type PlayerScore struct {
    Lives      uint8
    Score      int32
    Eliminated bool
}
```

`Config` is unchanged. `LevelParams.PlayerLives` is consumed during
`NewSim` to seed every `playerState.livesRemaining`.

### 5.2 Match (`internal/match`)

```go
// SubmitDC reports that a player's WS has closed (mid-match) and the
// slot should enter DC-grace per §4.7.1. Idempotent: a second SubmitDC
// for the same player before reconnect is a no-op.
func (m *Match) SubmitDC(playerID sim.EntityID)

// SubmitReconnect attempts to re-bind a fresh WS to an existing
// DC-graced slot. Returns the PlayerID on success or an error
// (ErrAuth for unknown/expired token, ErrNotDC for a slot not in
// DC-grace, ErrMatchEnded if the match has terminated).
func (m *Match) SubmitReconnect(token string, out chan<- OutboundFrame) (sim.EntityID, error)
```

The existing `SubmitClose` is retained for clean exits (e.g.
post-`MatchOver`); `SubmitDC` is the new mid-match drop path.

### 5.3 Net (`internal/net`)

No new public types. `Server` gains an internal helper:

```go
// resolveJoinPath decides whether an incoming MatchJoin is a fresh
// join (token still in bySession), a reconnect (token belongs to a
// slot currently in DC-grace), or AUTH-rejected.
func (s *Server) resolveJoinPath(matchID, token string) joinPath
```

---

## 6. Lives, score, and the death pipeline (sim side)

### 6.1 `playerState` additions

```go
type playerState struct {
    fireCooldown      uint8
    lastDir           Dir
    lastInputTick     uint16
    respawnAt         uint32
    deathX            int32
    deathY            int32
    hasRespawnAt      bool

    // Phase 5 additions:
    livesRemaining    uint8  // initialised from LevelParams.PlayerLives
    score             int32
    spawnInvulnUntil  uint32 // server tick at which FlagSpawnInvuln clears (0 = not invuln)
    eliminated        bool
}
```

### 6.2 Initialisation

`NewSim` seeds `livesRemaining = levelParams.PlayerLives` for every
`Config.PlayerIDs[i]`. `score = 0`. `eliminated = false`.
`spawnInvulnUntil = 0` (the *initial* spawn is invulnerable for
60 ticks too — the first respawn fire in step 8 sets the flag
explicitly).

For Phase 2 PvP-only mode (`LevelLetter == 0`), `PlayerLives` is not
populated by the level table; default to **3 lives** in this mode
(consistent with the original game's free-for-all expectation).
This is hard-coded in `NewSim` when `cfg.LevelLetter == 0`.

### 6.3 `killEntity` extensions

The existing `killEntity` (P3 §10.4) is amended to:

```go
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

        // §3.8 score deltas.
        s.awardKill(killer, KindPlayer)
        ps.score -= 5 // §3.8 death penalty (own score).

        // §3.9 lives.
        if ps.livesRemaining > 0 {
            ps.livesRemaining--
        }
        if ps.livesRemaining == 0 {
            ps.eliminated = true
            ps.hasRespawnAt = false // skip respawn schedule
        } else if !s.cfg.NoRespawn {
            ps.respawnAt = s.serverTick + respawnTimerTicks
            ps.hasRespawnAt = true
        }
    case KindGenerator:
        events = append(events, Event{Kind: EventGeneratorDestroyed, Actor: killer, Target: e.ID, Reason: 0})
        s.awardKill(killer, KindGenerator)
    case KindSnipe:
        if ss := s.store.snipes[e.ID]; ss != nil {
            ss.aiState = AIStateDead
        }
        s.awardKill(killer, KindSnipe)
    }
    return events
}
```

`awardKill(killer, victimKind)` looks up `killer` in
`s.store.players`; if found, credits `+1/+10/+25` per §3.8 depending
on `victimKind`. Killer = 0 (environment / timeout) awards nothing.
Friendly-fire is not a concern in v1: a player-fired projectile that
hits the same player's own generator is impossible (generators are
not owned per-player).

The `+25` PvP kill reward applies to the *killer*. The −5 death
penalty applies to the *victim*. If a player suicides via wall
ricochet (impossible in v1 — projectiles don't ricochet), the −5
still applies but no +25 is awarded (killer == victim is treated as
killer == 0 for award purposes).

### 6.4 Spawn invulnerability

`spawnInvulnTicks` is changed from 15 to **60** in `config.go`. The
existing P3 spawn-invuln machinery at NewSim-time becomes: at sim
construction (after every player is placed), set
`spawnInvulnUntil = spawnInvulnTicks` for every player, and set
`e.Flags |= FlagSpawnInvuln`.

In `Sim.Tick`, a new **step 8.5** runs after step 8 (respawn) and
before step 9 (GC):

```go
// Step 8.5: clear spawn invulnerability when its timer elapses.
for _, id := range s.store.liveIDsSorted() {
    idx := s.store.findByID(id)
    if idx < 0 {
        continue
    }
    e := &s.store.slots[idx]
    if e.Kind != KindPlayer {
        continue
    }
    ps := s.store.players[id]
    if ps.spawnInvulnUntil != 0 && s.serverTick >= ps.spawnInvulnUntil {
        e.Flags &^= FlagSpawnInvuln
        ps.spawnInvulnUntil = 0
    }
}
```

Step 8 (the respawn fire) is amended to also stamp the new
invulnerability window:

```go
ps.spawnInvulnUntil = s.serverTick + spawnInvulnTicks
e.Flags |= FlagSpawnInvuln
```

The cannot-fire-while-spawn-invuln rule per §3.9 is enforced by
`combat.go::resolveProjectile`'s existing `FlagSpawnInvuln` filter
(which already excludes invulnerable targets from being hit) plus a
new gate in `sim.go`'s fire-input handler that drops fire inputs
from a player with `ps.spawnInvulnUntil > serverTick`.

### 6.5 Eliminated players and the entity slab

An *eliminated* player (lives = 0) has their entity GC'd from the
slab on the death tick (the existing P3 `garbageCollect` for
players under `NoRespawn=true`, but Phase 5 ungates this for the
*eliminated* case regardless of `NoRespawn`). The player's record
remains in `s.store.players` so `Sim.Scores()` continues to report
the final score / `eliminated = true`. The `playerState.eliminated`
flag is one-way: once set, no respawn can clear it.

**Determinism note (§18.1).** `eliminated`, `livesRemaining`,
`score`, and `spawnInvulnUntil` are all included in the SHA-256
fingerprint ordering. The Phase 1 and Phase 3 goldens are
regenerated.

### 6.6 Score-aware end-reason evaluation (sim → match)

`evaluateMatchEnd` (in `internal/match/match.go`) switches its
"live player" definition from "has a non-dead slab entry" to
"`!ps.Eliminated`". This is exposed via `Sim.Scores()` which
returns `{lives, score, eliminated}` for every player. The match
actor reads `Eliminated` and `LivesRemaining` (not entity slab
state) for end-reason evaluation, because dead-cam players have
no slab entry but are still "active" until the match ends.

---

## 7. Spawn invulnerability (deeper)

### 7.1 Wire semantics

The existing `FlagSpawnInvuln = 1 << 1` is already published as
bit 1 of `Entity.flags` in §4.3.2. Phase 5 starts setting it
non-trivially:

- Initial spawn (NewSim): set, cleared at `serverTick = 60`.
- Respawn (step 8): set, cleared at `serverTick = respawnTick + 60`.

Clients render an invuln player differently (e.g. blinking, ~6 Hz
modulo `serverTick`). The render layer is Phase 7; here the flag
just needs to flip correctly.

### 7.2 Combat interaction

`combat.go::resolveProjectile` already filters candidates whose
`eflags & FlagSpawnInvuln != 0`. Phase 5 changes nothing in that
file. Both live-time and rewound-time (Phase 4 §8) filtering
honour the flag.

### 7.3 Fire-while-invuln gate

A player cannot fire while invulnerable (§3.9 "can't shoot during
it"). The sim's `processFireInputs` (step 3 of `Sim.Tick`) gains:

```go
ps := s.store.players[input.PlayerID]
if ps.spawnInvulnUntil > s.serverTick {
    continue  // drop the fire input silently
}
```

The drop is silent (no rejection event); the client's HUD shows the
invuln window and the player learns the rule through play.

---

## 8. Dead-cam (server-side state)

### 8.1 Slot field

`Slot` gains:

```go
type Slot struct {
    // ... existing fields ...
    LivesRemaining uint8
    Score          int32
    DeadCam        bool   // §3.9 — set when the player's lives hit 0
    DC             bool   // §4.7.1 — set when the WS dropped mid-match
    DCDeadlineTick uint32 // tick at which DC-grace expires (lives unchanged)
}
```

`DeadCam` and `DC` are independent flags: a player may be in
dead-cam (eliminated) AND have their WS drop, in which case the
slot is `DeadCam && DC` and the same 30 s grace applies for
re-binding the dead-cam WS.

### 8.2 Transition to dead-cam

In `match.tick()` after `sim.Tick(...)` returns, the match actor
walks every `Slot` and sets `DeadCam = true` whenever
`sim.Eliminated(slot.PlayerID) && !slot.DeadCam`. The first time
this flips for a given slot the actor broadcasts no extra event
(the `EventEntityKill` from the sim has already gone out;
`MatchOver` will deliver the final scoreboard). The `your_entity_id`
in subsequent snapshots is `0` because the entity is GC'd from the
sim slab — the snapshot builder honours that automatically.

### 8.3 Input handling while in dead-cam

`handleControl(ctlInput{...})` filters dead-cam slots:

```go
case ctlInput:
    if m.state() != StateLive {
        return
    }
    slot, ok := m.slots[v.PlayerID]
    if !ok {
        return
    }
    if slot.DeadCam {
        // §3.9: only Chat survives dead-cam. Input frames are dropped silently.
        return
    }
    m.pendingInputs[v.PlayerID] = v.Input
```

`Chat` frames continue to flow because they are dispatched via
`ctlChat` (a Phase 6 concern; for Phase 5 the chat dispatch path
in `net/server.go` is plumbed but the in-match chat broadcast is
left as a Phase 6 TODO — see §2 out-of-scope).

`Ping`/`Pong` continue to flow because they ride `ctlPong`, which
is independent of dead-cam state.

### 8.4 Snapshot delivery while in dead-cam

`Match.sendSnapshotTo(slot)` consults `slot.DeadCam` and calls
either `m.buildSnapshotFor(slot.PlayerID)` (priority order) or
`m.buildDeadCamSnapshotFor(slot.PlayerID)` (unfiltered). See §11
for the algorithm.

### 8.5 Match end while dead-cam slots exist

A dead-cam slot still receives the `MatchOver` frame on match end.
The slot's `LivesRemaining` is `0` in the `MatchOverEntry` payload.
The slot's `Score` is the final score (which may be positive — a
dead player who racked up kills earlier still appears in the
ranking).

---

## 9. Reconnect within DC-grace (§4.7.1)

### 9.1 DC-grace entry

When `net/server.go`'s connection reader returns (any WS termination
mid-match: FIN, RST, ping-timeout, backpressure-drop), instead of
the Phase 2 `m.SubmitClose(playerID, 0)` it now calls
`m.SubmitDC(playerID)`. The match actor handles `ctlDC` by:

1. Setting `slot.DC = true` and
   `slot.DCDeadlineTick = serverTick + DCGraceTicks`.
2. Calling `m.sim.FreezePlayer(slot.PlayerID)` — a new sim method
   that zeros the player's velocity and clears any pending fire
   input (the slab entry remains; the player can still be hit).
3. Broadcasting `Event{Kind: EventPlayerDC, Target: playerID,
   Reason: 0}` to every other slot.
4. Retaining `slot.out` as `nil` so any subsequent broadcast skips
   this slot silently (writers iterate `slots` and check `out != nil`).

If the player is already in dead-cam (`slot.DeadCam`), `ctlDC` is
still recorded so the dead-cam WS reconnect machinery applies.

### 9.2 Per-tick DC deadline check

`match.tick()` after `sim.Tick(...)` iterates every slot:

```go
for _, slot := range m.slots {
    if !slot.DC {
        continue
    }
    if m.sim.ServerTick() >= slot.DCDeadlineTick {
        m.dropDCSlot(slot)
    }
}
```

`dropDCSlot(slot)`:

- Calls `m.sim.RemovePlayer(slot.PlayerID)` — a new sim method that
  removes the slab entry and the `playerState` map entry. The
  player's score history is lost; that's intentional (slot is dead
  forever per §4.7.1).
- Broadcasts `Event{Kind: EventPlayerLeave, Target: playerID,
  Reason: 1 /* timeout */}`.
- Invalidates the slot's `joinToken` (see §9.4 below).
- Deletes the slot from `m.slots`.

### 9.3 Reconnect admission

`net/server.go`'s `MatchJoin` handler consults `resolveJoinPath`:

```go
type joinPath uint8
const (
    joinPathFresh    joinPath = iota // bySession token, ctlJoin
    joinPathReconnect                // DC-grace token, ctlReconnect
    joinPathReject                   // unknown / expired
)
```

- `joinPathFresh`: routes to the existing `m.SubmitJoin(token, out)`.
- `joinPathReconnect`: routes to `m.SubmitReconnect(token, out)`.
- `joinPathReject`: closes the WS with `Close{4001 AUTH}`.

`SubmitReconnect` posts `ctlReconnect{Token, Out, Reply}` to the
actor. The actor:

1. Looks up the `PlayerID` for `Token` in a per-match `dcTokens`
   map (populated when the slot entered DC-grace — see §9.4).
2. If found and `slot.DC == true` and `serverTick <
   slot.DCDeadlineTick`: clears `slot.DC`, sets `slot.out = v.Out`,
   broadcasts `Event{Kind: EventPlayerRejoin, Target: playerID}`,
   sends `Resync{ServerTick}` → `MapInit` → `Snapshot` →
   `Scoreboard` in that order to the new WS, replies with the
   `PlayerID`.
3. If not found, or grace expired, or slot already re-bound: replies
   with `ErrAuth`.

### 9.4 Token lifecycle for reconnect

The Phase 2 token rule was single-use ("`delete(m.bySession, v.Token)`
after first MatchJoin"). Phase 5 changes this to: when a slot
transitions to DC-grace, the actor copies the original token into a
new `m.dcTokens map[string]sim.EntityID`. The token continues to
work for *exactly one* reconnect MatchJoin within the 30 s grace
window. On successful reconnect: delete from `dcTokens`. On grace
expiry: delete from `dcTokens` AND broadcast `player_leave{timeout}`.

This preserves the §4.7.2 "auth is token-based" property: a third
party cannot hijack a DC'd slot without the token, and the token
is bound to one `(matchId, playerId)` pair.

### 9.5 Reconnect resync frame ordering

Per §4.3.2: `Resync` → `MapInit` → `Snapshot` → `Scoreboard`.
**Important:** the `Snapshot` after `Resync` must reflect the
player's *current* slab position (which is frozen at the DC point,
modulo any hits taken during grace — see §9.6 below). The
`Scoreboard` after `Resync` is built fresh (not the last broadcast)
so the reconnecting client gets every other player's current
state, not a stale rate-limited copy.

The client (per §4.4) flushes its prediction buffer on `Resync` and
re-establishes a new prediction lineage from the post-`Snapshot`
state. No additional client-side wiring is needed in Phase 5;
`netClient.ts` already handles `Resync` by drain-and-replay.

### 9.6 DC'd entity is killable

Per §4.7.1, a DC'd entity is still targetable. The sim sees no
difference between a DC'd player and a live one (velocity = 0, but
that is normal idle behaviour). Combat resolves against the DC'd
entity exactly as for any other player. If killed during grace:

- Lives decrement as usual.
- If lives reach 0 *during grace*: the slot transitions to
  `DeadCam = true` as normal. If the client reconnects within
  grace, the resync `Snapshot` carries `your_entity_id = 0` and the
  client enters dead-cam mode immediately.
- The score award goes to the killer as usual.

A DC'd player whose lives hit 0 mid-grace AND who does not
reconnect by the 30 s deadline: the slot is GC'd; no dead-cam ever
delivered. This is the documented v1 behaviour and is accepted.

---

## 10. Late-join policy

The Phase 2 admission gate already rejects late join during
`StateLive` via the `ErrAuth` branch in `handleJoin`. Phase 5 makes
two refinements:

1. The reject reason is logged as `late_join` (vs. `unknown_token`)
   for telemetry. Wire close code is unchanged: `Close{4001 AUTH}`.
2. The admission gate is consulted **after** the reconnect-path
   lookup. This means: an unknown token mid-match returns
   `Close{4001 AUTH}`, but a *DC-grace* token from a previously-
   joined player returns success (the reconnect path).

`StateStarting` is the brief window from "first MatchJoin received"
through "all slots joined and sim built". Phase 5 keeps the Phase 2
behaviour: joins during this window are admitted (this is the
warmup window per `matchWarmupTimeout = 10s`).

There is no separate `STARTING` enum value at the wire layer;
"warmup" is the `StateWaitingForJoins` state in the match actor.

---

## 11. AOI selection and snapshot building

This is the section with the most new code: `internal/match/aoi.go`.

### 11.1 Inputs

For each tick where a snapshot is broadcast (every 2 sim ticks per
§4.3.3), the match actor computes one `Snapshot` per recipient by
calling `buildSnapshotFor(recipient)`. Phase 5 replaces the Phase 2
"emit-all" body of that function with the §5.3.1 priority builder.

Inputs available:
- `entities := m.sim.Entities()` — the full slab (≤ 256 entries).
- `recipient sim.EntityID` — the recipient player.
- `m.aoiPrev map[sim.EntityID]map[sim.EntityID]struct{}` — per-
  recipient set of entity IDs included in the *previous* tick's
  snapshot. Used for §11.3 hysteresis.

### 11.2 Priority order (recap of §5.3.1)

```
1. Self (recipient's own player entity)
2. Other live players (all of them, distance ascending)
3. Recipient's own projectiles (all of them)
4. Projectiles within 10 tiles (12 tiles if previously included)
5. Generators within 20 tiles (22 tiles if previously included)
6. Snipes within 20 tiles (22 tiles if previously included)
7. Other entities (further-out by distance ascending) until cap
```

The cap is 64 entries. If a step would push the count past 64, that
step is truncated by distance (closest first) and steps 5–7 below
it are skipped.

### 11.3 Hysteresis

The hysteresis rule per §5.3.1: an entity included in the previous
snapshot stays included until it crosses the *outer* band. The
implementation:

```go
// distance in tiles, rounded down (subtile / 256)
dist := chebyshevTiles(recipientX, recipientY, e.X, e.Y)
inner := AOIInnerProj  // 10 for projectiles, 20 for snipes/generators
outer := AOIHysteresisProj  // 12 / 22

wasIncluded := m.aoiPrev[recipient][e.ID]
include := dist <= inner || (wasIncluded && dist <= outer)
```

Chebyshev distance (max of |dx|, |dy|) is used, not Euclidean — it
matches the existing `respawn.go::chebyshev` helper and is integer-
clean. **Performance note:** computing the per-entity distance is
~30 ns; 256 entities × 8 recipients = ~60 µs per tick total. Well
under budget.

### 11.4 Sort stability

For deterministic snapshots (so replay fixtures are byte-stable
across runs), distance ties are broken by ascending `EntityID`.
The priority groups are processed in fixed order (1 → 7) and
within each group the candidates are sorted by `(dist, id)`.

### 11.5 Bookkeeping

After the snapshot is built, the match actor writes the current
included set back to `m.aoiPrev[recipient]` for use by the next
tick. Memory: one map per recipient with ≤ 64 keys. Across 8
players that's 8 × 64 = 512 entries; negligible.

The `aoiPrev` map is cleared for a recipient whenever the
recipient enters dead-cam (so re-entering "normal" priority view
on resync — impossible in v1 since dead-cam is one-way — would
not carry stale hysteresis state).

### 11.6 Dead-cam unfiltered builder

`buildDeadCamSnapshotFor(recipient)` ignores §5.3.1 priorities 1–6
and emits *every* live entity in the slab, truncating at 64 by
**distance from the map centre** (priority 7 tiebreak). Map centre
is `(W * subtilePerTile / 2, H * subtilePerTile / 2)`.

```go
type aoiCandidate struct {
    id   sim.EntityID
    dist int  // chebyshev tiles from centre
}
candidates := make([]aoiCandidate, 0, len(entities))
for _, e := range entities {
    if e.Flags & sim.FlagDead != 0 { continue }
    candidates = append(candidates, aoiCandidate{e.ID, chebyshevTiles(cx, cy, e.X, e.Y)})
}
sort.Slice(candidates, func(i, j int) bool {
    if candidates[i].dist != candidates[j].dist { return candidates[i].dist < candidates[j].dist }
    return candidates[i].id < candidates[j].id
})
if len(candidates) > 64 { candidates = candidates[:64] }
```

The dead-cam snapshot sets `your_entity_id = 0` because the slab
entry has been GC'd (lives reached 0). `your_last_input_tick` is
the last input received pre-elimination; the client uses it for
informational HUD only (prediction is disabled in dead-cam).

### 11.7 Snapshot for DC'd (not eliminated) recipient

A slot in `DC` but not `DeadCam` has `slot.out == nil` so no
snapshot goes out at all during grace. When the client reconnects,
the resync sequence (§9.5) delivers one fresh snapshot built via
the normal priority order. `aoiPrev[recipient]` is reset on
reconnect so the resync snapshot has no hysteresis carryover.

---

## 12. Scoreboard delta emission

### 12.1 The detector

`internal/match/score_delta.go` exposes:

```go
type scoreSnapshot struct {
    score map[sim.EntityID]int32
    lives map[sim.EntityID]uint8
}

func (m *Match) detectScoreChange() bool {
    cur := m.sim.Scores()
    for id, ps := range cur {
        if m.lastScores.score[id] != ps.Score || m.lastScores.lives[id] != ps.Lives {
            return true
        }
    }
    return false
}
```

At the end of every `tick()`, if `detectScoreChange()` is true AND
`serverTick - lastScoreboardTick >= ScoreboardMinIntervalTicks (6)`,
the actor broadcasts a fresh `Scoreboard` frame to every joined
non-DC slot and updates `lastScoreboardTick = serverTick`. If the
change occurs within the cooldown window, the broadcast is
deferred until cooldown expires (the next tick that satisfies the
6-tick gap).

The 5 Hz cap (per §4.3.2 row) prevents a chain of simultaneous
kills from flooding the link with scoreboard frames.

### 12.2 Scoreboard payload

`buildScoreboardEntries()` is updated to populate the `Score` and
`Lives` fields from `sim.Scores()`:

```go
func (m *Match) buildScoreboardEntries() []proto.ScoreboardEntry {
    out := make([]proto.ScoreboardEntry, 0, len(m.slots))
    scores := m.sim.Scores()
    for id, slot := range m.slots {
        ps := scores[id]
        out = append(out, proto.ScoreboardEntry{
            PlayerID: uint32(id),
            Nick:     slot.Nick,
            Lives:    ps.Lives,
            Score:    ps.Score,
        })
    }
    sortScoreboardEntries(out)
    return out
}
```

`Nick` survives DC-grace and dead-cam (the slot is retained until
slot GC). DC'd or dead-cam players appear in the scoreboard with
`Lives = 0` (dead-cam) or `Lives = lastLives` (DC) accordingly.

### 12.3 Initial scoreboard

The first `Scoreboard` (sent during `MatchJoin` per §4.3.2's
"sent (c) once on initial MatchJoin so the HUD renders from
frame 1") carries `Lives = LevelParams.PlayerLives`, `Score = 0`
for every joined slot. Phase 2 already plumbs this; Phase 5
verifies it via `TestInitialScoreboardLives`.

---

## 13. End-reason precedence (full §3.8.1)

The Phase 3 `evaluateMatchEnd` already implements `PVE_COMPLETE`,
`LAST_STANDING`, and `ALL_ELIMINATED`. Phase 5 adds:

- `TIMER` at `serverTick >= MatchTimerTicks (18000)`.
- `SERVER_ERROR` via the existing panic-recovery path.

### 13.1 Lives-based "alive" definition

In Phase 3 the "live player count" was derived from the slab. In
Phase 5 it must come from `sim.Scores()`:

```go
liveByLives := 0
var lastAlive sim.EntityID
for id, ps := range m.sim.Scores() {
    if !ps.Eliminated && m.slots[id] != nil {
        liveByLives++
        lastAlive = id
    }
}
```

A DC'd player counts as "alive" (their entity is targetable; their
slot is retained). Only `eliminated == true` counts as dead for
end-reason purposes.

### 13.2 Full precedence

```go
func (m *Match) evaluateMatchEnd() (uint8, sim.EntityID, bool) {
    // ... compute liveByLives, lastAlive, liveGens, liveSnipes ...

    // Priority 0: PVE_COMPLETE.
    if m.isPvE && liveGens == 0 && liveSnipes == 0 && liveByLives >= 1 {
        return proto.EndPVEComplete, 0, true
    }

    // Priority 1: LAST_STANDING.
    if liveByLives == 1 && m.startingPlayerCount >= 2 {
        return proto.EndLastStanding, lastAlive, true
    }

    // Priority 2: ALL_ELIMINATED.
    if liveByLives == 0 {
        return proto.EndAllEliminated, 0, true
    }

    // Priority 3: TIMER.
    if m.sim.ServerTick() >= MatchTimerTicks {
        return proto.EndTimer, m.timerWinner(), true
    }

    // Priority 4: SERVER_ERROR — set by abortFromPanic, not here.
    return 0, 0, false
}
```

`m.timerWinner()` returns the player with the highest score (ties
allowed: returns the smallest `EntityID` on tie, which is the same
tie-break used elsewhere). Returns `0` if all players have score
≤ 0 AND no clear winner — TBD: see §22 open question.

### 13.3 MatchOver entries

`buildMatchOverEntries(winner, reason)` is updated to populate
`Score` and `LivesRemaining` from `sim.Scores()`:

```go
for id, slot := range m.slots {
    ps := scores[id]
    entries = append(entries, proto.MatchOverEntry{
        PlayerID:       uint32(id),
        Score:          ps.Score,
        LivesRemaining: ps.Lives,
    })
}
sortEntries(entries)
```

For `LAST_STANDING`: the winner's `LivesRemaining` reflects their
current value (which could be any number from 1 to MAX). For
`PVE_COMPLETE`: every player who finished with `Lives >= 1` is a
joint winner; `winner = 0` per §3.8 and the client renders the
scoreboard tied. For `TIMER`: highest score wins (joint allowed);
again the client handles ties on `winner = 0`.

---

## 14. Wire protocol notes

**The wire is unchanged.** Every byte produced by Phase 5 is
already part of the Phase 2 schema:

| Frame | Phase 5 change |
|---|---|
| `MatchOver` | populates `Score` and `LivesRemaining` (were stubs in P2) |
| `Scoreboard` | populates `Lives` and `Score` (were stubs in P2) |
| `Resync` | now emitted on reconnect-accept (was unused in P2) |
| `Event{player_dc}` | now emitted on WS drop mid-match (was unused) |
| `Event{player_rejoin}` | now emitted on reconnect-accept (was unused) |
| `Event{player_leave, reason: timeout}` | now emitted at DC deadline |
| `Event{respawn_pending}` | now emitted in step 8 of `Sim.Tick` for each player whose respawn fires this tick (the existing event was reserved in P2; Phase 5 starts emitting) |

`schemaChecksum` is **byte-identical** to `0x42607394`.
`testdata/proto/checksum.txt` is **NOT** edited.
`web/tests/proto.test.ts` continues to pass without modification.

The TS client decode path already accepts these fields; no
`web/src/proto.ts` edits are required.

---

## 15. Match actor changes

### 15.1 Control message additions

```go
type ctlDC struct{ PlayerID sim.EntityID }
type ctlReconnect struct {
    Token string
    Out   chan<- OutboundFrame
    Reply chan<- joinResult
}
type ctlDeadCamInput struct {
    PlayerID sim.EntityID
    Input    proto.Input
}

func (ctlDC) controlTag()           {}
func (ctlReconnect) controlTag()    {}
func (ctlDeadCamInput) controlTag() {}
```

`ctlDeadCamInput` is only used by the test harness to verify that
inputs are correctly dropped when the slot is in dead-cam (the
production net layer routes everything through `ctlInput`, which
the actor then filters).

### 15.2 Per-tick state machine

`match.tick()` (existing, in `match.go`) is extended:

```go
func (m *Match) tick() {
    // ... existing input-buffer drain + sim.Tick + event broadcast ...

    // Phase 5 §6.5: transition newly-eliminated slots to dead-cam.
    for id, slot := range m.slots {
        if m.sim.Eliminated(id) && !slot.DeadCam {
            slot.DeadCam = true
            slot.LivesRemaining = 0
        }
    }

    // Phase 5 §9.2: drop DC slots whose grace has expired.
    for id, slot := range m.slots {
        if slot.DC && m.sim.ServerTick() >= slot.DCDeadlineTick {
            m.dropDCSlot(id, slot)
        }
    }

    // Phase 5 §12: scoreboard delta detector.
    if m.detectScoreChange() && m.canEmitScoreboard() {
        m.broadcastScoreboard()
    }

    // ... existing snapshot broadcast (every 2 ticks) ...
    // ... existing end-reason evaluation ...
}
```

### 15.3 Match-warmup interaction

The Phase 2 warmup (`matchWarmupTimeout = 10s`) is unchanged. A
match that fails to gather `minPlayers` (1 for PvE, 2 for PvP)
within 10 s aborts with `NO_OPPONENT`. Dead-cam and reconnect do
not apply to a match that never reached `StateLive`.

---

## 16. Determinism rules (additions)

### 16.1 Fingerprint extensions

`internal/sim/fingerprint.go` is extended (after the existing
`respawnAt`, `deathX`, `deathY` field encoding) to write four
new fields per player in `liveIDsSorted` order:

```go
// playerState additions, in this byte order:
// livesRemaining (u8), spawnInvulnUntil (u32, LE), score (i32, LE), eliminated (u8)
```

Eliminated players whose slab entry has been GC'd still contribute
to the fingerprint via `s.store.players[id]`: the map retains the
record (see §6.5). The fingerprint walks `liveIDsSorted` for slab
entries AND walks a separately-sorted list of `players` map IDs
that are not in the slab (those are the eliminated ones).

### 16.2 Lag-comp is still per-connection

Phase 4's note that "lag-comp is per-connection state and is NOT
in the fingerprint" continues to hold. The OWT estimator's
internal state is in `internal/match`, never `internal/sim`.

### 16.3 Network state is not in the fingerprint

`Slot.DC`, `Slot.DCDeadlineTick`, `Slot.DeadCam`, scoreboard
emission timing — all of these are match-actor state. The sim
fingerprint sees only player `eliminated` (which is a fact about
the sim, not the network).

### 16.4 Golden regeneration

The Phase 1 `testdata/replays/baseline.{inputs,hash}` and
Phase 3 `testdata/replays/phase3_pve.{inputs,hash}` are
**regenerated** to include the new fingerprint fields. The
`inputs` files are unchanged; only the `hash` files change.

The new Phase 5 fixture `phase5_4p_pvp.{inputs,hash,matchover.json}`
is committed alongside.

---

## 17. Sim tick loop changes (full revised step list)

```
Step 1: drain inputs (existing P1)
Step 2: snipe AI tick (existing P3)
Step 3: process fire inputs (existing P1 + §7.3 invuln gate)
Step 4: movement (existing P1)
Step 4.9: writeHistorySamples (existing P4 §6.2)
Step 5: projectile motion + hit resolution (existing P1 + P4 lag-comp)
Step 6: snipe firing + emit timers (existing P3)
Step 7: kill processing (P1 + Phase 5 §6.3: lives, score, eliminated)
Step 8: respawn (P1 + Phase 5: sets spawnInvulnUntil + FlagSpawnInvuln)
Step 8.5: clear spawnInvuln on timer expiry (NEW — Phase 5 §6.4)
Step 9: garbageCollect (existing — Phase 5 widens eliminated-player GC)
Step 10: increment serverTick + return events
```

Step 8.5 is the only new step. Its cost is O(players) ≈ O(8), so
bench impact is negligible.

---

## 18. Test plan

### 18.1 Sim — lives & score (`internal/sim/lives_test.go`)

- `TestPlayerStartsWithLivesFromLevel` — `LevelParams.PlayerLives`
  flows into `Sim.LivesRemaining(id)` for every player at construction.
- `TestPlayerLivesDecrementOnKill` — fire a snipe projectile at a
  player; on the death tick `LivesRemaining` decrements by 1.
- `TestPlayerEliminatedAtZero` — kill a player N times; on the Nth
  death `Eliminated(id)` returns true and the slab entry is GC'd.
- `TestNoRespawnScheduledForEliminated` — after the final death,
  `playerState.hasRespawnAt` is false; sim.Tick advances 200 ticks
  with no respawn event.
- `TestSpawnInvulnSetAndCleared` — initial spawn: `FlagSpawnInvuln`
  is set; at `serverTick = 60` the flag is cleared.
- `TestSpawnInvulnFireGate` — a player input with `FireDir != 0`
  during invuln does not produce a projectile.
- `TestKillerAwardedScore` — snipe killed: killer's score += 1;
  generator killed: += 10; player killed: += 25; player died
  (own): -= 5.
- `TestDeterminism_LivesAndScoreInFingerprint` — two `Sim` instances
  with identical configs and scripted inputs produce byte-equal
  fingerprints at every tick.
- `TestFingerprintEliminatedSurvives` — eliminated player's record
  in `s.store.players` continues to contribute to the fingerprint
  after slab GC.

### 18.2 Sim — respawn + invuln (`internal/sim/respawn_test.go` extensions)

- `TestRespawnSetsInvulnAndTimer` — on respawn tick, the player's
  entity has `FlagSpawnInvuln` set and `SpawnInvulnUntil() ==
  serverTick + 60`.
- `TestRespawnTileSelectionUnchanged` — the existing P3 respawn
  selector (§10.5) still operates correctly; spawn invulnerability
  does not affect selection.
- `TestProjectileMissesInvulnTarget` — fire a projectile at an
  invuln target; no hit event; projectile expires.

### 18.3 Match — scoreboard & end-reason (`internal/match/match_test.go` extensions)

- `TestInitialScoreboardLives` — first `Scoreboard` after MatchJoin
  carries `Lives = LevelParams.PlayerLives` for every slot.
- `TestScoreboardEmittedOnScoreChange` — fire a kill event;
  `Scoreboard` is broadcast within ≤ 6 ticks.
- `TestScoreboardRateLimited` — 10 rapid kill events fire 1
  Scoreboard, not 10; the 5 Hz cap holds.
- `TestEndReason_PvECompleteWinsAllElimTie` — all generators
  destroyed on the same tick the last 2 players kill each other:
  `PVE_COMPLETE`.
- `TestEndReason_AllElimOverTimer` — all players die on the same
  tick the timer expires: `ALL_ELIMINATED`.
- `TestEndReason_LastStandingOnlyAtStartCountGE2` — solo PvP match
  (impossible at the lobby gate but verified here): never produces
  `LAST_STANDING`.
- `TestEndReason_TimerFiresAt10Min` — drive the sim to
  `serverTick = 18000` with all players alive; `TIMER` fires;
  winner is the player with the highest score.
- `TestEndReason_TimerTieByScore` — two players with equal score
  at `serverTick = 18000`: `winner = 0`; both appear at the top of
  the scoreboard.

### 18.4 Match — dead-cam (`internal/match/deadcam_test.go`)

- `TestDeadCamTransition` — player's last life lost; on the next
  tick `slot.DeadCam` is true.
- `TestDeadCamIgnoresMovementInput` — `SubmitInput` with `Dir = E`
  on a dead-cam slot does not appear in `m.pendingInputs`.
- `TestDeadCamAcceptsChat` — `Chat` frames continue to flow (the
  chat handler is wired but the broadcast path is a Phase 6 stub;
  this test verifies the path is *not* blocked by the dead-cam
  filter).
- `TestDeadCamSnapshotYourEntityIDIsZero` — every snapshot sent to
  a dead-cam slot carries `your_entity_id = 0`.
- `TestDeadCamSnapshotIsUnfiltered` — fixture with 8 players + 50
  snipes + 30 projectiles + 9 generators (≤ 64 expected after the
  cap binds); dead-cam recipient receives entries ordered by
  distance from the map centre; no priority 1–6 filtering.
- `TestDeadCamReceivesMatchOver` — kill an eliminated player; the
  match continues and `MatchOver` arrives at the dead-cam WS.

### 18.5 Match — AOI (`internal/match/aoi_test.go`)

- `TestAOI_RecipientSelfAlwaysIncluded` — single recipient, busy
  map; their own entity is always entry 0 (modulo ID-sort).
- `TestAOI_RecipientOwnProjectilesAlwaysIncluded` — fire 5
  projectiles; even at the edge of the map, all 5 appear in the
  snapshot.
- `TestAOI_ProjectilesBeyond10TilesExcluded` — fire a projectile
  20 tiles away from the recipient; not in the recipient's
  snapshot.
- `TestAOI_HysteresisProjectile` — projectile enters at 9 tiles
  (included), moves to 11 tiles (still included due to 12-tile
  outer band), moves to 13 tiles (excluded). Verified across 3
  consecutive snapshots.
- `TestAOI_HysteresisSnipeGen` — same but for snipes/generators at
  the 20 / 22 tile bands.
- `TestAOI_CapBinds` — 97-entity fixture; every snapshot has
  exactly 64 entries; priority order is preserved.
- `TestAOI_DistanceTiebreakByID` — two projectiles at exactly the
  same distance: entry order is ascending `EntityID`.

### 18.6 Match — reconnect (`internal/match/reconnect_test.go`)

- `TestReconnect_DCMarksSlotAndFreezes` — `SubmitDC` is called;
  the slot's `DC` flag is true; `sim.Entities()` shows the entity
  with `VX = VY = 0`.
- `TestReconnect_PlayerDCEventBroadcast` — `SubmitDC` triggers one
  `Event{player_dc, target: pid}` to every other slot.
- `TestReconnect_AcceptedWithinGrace` — `SubmitReconnect(token,
  newOut)` 20 s post-DC: the slot's `DC` flag clears, `out` is
  re-bound, `Resync` → `MapInit` → `Snapshot` → `Scoreboard` are
  delivered in that order.
- `TestReconnect_PlayerRejoinEventBroadcast` — on accept, a
  `player_rejoin` event reaches every other slot.
- `TestReconnect_TokenSingleUseWithinGrace` — `SubmitReconnect`
  twice with the same token; the second call returns `ErrAuth`.
- `TestReconnect_RejectedPastGrace` — `SubmitReconnect` at
  `DCDeadlineTick + 1`: returns `ErrAuth`; the slot is gone;
  `player_leave{reason: timeout}` has been broadcast.
- `TestReconnect_DCdEntityCanBeKilled` — fire a snipe projectile
  at a DC'd player; lives decrement; on reach-0 the slot
  transitions to dead-cam mid-grace.
- `TestReconnect_DCdMidEliminationGracefulDrop` — DC mid-grace,
  killed mid-grace, lives reach 0, grace expires: slot is GC'd;
  no dead-cam reconnect possible.

### 18.7 Net — admission routing (`internal/net/server_test.go` extensions)

- `TestNet_MatchJoinFreshPath` — unknown token → `Close{4001
  AUTH}`; known token → `ctlJoin` → success.
- `TestNet_MatchJoinReconnectPath` — DC-grace token → `ctlReconnect`
  → success.
- `TestNet_MatchJoinPostGraceAuthClose` — token whose grace
  expired → `Close{4001 AUTH}`.
- `TestNet_WSDropMidMatchSubmitsDC` — kill a WS via reader cancel;
  the match actor receives `ctlDC{playerID}`.
- `TestNet_IdleTimeoutMidMatchSubmitsDC` — no frames for 5 s; the
  reader exits via idle path; `ctlDC` posted.

### 18.8 Late-join (`internal/match/match_test.go` extensions)

- `TestLateJoinDuringStarting_Accepted` — `MatchJoin` during
  `StateWaitingForJoins` (before all slots filled): accepted, slot
  marked `Joined = true`.
- `TestLateJoinDuringLive_Rejected` — `MatchJoin` after the sim is
  built (StateLive): `Close{4001 AUTH}`.

### 18.9 Integration (`internal/match/match_test.go` — new file `e2e_test.go`)

- `TestMatch4PlayerScoreboardE2E` — see DoD #7 — 4-client scripted
  match against a committed `phase5_4p_pvp.matchover.json` fixture.
  The scripted inputs cover: kills, respawn, spawn-invuln window
  (verify zero damage during it), one player reaching dead-cam.

### 18.10 Determinism (`internal/sim/property_test.go` extensions)

- `TestDeterminism_GoldenFingerprint` — Phase 1 fixture, hash
  regenerated to include the new fingerprint fields.
- `TestDeterminism_GoldenFingerprint_Level9` — Phase 3 fixture,
  hash regenerated.
- `TestDeterminism_Phase5_4PPvP` — new fixture per DoD #5.

### 18.11 Benchmarks (`internal/match/bench_test.go` — new file)

- `BenchmarkMatchTick_8P_Level9` — 8 active players + 11 gens +
  level-9 snipe cap; measures full match-tick cost including AOI
  builder, score-delta detector, and end-reason evaluation. Gate:
  ≤ 4 ms/op without `-race`.
- `BenchmarkAOIPriorityBuilder` — 97-entity fixture, single
  recipient; measures the priority builder cost in isolation. Gate:
  ≤ 50 µs/op.

---

## 19. Testdata

### 19.1 `testdata/replays/phase5_4p_pvp.inputs`

Plain-text scripted input format, identical to Phase 3's
`phase3_pve.inputs`:

```
# header line: seed width height level_letter level_number
12345 60 40 A 1
# input lines: tick player_id dir turbo fire_dir
0 1 3 0 0
0 2 7 0 0
...
```

Roughly 2000 ticks, ~66 s, 4 players (IDs 1–4), at level `A1`
(`PlayerLives = 9` — generous, fits a longer fixture without
hitting the 5-life cap of higher numbers).

Coverage: at least 6 kill events (player-on-player; ID-1 kills
ID-2; ID-3 kills ID-1; etc.), 4 respawn cycles, one player
reaching 0 lives (ID-4 dies 9 times), spawn-invuln windows
verified by inputs that try to fire during the post-respawn
60-tick window.

### 19.2 `testdata/replays/phase5_4p_pvp.hash`

SHA-256 of the sim fingerprint at the final tick. Committed
verbatim; regenerated on the four CI runners every time the
fingerprint format changes.

### 19.3 `testdata/replays/phase5_4p_pvp.matchover.json`

The expected `MatchOver` payload, encoded as JSON for readability:

```json
{
  "final_tick": 2000,
  "reason": 1,
  "winner_id_or_0": 1,
  "entries": [
    {"player_id": 1, "score": 75, "lives_remaining": 8},
    {"player_id": 2, "score": -5, "lives_remaining": 7},
    {"player_id": 3, "score": 25, "lives_remaining": 6},
    {"player_id": 4, "score": -45, "lives_remaining": 0}
  ]
}
```

(Exact numbers will be determined by the inputs; the above is
illustrative.) The test reads this file via `os.ReadFile` and
compares to the actor's emitted `MatchOver` after JSON
round-tripping.

### 19.4 `testdata/replays/baseline.hash` and `phase3_pve.hash`

Both files are **regenerated** by Phase 5's first iteration to
include the new fingerprint fields. The `.inputs` files are
**not** modified. The expected procedure: a CI-runner side script
runs the existing replay tests with `-update-goldens=1`, the
human commits the resulting hash files. (Or — the test itself
detects the format change and produces a clear "regenerate
goldens" message; the operator regenerates and commits.)

---

## 20. Risks

- **Fingerprint regeneration cost.** Changing the fingerprint
  format invalidates every committed `*.hash` file. Mitigation:
  do it once in Phase 5's first iteration; commit all three new
  hashes in one PR.
- **AOI cost under saturation.** 8 recipients × 97 entities ×
  log(97) sort = ~9000 comparisons per snapshot; at 15 Hz that's
  135K comparisons/s. Mitigation: heap-based partial sort (k=64
  closest) reduces this to k log n per recipient = ~400 ops; bench
  gates the result.
- **Reconnect token grace bypass.** A client that knows another
  player's token could try a reconnect during their DC-grace.
  Mitigation: the §4.7.2 token model already binds (matchId,
  playerId); a stranger does not have the token. The token is
  also bound to a TLS-encrypted WS in production.
- **Mid-grace damage after reconnect.** A client who reconnects to
  find their HP reduced (because they were shot during DC) might
  be surprised. Mitigation: documented v1 behaviour per §4.7.1
  "still targetable". The resync `Snapshot` carries the current
  HP; client renders accordingly.
- **Eliminated player's `playerState` map leak.** A long-running
  match where multiple players are eliminated accumulates
  `playerState` records. Mitigation: per-match memory is capped
  (`maxEntities = 256`); the players-map grows by at most 16 (the
  player cap). Negligible.
- **Spawn-invuln 60-tick window feels too long.** 2 s of immunity
  in a fast PvP firefight is visible. Mitigation: SPEC §3.9 binds
  it; tuning is a v1.1 concern. Tests pin the exact value via
  `TestSpawnInvulnSetAndCleared`.
- **TIMER tie-breaker ambiguity.** Multiple players with equal
  highest score at `serverTick = 18000` → `winner = 0` and the
  client renders the joint result. Documented in §13.2.
- **Dead-cam input filter race.** A `ctlInput` arriving on the
  same tick the slot transitioned to dead-cam: filtered correctly
  because the filter is checked at `handleControl` time, post-
  transition. Verified by `TestDeadCamIgnoresMovementInput`.
- **Hysteresis state when recipient teleports.** Phase 5 has no
  teleport mechanic, but a respawn moves the recipient by many
  tiles. Mitigation: respawn clears `aoiPrev[recipient]` so the
  next snapshot starts from a clean hysteresis state.

---

## 21. Open questions

These are flagged for codex review and for the author to decide
before implementation begins. Defaults are listed if no decision
is forced.

1. **PvP-only-default lives count.** §3.7 binds lives via the
   level table, but Phase 2's PvP-only mode (`LevelLetter == 0`)
   has no level table. Default: 3 lives. Alternative: 1 life
   (sudden-death). Pin via `TestPvPDefaultLives`.
2. **TIMER tie-break — ID ascending or lobby order?** §3.8 says
   "ties allowed (joint winners)". This spec returns the smallest
   `EntityID` as the cosmetic "winner" in `MatchOver.winner_id_or_0`,
   matching every other tiebreak in the codebase. Alternative:
   `winner_id_or_0 = 0` always under tie (purely cosmetic — the
   scoreboard ordering tells the truth). Default: smallest ID.
3. **DC-grace and dead-cam interaction.** If a player in dead-cam
   has their WS drop: do they get the same 30 s grace, or are they
   immediately dropped? This spec says: same 30 s grace, because
   the slot serves match-end frames. Tests cover both paths.
4. **Scoreboard delta detector — every tick or every snapshot?**
   This spec runs the detector every tick (30 Hz read) but rate-
   limits emission to 5 Hz. Alternative: run only at snapshot ticks
   (15 Hz read). Default: every tick — the read is cheap and
   catches changes faster.
5. **Late-join: STARTING window.** This spec treats
   `StateWaitingForJoins` as a "join window" (so a player who
   loses their first `MatchJoin` to a network blip can retry).
   Alternative: tokens are single-use and a retry-during-warmup
   gets `ErrAuth`. Default: retry allowed up to `Joined = true`.
6. **`buildMatchOverEntries` ordering.** Current code sorts by
   ascending `PlayerID`. SPEC §3.8 says "scoreboard order is by
   score descending; ties allowed". For the *MatchOver* frame the
   client re-sorts for display; the wire order is arbitrary.
   Default: stay with ID-ascending (matches the existing
   `sortEntries` helper).
7. **`Sim.RemovePlayer` vs. `Sim.MarkPlayerLeft`.** `dropDCSlot`
   needs a way to remove the slab entry mid-match. This spec adds
   `Sim.RemovePlayer(id)` which calls `store.remove(id)` and
   deletes the `playerState` map entry. Alternative: keep the
   record forever (consistent with eliminated-but-still-in-map).
   Default: remove fully (a timed-out player should not appear in
   `MatchOver` at all per §4.7.1 "slot terminated"). To resolve in
   iteration 1.

---

## 22. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./internal/sim/...` green; new fingerprint goldens committed | CI |
| 2 | `go test -race -count=1 ./internal/match/...` green | CI |
| 3 | `go test -race -count=1 ./internal/net/...` green (DC + reconnect paths) | CI |
| 4 | `pnpm -C web test` green (no client wire changes; existing P4 tests still pass) | CI |
| 5 | `testdata/replays/phase5_4p_pvp.{inputs,hash,matchover.json}` committed; replay reproduces byte-for-byte | §19 |
| 6 | `BenchmarkMatchTick_8P_Level9` ≤ 4 ms/op; `BenchmarkAOIPriorityBuilder` ≤ 50 µs/op | CI bench gate |
| 7 | `TestMatch4PlayerScoreboardE2E` passes against committed `matchover.json` | §18.9 |
| 8 | `TestDeadCamSnapshotIsUnfiltered` passes | §18.4 |
| 9 | `TestReconnectWithinGrace` passes (Resync→MapInit→Snapshot→Scoreboard) | §18.6 |
| 10 | `TestReconnectPastGraceRejected` passes (player_leave{timeout}; later MatchJoin → AUTH) | §18.6 |
| 11 | `TestLateJoinDuringLive_Rejected` passes | §18.8 |
| 12 | `TestAOIPriorityCapAndHysteresis` (97-entity fixture) passes for every recipient | §18.5 |
| 13 | `TestEndReasonPrecedence` 4 sub-tests pass (PvECompleteWinsAllElimTie, AllElimOverTimer, LastStandingOnlyAtStartCountGE2, TimerFiresAt10Min) | §18.3 |
| 14 | Coverage ≥ 80 % `internal/sim`; ≥ 75 % `internal/match`; ≥ 70 % `internal/net` | CI |
| 15 | `schemaChecksum` unchanged from Phase 2 (`0x42607394`) | `TestSchemaChecksumValue` |

Items 1–15 are gate-able in CI.

The wire protocol is **byte-identical** to Phase 4, so the existing
Phase 2 vitest mirror under `web/tests/proto.test.ts` continues to
pass without modification. No `internal/proto/checksum.go` edits.
