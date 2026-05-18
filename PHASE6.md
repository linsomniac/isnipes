# Phase 6 — Lobby polish & matchmaking

This document is the buildable, testable expansion of §8 Phase 6 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–5 are on `main`:
`internal/sim` is the deterministic 30 Hz simulation (with snipes,
lives, scoring, spawn-invuln, position-history); `internal/proto`
defines the binary wire schema (`schemaChecksum = 0x42607394`);
`internal/match` runs match actors with end-reasons, lives,
dead-cam, and DC-grace reconnect; `internal/net` carries frames
with server-initiated Ping; `internal/lobby` exposes a *minimal*
lobby (hello/welcome/roomList/createRoom/joinRoom/leaveRoom/
startMatch/error/matchStarted with token janitor TTL and idle
session expiry); and `cmd/isnipes` boots all of the above.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is
canonical and this document is wrong; please file an issue.
Section references like "§6.3" point into `SPEC.md`; "P1 §x",
"P2 §x", "P3 §x", "P4 §x", "P5 §x" point into the earlier phase
docs.

---

## 1. Scope and definition of done

**Scope.** Phase 6 brings the Phase 2 minimal lobby up to the full
feature set described in SPEC §6:

- **Lobby chat** (`chat` envelope, room-scoped) and the
  `chat_relay` Event variant on the lobby WS.
- **Kick-host action** — the room host may evict another player
  from their room.
- **Level selector** — the (`A`..`Z`, `1`..`9`) level table that
  matches `internal/sim`'s §3.7 parameters is exposed through
  `createRoom`'s `level` payload, with server-side validation and
  preset-preview metadata.
- **Per-room inactivity / abandonment GC** — rooms with no live
  members are torn down after 30 s; matches whose every player has
  exited (DC-grace expired AND/OR clean leave) end after 30 s
  per §6.3.
- **`roomList` delta-push protocol** — Phase 2 broadcasts the full
  list on every change; Phase 6 keeps the full-list initial reply
  but layers a delta envelope (`room_added`, `room_updated`,
  `room_removed`) for subsequent change notifications, halving
  message size on busy lobbies.
- **`?room=ABCD23` URL deep-link** — client-side router parses
  the query, prompts for a nick if needed, then auto-issues
  `joinRoom{roomId}` post-hello.
- **Browser-side lobby client** — new TS modules `lobby.ts`,
  `main.ts` (top-level state machine), and a minimal HTML chrome
  that exercises the lobby protocol end-to-end. No render polish
  (that is Phase 7).
- **Lobby concurrency hardening** — the actor's existing
  single-goroutine ownership is unchanged, but new public read
  helpers (room snapshot, member list) gain `sync.RWMutex` or
  atomic-snapshot semantics where the net layer reads from
  reader goroutines.

Phase 6 also adds the **200-concurrent-room** stress test and the
**Playwright deep-link e2e** per SPEC §8 Phase 6 "Tests".

Specifically, Phase 6 ships:

1. `internal/lobby/chat.go` — `LobbyChat` envelope handler,
   room-scoped broadcast, 1 KiB per-message size cap.
2. `internal/lobby/kick.go` — host-only kick action; the kicked
   player receives `error{code: KICKED}` and is removed from the
   room (slot freed for future join).
3. `internal/lobby/levels.go` — preset table + validation for
   `level` strings of the form `<letter><number>` (e.g. `"A1"`).
4. `internal/lobby/gc.go` — per-room inactivity sweeper + match-
   abandonment detector (called from the existing janitor tick).
5. `internal/lobby/deltas.go` — delta-encoded roomList push
   protocol.
6. `internal/proto/lobby.go` extensions — new envelope types
   `LobbyChat`, `LobbyKick`, `LobbyRoomDelta` (envelope only — the
   binary `schemaChecksum` is unchanged).
7. `web/src/lobby.ts` — TS lobby client.
8. `web/src/main.ts` — top-level state machine
   (`LOBBY` / `IN_MATCH` / `POST_MATCH`).
9. `web/src/lobbyUI.ts` — minimal DOM-driven lobby UI (room list,
   create-room form, chat pane, level picker). Render polish is
   Phase 7.
10. `web/tests/lobby.test.ts` — vitest coverage for the TS lobby
    client.
11. `web/tests/e2e/deeplink.spec.ts` — Playwright deep-link e2e.

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./internal/lobby/...` is green on the
   four SPEC §12-mandated native CI runners (linux/amd64,
   linux/arm64, darwin/arm64, windows/amd64). The Phase 1
   `TestDeterminism_GoldenFingerprint` baseline continues to
   pass byte-for-byte (Phase 6 does not touch `internal/sim`).
2. `go test -race -count=1 ./internal/net/...` is green: the
   Phase 2 lobby integration test continues to pass, augmented
   with chat-broadcast / kick-host / room-delta-push wire tests.
3. `go test -race -count=1 ./internal/match/...` is green
   (unchanged — Phase 6 doesn't edit `internal/match`).
4. `go test -race -count=1 -tags testhooks ./...` is green —
   includes the Phase 5 testhook-fenced lives/score tests; Phase 6
   adds no new test-only mutators to `internal/sim` or
   `internal/match`.
5. `pnpm -C web test` (vitest) is green: `lobby.test.ts` exercises
   the TS lobby client end-to-end against a fake WebSocket; the
   existing P4 prediction/interp/netClient/proto tests still pass.
6. `pnpm -C web test:e2e` (Playwright) is green: `deeplink.spec.ts`
   spawns 2 browser contexts and verifies the deep-link flow.
7. `TestLobby_ChatBroadcastToRoom`: A creates room R; B joins R; A
   sends `chat{roomId: R, text: "hi"}`; B receives a JSON envelope
   `chat_relay{roomId: R, fromNick: "A", text: "hi"}` within one
   actor tick.
8. `TestLobby_KickHost`: host A kicks player B; B receives
   `error{code: "KICKED"}` and a follow-up `kicked{roomId: R}` and
   is removed from the room; A's subsequent `roomList` shows the
   freed slot.
9. `TestLobby_KickHost_NonHostRejected`: B (non-host) sends
   `kick{roomId: R, sessionId: A.id}`; the lobby responds
   `error{code: NOT_HOST}` and ignores.
10. `TestLobby_LevelValidation`: `createRoom{level: "Z9"}` accepted;
    `level: "B0"` rejected with `error{code: BAD_LEVEL}`; `"AB1"`
    rejected; `""` rejected.
11. `TestLobby_RoomGCAfterInactivity`: a room with no members for
    30 s is removed; the janitor logs `room_gc{reason: empty}` and
    a subsequent `roomList` does not include it.
12. `TestLobby_MatchEndsWithZeroPlayers`: a match whose every
    player has left (DC-grace expired or clean leave) emits
    `match_end{reason: ALL_ELIMINATED}` after 30 s of zero-player
    state. This DoD item composes Phase 5 §9.2 (`dropDCSlot`) with
    the new lobby-side detection.
13. `TestLobby_RoomListLiveUpdatesAfter1Tick`: 2 mock connections;
    A creates a room; B receives the delta `room_added{...}`
    within one actor tick (≤ 10 ms in the test fixture, which uses
    a 1 ms janitor tick).
14. `TestLobby_200ConcurrentRoomsNoPanic`: 200 mock connections
    each call `createRoom` simultaneously; no panic; all rooms
    have unique IDs; the post-condition `len(l.rooms) == 200`.
15. `TestLobby_DeepLinkJoinFlow` (vitest): the TS lobby client
    given `window.location.search = "?room=ABCD23"` issues
    `hello` then immediately `joinRoom{roomId: "ABCD23"}` without
    user click.
16. `TestLobby_DeepLinkPlaywright_E2E` (Playwright): two browser
    contexts; A creates a room; B opens `?room=<A's room ID>`; both
    contexts receive `matchStarted` after host A clicks "start".
17. Coverage ≥ 80 % statements for `internal/lobby` (raised from
    Phase 2's loose target since Phase 6 lands the bulk of the
    chat/kick/GC surface); ≥ 70 % for `internal/match` and
    `internal/net` (unchanged from P5 §22 #14).
18. `schemaChecksum` unchanged from Phase 2 (`0x42607394`); the
    binary match-WS protocol is **byte-identical**. Lobby JSON
    envelopes gain new `t` values (`chat`, `chat_relay`, `kick`,
    `kicked`, `room_added`, `room_updated`, `room_removed`); the
    JSON envelope is versioned by the `v: 1` field already in
    Phase 2 and these new types are additive — every existing
    Phase 2 envelope continues to round-trip unchanged.

---

## 2. Out of scope

Explicitly **not** built in Phase 6:

- **Animations, sprites, audio, HUD polish** — that is Phase 7
  per SPEC §8. Phase 6's lobby UI is minimal DOM (room cards,
  create-room form, plain `<input>` for chat). Sprite work and
  the in-match HUD are not touched.
- **Rebinds / settings persistence** — Phase 7.
- **Matchmaking ranking / Elo / queue mode** — not in v1 per SPEC
  §1.2 non-goal "no progression system, ranking, accounts".
  Phase 6's "matchmaking" is room-list browsing only.
- **Server-list / cross-server federation** — single-server v1.
- **Persistent rooms across server restart** — rooms live in-memory
  only; restart drops every active room. The spec is silent here;
  documented as accepted in §22 open question #2.
- **Spectator slots in lobby** — distinct from dead-cam (§3.9);
  v1.1 candidate.
- **Lobby chat history / scrollback** — chat is fire-and-forget;
  a fresh joiner sees no prior messages. Persistent chat scrollback
  is a v1.1 candidate.
- **Per-room password / invite-only** — `?room=ID` deep-link is
  the only invite mechanism; v1.1 may add `&pwd=` query.

Phase 6 does **not** change the binary match-WS protocol. The
`internal/proto` *binary* schemaDescriptor is **byte-identical**.
Lobby JSON gains 3 inbound envelope types (`chat`, `kick`,
`leaveRoom` — the last already exists) and 4 outbound (`chat_relay`,
`kicked`, `room_added`, `room_updated`, `room_removed`); these
ride the same `{ "t": "...", "v": 1, "d": {...} }` envelope and
do not change `schemaChecksum`.

---

## 3. Prerequisites and assumptions

- Phase 2's `internal/lobby` actor (`Lobby.Run`,
  `controlMsg`-driven inbox, sessions, rooms, token janitor) is
  on `main`. Phase 6 layers new control messages on top.
- Phase 2's `internal/proto/lobby.go` JSON envelope schema
  (`{ t, v, d }`) with `v = 1`. Phase 6 adds new `t` values; `v`
  does not bump.
- Phase 2's `cmd/isnipes` already mounts `/ws/lobby` and
  `/ws/match/:id`. Phase 6 keeps both paths.
- Phase 5's `internal/match` reconnect / dead-cam machinery is on
  `main`. Phase 6's "match-abandonment" detection uses
  `Match.State() == StateEnded` as the terminal signal AND tracks
  the per-match "zero connected players" duration.
- Go 1.22+; `math/rand/v2` for room-ID generation; `crypto/rand`
  for `joinToken` (already in P2's `internal/lobby/token.go`).
- Node.js ≥ 20 for vitest + Playwright. New devDependencies:
  - `@playwright/test` (Phase 7 will also use it — Phase 6 adds it
    here so the deep-link e2e is gate-able).
- Level table (A–Z × 1–9) is the same set as `internal/sim/levels.go`
  §3.7; Phase 6 mirrors it on the lobby side but does **not**
  duplicate the parameter math — only the (letter, number) tuple
  validation. The match actor still calls `sim.LookupLevel` at
  match construction; the lobby's role is purely admission.
- Room IDs are 6-char base32 per SPEC §6.3. P2's `newRoomID()`
  already generates these; Phase 6 documents the contract and
  adds collision-retry (currently the P2 path doesn't retry).
- Sessions are 1-per-WS; the lobby actor owns session state.
  Disconnect detection is the existing P2 `ctlDisconnect`.

---

## 4. Package and file layout

New files in Phase 6:

```
internal/lobby/
├── chat.go              # NEW — chat envelope handler + broadcast
├── chat_test.go         # NEW
├── kick.go              # NEW — host-only kick action
├── kick_test.go         # NEW
├── levels.go            # NEW — level-string validation + preset table
├── levels_test.go       # NEW
├── gc.go                # NEW — per-room + per-match inactivity sweeper
├── gc_test.go           # NEW
├── deltas.go            # NEW — room delta build + push
├── deltas_test.go       # NEW

internal/proto/
├── lobby_chat.go        # NEW — `chat`, `chat_relay` envelope structs
├── lobby_kick.go        # NEW — `kick`, `kicked` envelope structs
├── lobby_deltas.go      # NEW — `room_added` / `room_updated` /
│                        # `room_removed` envelope structs

web/src/
├── main.ts              # NEW — top-level state machine
├── lobby.ts             # NEW — lobby client (createRoom / joinRoom /
│                        # leaveRoom / startMatch / chat / kick / deep-link)
├── lobbyUI.ts           # NEW — minimal DOM lobby UI

web/tests/
├── lobby.test.ts        # NEW — vitest unit tests for lobby.ts
├── e2e/
│   └── deeplink.spec.ts # NEW — Playwright deep-link e2e

web/playwright.config.ts # NEW — Playwright project config
```

Existing files modified by Phase 6:

```
internal/lobby/
├── lobby.go     # adds ctlChat, ctlKick to controlMsg union; routes
│                # to chat.go / kick.go handlers. handleStartMatch
│                # records the match-id → room mapping for the
│                # zero-player abandonment detector in gc.go.
├── room.go      # adds Level field (validated upstream); members
│                # is now a map[SessionID]struct{} (was a slice in
│                # P2 — slice scan is O(N) on every chat/kick).

internal/proto/
├── lobby.go     # adds new LobbyMsgType constants:
│                # LobbyChat, LobbyKick, LobbyChatRelay, LobbyKicked,
│                # LobbyRoomAdded, LobbyRoomUpdated, LobbyRoomRemoved.
│                # schemaDescriptor UNCHANGED — JSON envelope types
│                # are not in the descriptor (only binary frames are).

cmd/isnipes/
├── main.go      # serve /web/dist statically (already done in P2 if
│                # web/dist exists); Phase 6 ensures the dev-mode
│                # `--web-proxy=http://localhost:5173` flag is wired
│                # so `pnpm dev` is the supported developer loop.

web/
├── index.html   # adds <div id=app> mount point; minimal markup.
├── package.json # adds @playwright/test devDep + e2e script.
├── vite.config.ts  # adds the /ws/lobby + /ws/match WS proxy
│                   # for `pnpm dev`.
```

The `internal/proto/checksum.go` is **NOT** edited.
`web/src/proto.ts` is **NOT** edited.

---

## 5. Public API additions

### 5.1 Lobby (`internal/lobby`)

```go
// SubmitChat posts a chat message from a session into their current
// room. Non-blocking; drops on a full inbox. Sessions not in a room
// receive an error{code: NOT_IN_ROOM} reply.
//
// AIDEV-NOTE: this is the lobby-WS chat path. In-match chat (the
// 0x05 binary Chat frame on the match WS) is unchanged from P2 and
// is Phase 7 polish — neither path touches the other.
func (l *Lobby) SubmitChat(sid SessionID, roomID, text string)

// SubmitKick posts a host kick. The host's SessionID is taken from
// the room.HostID for authentication; the target's ID is supplied.
// Rejected if sid != room.HostID. Idempotent.
func (l *Lobby) SubmitKick(sid SessionID, roomID string, target SessionID)
```

### 5.2 Proto (`internal/proto`)

```go
// Lobby envelope additions. v=1 unchanged; t values are additive.
const (
    LobbyChat         = "chat"          // C2S
    LobbyChatRelay    = "chat_relay"    // S2C
    LobbyKick         = "kick"          // C2S — host only
    LobbyKicked       = "kicked"        // S2C — to the evictee
    LobbyRoomAdded    = "room_added"    // S2C — delta
    LobbyRoomUpdated  = "room_updated"  // S2C — delta
    LobbyRoomRemoved  = "room_removed"  // S2C — delta
)

type LobbyChatPayload struct {
    RoomID string `json:"roomId"`
    Text   string `json:"text"` // 1..1024 bytes UTF-8
}

type LobbyChatRelayPayload struct {
    RoomID   string    `json:"roomId"`
    FromNick string    `json:"fromNick"`
    Text     string    `json:"text"`
    Ts       int64     `json:"ts"`     // server unix-ms at relay time
}

type LobbyKickPayload struct {
    RoomID string `json:"roomId"`
    Target string `json:"sessionId"` // target SessionID
}

type LobbyKickedPayload struct {
    RoomID string `json:"roomId"`
    Reason string `json:"reason"` // "kicked-by-host"
}

type LobbyRoomDeltaPayload struct {
    Room RoomDescriptor `json:"room"`
}
```

### 5.3 Client (`web/src/`)

```ts
// lobby.ts
export interface LobbyClient {
  connect(url: string, nick: string): Promise<void>;
  onWelcome(handler: (w: Welcome) => void): void;
  onRoomListChange(handler: (rooms: Room[]) => void): void;
  onChat(handler: (msg: ChatRelay) => void): void;
  onMatchStarted(handler: (m: MatchStarted) => void): void;
  createRoom(name: string, max: number, level: string): Promise<void>;
  joinRoom(roomId: string): Promise<void>;
  leaveRoom(): Promise<void>;
  startMatch(): Promise<void>;
  sendChat(roomId: string, text: string): void;
  kick(roomId: string, target: string): void;
  disconnect(): void;
}

// main.ts — top-level state machine.
export type AppState = "LOBBY" | "IN_MATCH" | "POST_MATCH";

export class App {
  // Reads window.location for ?room=XXXXXX and seeds the auto-join.
  start(): void;
}
```

---

## 6. Lobby chat (§6 protocol)

### 6.1 Wire shape

Inbound JSON envelope:

```json
{ "t": "chat", "v": 1, "d": { "roomId": "ABCD23", "text": "hi" } }
```

Outbound JSON envelope (broadcast to every session in that room):

```json
{ "t": "chat_relay", "v": 1, "d": { "roomId": "ABCD23", "fromNick": "alice", "text": "hi", "ts": 1715990000000 } }
```

### 6.2 Validation

- `text` must be 1..1024 bytes UTF-8 after `strings.TrimSpace`.
  Empty → `error{code: BAD_REQUEST}`. >1024 bytes → `error{code:
  TEXT_TOO_LONG}`.
- The sender must be in the named room. Mismatch → `error{code:
  NOT_IN_ROOM}`.
- Per-session rate limit: max 4 messages per 2 seconds (token-bucket
  with burst 4, refill 1 per 500 ms). Exceeding rate → `error{code:
  RATE_LIMITED}`. The token bucket lives on `Session`.

### 6.3 Broadcast

The actor walks `room.Members` and pushes a `chat_relay` envelope
to each member's `out` channel. The sender receives their own
message echoed (so a single render path drives both "I sent" and
"I received"). Delivery is best-effort: if a member's `out` queue
is full, the message is silently dropped *for that member only*;
the chat path does not abort or close the slot.

### 6.4 No in-match chat in this phase

The binary `Chat` (0x05) frame on the match WS continues to be
defined but unused in Phase 6. Wiring it through the match actor
is Phase 7. SPEC §6 places chat in the lobby; the match-WS chat
is a render concern.

---

## 7. Kick-host action (§6.3)

### 7.1 Wire shape

Inbound:

```json
{ "t": "kick", "v": 1, "d": { "roomId": "ABCD23", "sessionId": "S-Bob" } }
```

Outbound to the evictee:

```json
{ "t": "kicked", "v": 1, "d": { "roomId": "ABCD23", "reason": "kicked-by-host" } }
```

Outbound to remaining members: a fresh `room_updated` envelope
reflecting the reduced member count.

### 7.2 Authorization

- The sender's `SessionID` must equal `Room.HostID` for the named
  room. Otherwise → `error{code: NOT_HOST}` and the action is a
  no-op.
- A host cannot kick themselves: `target == HostID` →
  `error{code: BAD_REQUEST}`. Hosts who want to leave use
  `leaveRoom` (which transfers host to the next-oldest member —
  see §7.4).

### 7.3 Effect

- Remove `target` from `room.Members`.
- Send the `kicked` envelope to the evictee.
- The evictee's `Session.RoomID` is cleared; they remain connected
  to the lobby and continue to receive `roomList` updates.
- Broadcast `room_updated{...}` to remaining members (the freed
  slot is now joinable).
- If `target` had a pre-allocated `joinToken` from a Phase-2
  `startMatch` flow that has not yet been used, the token is
  invalidated (the corresponding `match.PendingJoin` is rejected
  on subsequent `MatchJoin` with `Close{4001 AUTH}`).

### 7.4 Host transfer on leaveRoom

When the host calls `leaveRoom`, the host role transfers to the
**oldest joined remaining member** (smallest `Member.JoinedAt`
timestamp; ties broken by ascending SessionID). Phase 2 already
removes the host on `leaveRoom` but doesn't transfer hostship —
Phase 6 adds the transfer. If the host was the last member, the
room is GC'd (see §10).

---

## 8. Level selector and preset table

### 8.1 Validation

`CreateRoom.level` is the canonical wire field. Phase 6 enforces:

- Format: `<letter><number>` where `letter ∈ [A-Za-z]`,
  `number ∈ [1-9]`. Length is exactly 2 chars; lowercase is
  canonicalised to upper.
- Examples: `"A1"`, `"Z9"`, `"f3"` (→ `"F3"`).
- Rejected: `""`, `"A"`, `"A0"`, `"AA1"`, `"A10"`, `"@1"`, `"ZZ"`.

Validation is enforced in `lobby/levels.go::ParseLevel(string)`,
returning `(letter byte, number int, err error)`. The actor calls
this on every `createRoom`.

### 8.2 Preset preview metadata

For each (letter, number) pair, the lobby maintains a short
preview metadata struct (computed once at boot):

```go
type LevelPreset struct {
    Letter        byte   `json:"letter"`
    Number        int    `json:"number"`
    Difficulty    string `json:"difficulty"`     // Easy/Medium/Hard/Brutal
    PlayerLives   int    `json:"playerLives"`
    Generators    int    `json:"generators"`
    MaxSnipes     int    `json:"maxSnipes"`
    Description   string `json:"description"`    // short prose
}
```

The bucket name (`Easy`/`Medium`/`Hard`/`Brutal`) maps from the
letter bucket per §3.7:

- A..F → `"Easy"`
- G..M → `"Medium"`
- N..S → `"Hard"`
- T..Z → `"Brutal"`

`Description` is one short sentence describing the bucket's
behavioural change (e.g. "Snipes lead targets and shoot every
0.5 s.").

### 8.3 LobbyLevels envelope

A new outbound envelope `level_presets` is sent **once per session,
just after the `welcome`**, carrying the full 26 × 9 = 234-entry
preview table. The client renders the level picker from this
payload so future preset tweaks do not require client redeploys.

```json
{ "t": "level_presets", "v": 1, "d": { "presets": [ ... 234 entries ... ] } }
```

The payload is approximately 30 KiB JSON — well within WS frame
limits.

---

## 9. Deep-link URL `?room=ABCD23`

### 9.1 Client behaviour

`main.ts` reads `window.location.search` on boot:

```ts
const params = new URLSearchParams(window.location.search);
const roomCode = params.get("room");
```

If `roomCode` is present AND is 6 chars of `[A-Z0-9]`:

1. Prompt for nick if `localStorage.nick` is empty.
2. Open `/ws/lobby`, send `hello{nick, clientVersion,
   schemaChecksum}`.
3. On `welcome`, immediately send `joinRoom{roomId: roomCode}`
   without waiting for `roomList`.
4. On the join failure path (`error{code: ROOM_NOT_FOUND}` or
   `error{code: ROOM_FULL}`), surface the error in the UI and
   strip the `?room=` query so the back/forward button doesn't
   re-trigger.

### 9.2 Server-side behaviour

No new server-side code is required. The lobby's existing
`handleJoinRoom` already handles unknown / full rooms. Phase 6
just adds the auto-join client wiring.

### 9.3 Edge cases

- `roomCode` is a 6-char base32 string. The client validates the
  format pre-send; a malformed code is treated as "no deep-link"
  and the user lands in the regular lobby.
- The server canonicalises room IDs to upper-case at create time;
  the client uppercases the `roomCode` before joining.

---

## 10. Per-room and per-match GC (§6.3)

### 10.1 Empty-room sweep

Every janitor tick (already runs every 10 s in Phase 2), the actor
walks `l.rooms`:

```go
for rid, room := range l.rooms {
    if len(room.Members) == 0 {
        if l.clock().Sub(room.EmptySince) >= 30*time.Second {
            l.gcRoom(rid, "empty")
        }
    } else {
        room.EmptySince = time.Time{} // reset if rejoined
    }
}
```

`Room.EmptySince` is set when the last member leaves. If anyone
rejoins within the 30 s window, the timer resets.

### 10.2 Match-abandonment sweep

When a `Match` enters `StateEnded` (Phase 5 §13), the registry
already removes it. But a *live* match where every player's slot
has been DC'd or kicked or `leaveRoom`'d must also end. Phase 6
detects this via:

```go
type matchTracker struct {
    matchID        string
    zeroPlayersAt  time.Time // first tick we observed 0 joined slots
}

// Called from the janitor tick.
func (l *Lobby) sweepMatches() {
    for _, mt := range l.matches {
        m := l.reg.Get(mt.matchID)
        if m == nil || m.State() == match.StateEnded {
            delete(l.matches, mt.matchID)
            continue
        }
        if liveCount(m) == 0 {
            if mt.zeroPlayersAt.IsZero() {
                mt.zeroPlayersAt = l.clock()
                continue
            }
            if l.clock().Sub(mt.zeroPlayersAt) >= 30*time.Second {
                m.Abort("zero-players")
            }
        } else {
            mt.zeroPlayersAt = time.Time{}
        }
    }
}
```

`liveCount(m)` calls a new `Match.JoinedCount()` public method —
returning the count of joined+not-DC slots. The lobby cannot read
match-internal state directly; the public API is added in iteration 4
of this loop (and lives in `internal/match/match.go`).

### 10.3 Janitor cadence

The janitor already fires every 10 s. The 30 s GC threshold means
the worst-case delay between "deserves GC" and "actually GC'd" is
40 s. Acceptable per SPEC §6.3 ("after 30 s" reads as a soft lower
bound, not a hard deadline).

For tests, `Config.JanitorIntervalForTest` overrides the 10 s to
1 ms so per-room GC fires within a few sleeps. Reuses the Phase 5
testhooks pattern.

---

## 11. roomList delta-push protocol

### 11.1 Phase 2 behaviour (unchanged)

On `welcome`, the server sends the full `roomList`. This stays.

### 11.2 New: per-change delta envelope

After the initial `roomList`, subsequent room changes (create / fill
/ start / end / GC) emit a single delta envelope to every connected
session:

- `room_added{room: RoomDescriptor}` — on `createRoom`.
- `room_updated{room: RoomDescriptor}` — on member join/leave/kick
  AND on state transitions (`OPEN → STARTING → IN_MATCH → CLOSED`).
- `room_removed{roomId: string}` — on GC OR final `IN_MATCH → CLOSED`.

The full `roomList` is **not** re-broadcast. Clients build their
local view from the initial `roomList` and apply deltas in order.

### 11.3 Sequencing

Deltas are emitted from the actor goroutine, so they are
naturally serialised. The client is expected to apply them
strictly in the order received (the WS preserves order).

### 11.4 Client-side reconstruction

The TS lobby client maintains a `Map<roomId, Room>` keyed on
`room.id`. Each delta merges into or removes from the map. The
client UI re-renders the room list on every delta.

### 11.5 Backward compatibility

A Phase-2-vintage TS client receiving a `room_added` envelope
would log "unknown type room_added" and ignore it. The room list
would then go stale relative to the server. **This is acceptable
because the TS client mirror is in the same repo and is updated
in lockstep with the server.** External / 3rd-party clients are
not supported in v1.

---

## 12. Match-end → lobby flow (§6.2)

When a match transitions to `StateEnded` (Phase 5):

1. The Phase 5 `endMatch` already sends `MatchOver` to every
   joined slot AND emits an internal `ctlMatchEnded` to the lobby
   (existing P2 wiring).
2. The lobby's `handleMatchEnded` already exists in P2. Phase 6
   amends it to:
   - Mark the room state as `OPEN` (not `CLOSED` — players return
     to the same room and can start another match).
   - Re-issue `joinToken`s to surviving members are NOT minted
     here; that happens on the next `startMatch`.
   - Emit `room_updated{...}` to every session (the room is now
     joinable again).
3. The match-WS for each player is closed by Phase 5's
   `closeSlot`. The lobby-WS stays open per SPEC §6.2 step 5.

This is a behavioural change from Phase 2 (which had no
re-joinable state for ended matches). Phase 6 explicitly closes
the loop: a match end returns players to the lobby and they may
start another.

---

## 13. Wire protocol notes

The binary match-WS protocol is **byte-identical** to Phase 5.
`schemaChecksum = 0x42607394`. `internal/proto/checksum.go` is
NOT edited.

The lobby JSON envelope schema (`{ t, v, d }`) is at `v: 1`
unchanged. Phase 6 adds these envelope types:

| Direction | `t` | Payload |
|---|---|---|
| C2S | `chat` | `{roomId, text}` |
| C2S | `kick` | `{roomId, sessionId}` |
| S2C | `chat_relay` | `{roomId, fromNick, text, ts}` |
| S2C | `kicked` | `{roomId, reason}` |
| S2C | `room_added` | `{room: RoomDescriptor}` |
| S2C | `room_updated` | `{room: RoomDescriptor}` |
| S2C | `room_removed` | `{roomId}` |
| S2C | `level_presets` | `{presets: [LevelPreset...]}` |

Every existing P2 envelope (`hello` / `welcome` / `roomList` /
`createRoom` / `joinRoom` / `leaveRoom` / `startMatch` /
`matchStarted` / `error`) continues to round-trip unchanged.

`web/src/proto.ts` is **not** edited; lobby envelopes are
hand-rolled JSON on the TS side and the new `t` values are
literals in `lobby.ts`.

---

## 14. Determinism rules

Phase 6 touches only `internal/lobby`, `internal/proto`, and the
TS client. **It does not touch `internal/sim`.** The Phase 1
baseline.hash and Phase 3 phase3_pve.hash continue to pass
byte-for-byte. The Phase 5 score-extended fingerprint is unchanged.

Lobby state is not deterministic — `time.Now()` and `crypto/rand`
for room IDs are both observed-time / observed-entropy sources.
The lobby tests inject a `Clock` and a deterministic ID generator
(already in P2's `Config`); Phase 6 adds no new randomness.

---

## 15. Concurrency rules

### 15.1 Actor model preserved

The lobby's single-goroutine ownership of `l.sessions` and
`l.rooms` is preserved. Every new feature (chat, kick, delta
push, GC) runs from `handleControl` in the actor's `Run` loop.

### 15.2 Net-layer reads

The net layer's reader goroutines call `Lobby.PostMessage(sid,
raw)` (existing P2 API) which posts a `ctlMessage` to the actor.
Phase 6 adds no new direct-read APIs on `Lobby`; everything
funnels through the inbox.

The match registry's `Get(matchID)` (used by the new
`sweepMatches`) is called only from the actor goroutine — no
new synchronisation is needed.

### 15.3 200-room concurrency test

`TestLobby_200ConcurrentRoomsNoPanic` spawns 200 goroutines each
calling `lobby.SubmitCreateRoom`-equivalent (via `PostMessage`
with a synthesised `createRoom` envelope). All 200 messages
queue in the actor's inbox; the actor drains them sequentially.
The test asserts: no panic, exactly 200 distinct rooms, every
room has a valid 6-char base32 ID, the post-condition
`l.rooms` map has 200 entries.

This test does NOT exercise concurrent writes to `l.rooms` from
multiple goroutines; it exercises concurrent producers feeding
a single actor consumer. The actor's single-threaded ownership
is the safety property under test.

---

## 16. Test plan

### 16.1 Chat (`internal/lobby/chat_test.go`)

- `TestLobby_ChatBroadcastToRoom` — DoD #7. Two mock sessions in
  one room; sender sees their own echo; both see the relay.
- `TestLobby_ChatRequiresMembership` — sender not in the named
  room → `error{NOT_IN_ROOM}`.
- `TestLobby_ChatRejectsEmpty` — empty text after trim →
  `error{BAD_REQUEST}`.
- `TestLobby_ChatRejectsOversize` — 1025-byte text →
  `error{TEXT_TOO_LONG}`.
- `TestLobby_ChatRateLimit` — 5 messages in 1 s; the 5th
  receives `error{RATE_LIMITED}`.
- `TestLobby_ChatTrimsWhitespace` — leading/trailing whitespace
  is removed before broadcast.

### 16.2 Kick (`internal/lobby/kick_test.go`)

- `TestLobby_KickHost` — DoD #8.
- `TestLobby_KickHost_NonHostRejected` — DoD #9.
- `TestLobby_KickHost_SelfKickRejected` — host kicks self →
  `error{BAD_REQUEST}`.
- `TestLobby_KickHost_UnknownTarget` — target session is not in
  the room → `error{NOT_IN_ROOM}` (about the target).
- `TestLobby_KickInvalidatesJoinToken` — if the evictee had a
  pre-allocated `joinToken` from a pending `startMatch`, that
  token is invalidated; subsequent MatchJoin on the match WS
  returns `Close{4001 AUTH}`.

### 16.3 Levels (`internal/lobby/levels_test.go`)

- `TestLobby_LevelValidation` — DoD #10 — table-driven across
  every (letter, number) pair plus malformed examples.
- `TestLobby_LevelPresetTable` — every (letter, number) pair has
  a `LevelPreset` entry; counts equal sim.LookupLevel results.
- `TestLobby_LevelPresetsEnvelopeOnWelcome` — the welcome reply
  is followed by `level_presets` with 234 entries.

### 16.4 GC (`internal/lobby/gc_test.go`)

- `TestLobby_RoomGCAfterInactivity` — DoD #11.
- `TestLobby_RoomGCResetsOnRejoin` — a member rejoins within the
  30 s window; the room survives.
- `TestLobby_MatchEndsWithZeroPlayers` — DoD #12.
- `TestLobby_MatchAbandonmentResetsOnRejoin` — a player
  reconnects within the 30 s zero-player window; the match
  continues.

### 16.5 Deltas (`internal/lobby/deltas_test.go`)

- `TestLobby_RoomListLiveUpdatesAfter1Tick` — DoD #13.
- `TestLobby_RoomDelta_AddedOnCreate`.
- `TestLobby_RoomDelta_UpdatedOnJoin`.
- `TestLobby_RoomDelta_UpdatedOnKick`.
- `TestLobby_RoomDelta_RemovedOnGC`.
- `TestLobby_RoomDelta_OrderingPreserved` — issuing 10 rapid
  state changes; client-side reconstruction matches the final
  authoritative state.

### 16.6 Concurrency (`internal/lobby/lobby_test.go` extension)

- `TestLobby_200ConcurrentRoomsNoPanic` — DoD #14.
- `TestLobby_500ConcurrentMessagesNoPanic` — synthetic-tagged
  stress test; 500 random envelopes from 50 goroutines; no panic.

### 16.7 Client (`web/tests/lobby.test.ts`)

- `TestLobbyClient_HelloWelcomeRoundTrip`.
- `TestLobbyClient_CreateRoomEmitsExpectedFrame`.
- `TestLobbyClient_ApplyRoomDelta_Added`.
- `TestLobbyClient_ApplyRoomDelta_Updated`.
- `TestLobbyClient_ApplyRoomDelta_Removed`.
- `TestLobbyClient_DeepLinkAutoJoin` — DoD #15.
- `TestLobbyClient_ChatRoundTrip`.
- `TestLobbyClient_KickEvictionHandled`.

### 16.8 E2E (`web/tests/e2e/deeplink.spec.ts`)

- `TestLobby_DeepLinkPlaywright_E2E` — DoD #16. Two browser
  contexts; A creates a room (note the room ID from the UI),
  B opens `?room=<that ID>`; A clicks "start"; both contexts
  navigate to the match view (asserted via a known `data-testid`
  marker on the in-match canvas wrapper).

The Playwright config:
- `testDir: ./tests/e2e`
- `webServer`: spawns `cmd/isnipes` on a free port (uses Go's
  `httptest`-like single-binary mode; the binary needs a
  `--web-dist=./web/dist` flag).
- Two `chromium` contexts; **Chromium-only** per SPEC §8 Phase 7
  "Default CI gate" (firefox + webkit are nightly).

---

## 17. Testdata

No new testdata fixtures. Phase 6 is logic / wiring, not replay.

Existing committed fixtures are unchanged:
- `testdata/replays/baseline.hash` (Phase 5 regen).
- `testdata/replays/phase3_pve.hash` (Phase 5 regen).
- `testdata/proto/checksum.txt` (`0x42607394`).

---

## 18. Risks

- **JSON envelope schema drift.** Adding new `t` values is
  additive but client/server can drift if either lags. Mitigation:
  the TS client is in the same repo and the `pnpm web test`
  CI gate exercises every envelope round-trip.
- **`level_presets` payload size.** 234 entries × ~150 bytes ≈
  35 KiB. WS frames are fine; the cost is one-time per session.
  Mitigation: pre-encode once at boot and reuse the byte slice.
- **Concurrency stress and inbox saturation.** 200 concurrent
  `createRoom` from 200 goroutines push 200 messages into a
  256-element inbox. If the inbox fills, `PostMessage` blocks
  the producer. Mitigation: Phase 2 sizes the inbox to 256 (P2's
  `make(chan controlMsg, 256)`); 200 < 256 fits.
- **Playwright flakiness.** WS races + race detector + headless
  browser have a documented baseline of ~3 % retry rate.
  Mitigation: 1 automatic retry on failure; `expect(... ).toPass`
  for state assertions.
- **Match-abandonment 30 s false positive.** A 4-player match
  where all 4 DC simultaneously and reconnect at the 29-second
  mark would, under a tight implementation, end the match. Spec
  permits this; the test `TestLobby_MatchAbandonmentResetsOnRejoin`
  pins the reset-on-rejoin behaviour.
- **Deep-link XSS.** A malicious `?room=<script>` URL must not
  inject. Mitigation: client validates `^[A-Z0-9]{6}$` before
  emitting `joinRoom`; non-matching values trigger the regular
  lobby boot.
- **Lobby chat as spam vector.** Rate limit (§6.2) is the primary
  mitigation. The 4-msg/2-sec bucket allows normal conversation
  but caps a malicious sender at ~120 msg/min — high enough to be
  annoying but below the "kicks the room" threshold. Operator
  may tune this in Phase 7.

---

## 19. Open questions

These are flagged for codex review and for the author to decide
before implementation begins. Defaults are listed if no decision
is forced.

1. **Persistent rooms across server restart.** Default: no
   persistence; restart drops every active room. The complexity
   of persistent room state isn't justified for v1's expected
   match length (≤ 10 min) and uptime model.
2. **Host transfer on leaveRoom — oldest member or random.** Spec
   is silent. Default: oldest by `JoinedAt`; ascending SessionID
   on tie. Predictable, no surprise. Alternative: random — keeps
   any single user from accumulating "host privilege" via
   join-order gaming.
3. **`level_presets` cadence.** Sent once on `welcome` only, or
   re-sent on every `roomList`? Default: once per session.
   Re-sending on every roomList is wasteful (the table never
   changes within a single server lifetime).
4. **Chat history scrollback.** Default: zero history. A fresh
   joiner sees only messages sent after they join. Implementing
   even a 50-line ring buffer is non-trivial under the
   actor-model invariant (a list-membership change would race
   with chat send). v1.1 candidate.
5. **`?room=` deep-link without pre-existing nick.** Default:
   prompt for nick via a one-shot dialog before the WS opens.
   Alternative: generate a random nick `Guest-N`. Default keeps
   identity explicit.
6. **Match-abandonment 30 s grace — uses lobby clock or sim
   tick?** Default: lobby clock. The match actor doesn't observe
   "everyone DC'd" except via per-tick slot inspection; the
   lobby observes connection state more directly. Using the lobby
   clock keeps the detection responsive to network events rather
   than tick cadence.
7. **Kick during `STARTING` state.** A host calls `startMatch` →
   tokens minted → host kicks a player before that player's
   `MatchJoin`. Default: the token is invalidated; the kicked
   player's `MatchJoin` fails with `Close{4001 AUTH}`. The
   remaining members still join; the match starts with one fewer
   player. Alternative: revert to `OPEN` if any pre-match kick
   happens. Default is simpler.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./internal/lobby/...` green; P1+P3 fingerprint goldens unchanged | CI |
| 2 | `go test -race -count=1 ./internal/net/...` green | CI |
| 3 | `go test -race -count=1 ./internal/match/...` green (no edits in Phase 6) | CI |
| 4 | `go test -race -count=1 -tags testhooks ./...` green | CI |
| 5 | `pnpm -C web test` green (vitest) | CI |
| 6 | `pnpm -C web test:e2e` green (Playwright; Chromium-only default) | CI |
| 7 | `TestLobby_ChatBroadcastToRoom` passes | §16.1 |
| 8 | `TestLobby_KickHost` passes | §16.2 |
| 9 | `TestLobby_KickHost_NonHostRejected` passes | §16.2 |
| 10 | `TestLobby_LevelValidation` passes | §16.3 |
| 11 | `TestLobby_RoomGCAfterInactivity` passes | §16.4 |
| 12 | `TestLobby_MatchEndsWithZeroPlayers` passes | §16.4 |
| 13 | `TestLobby_RoomListLiveUpdatesAfter1Tick` passes | §16.5 |
| 14 | `TestLobby_200ConcurrentRoomsNoPanic` passes | §16.6 |
| 15 | `TestLobbyClient_DeepLinkAutoJoin` (vitest) passes | §16.7 |
| 16 | `TestLobby_DeepLinkPlaywright_E2E` (Playwright) passes | §16.8 |
| 17 | Coverage ≥ 80 % `internal/lobby`; ≥ 70 % `internal/match` and `internal/net` (unchanged from P5) | CI |
| 18 | `schemaChecksum` unchanged from Phase 2 (`0x42607394`) | `TestSchemaChecksumValue` |

Items 1–18 are gate-able in CI.

The binary wire protocol is unchanged from Phase 5. The lobby JSON
envelope schema adds 8 new `t` values; every existing envelope
continues to round-trip unchanged. `web/tests/proto.test.ts` from
Phase 2 is unmodified.
