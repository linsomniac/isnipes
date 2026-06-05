# Lobby Rework — Design

- **Date:** 2026-06-05
- **Status:** Approved (ready for implementation planning)
- **Author:** brainstormed with Sean

## Summary

Rework the isnipes lobby so it (1) looks polished and on-theme with the
in-game neon/CRT graphics, (2) carries built-in How-to-Play instructions
(objective, controls, scoring), and (3) stops showing stale finished games —
replacing them with a "your last match" results recap.

The lobby today is **100% unstyled, browser-default HTML** built imperatively in
`web/src/browser.ts` (`buildDOM`). There is no lobby CSS at all (only the match
view has an injected `<style>`). That is the entire reason it looks plain. The
rework is therefore mostly: inject a themed `<style>` for `#lobby` and
restructure the existing DOM into a "Command Deck" layout, keeping every
`data-testid` the e2e tests depend on.

## Goals

1. **Prettier lobby** — neon/CRT theme matching the game, "Command Deck" layout.
2. **Instructions** — always-visible cheat bar + expandable full guide.
3. **No stale games** — finished rooms disappear from the list (server fix);
   the returning player sees a "Last Match" recap card (client-only).

## Non-goals / out of scope

- **Global results board** for *other players'* finished games. That needs new
  data on the lobby socket, but `internal/proto/lobby.go` is frozen and has no
  result fields. Explicitly **not** doing a frozen-protocol re-seed here.
- **Dead-player spectator scroll** (pan the map with movement keys after your
  last life). Captured separately as a future idea; not part of this work.
- Redesigning the level-picker *information architecture* (still A1–Z9). We only
  re-style it. A difficulty-grouped picker is a possible later improvement.

## Background — grounded findings

### Client (web/src), editable vs frozen
- Frozen web mirrors (do **not** edit): `prediction.ts`, `interp.ts`,
  `netClient.ts`, `proto.ts`, `sim.ts` (per `scripts/frozen.sha256`).
- Editable and relevant: `browser.ts` (all lobby DOM + MatchRunner), `lobby.ts`
  (protocol client), `main.ts` (App state machine), `scenes.ts` (golden
  harness), `hud.ts` (MatchOver/scoreboard decoders), `settings.ts`, `palette.ts`.
- Lobby rendering: `buildDOM()` (browser.ts:93–188) creates plain elements via
  `el()`. Lobby visibility toggles with `.hidden`; `injectMatchStyles()`
  (browser.ts:204–224) is the existing pattern for an injected `<style>`.
- Theme to match (palette.ts): bg `#0a0a0f`, walls `#2b2b40`, self `#4fd1ff`,
  players `#8aff80`, snipes `#ff5a5a`, generators `#ffd166`, text `#e6e6f0`,
  neon wall stroke `#1aa0e6` / glow `#34bdf0`; monospace; CRT scanlines.

### MatchOver data already on the client (recap source)
- `MatchRunner.onFrame` → `case MsgType.MatchOver` (browser.ts:499) decodes the
  frame (`decodeMatchOver`, hud.ts:96) and builds an `EndDialog` via
  `buildEndDialog(mo, nickById)` (hud.ts:138) — `{reason, winnerId, rows:[{id,
  nick, lives, score}]}`, already nick-joined and score-ordered. The in-match
  end dialog renders exactly this. The recap reuses the same `EndDialog`.

### Server room lifecycle + the stale-games bug (editable — not frozen)
- `internal/lobby`, `internal/match`, `internal/net` are **all editable**
  (only `internal/sim` and `internal/proto` are frozen).
- `Lobby.handleMatchEnded(ctlMatchEnded)` (lobby.go:654) ALREADY does the right
  thing: set room `CLOSED`, `delete(l.rooms, id)`, clear each member's
  `s.roomID`, then `broadcastRoomList()`. **It is simply never invoked in
  production** — nothing posts `ctlMatchEnded`. There is no public poster for it
  (compare `Touch`/`Disconnect`).
- Match end happens in `Registry.Create`'s goroutine: `go func(){ m.Run();
  r.RemoveEnded(mc.MatchID) }()` (registry.go:79–82). No callback reaches the
  lobby today.
- Consequence: finished rooms linger (in `STARTING`/`CLOSED`) until
  `sweepEmptyRooms` GC's them ~30s after they empty — and members' `roomID` is
  never cleared, so a returning player is wedged "already in a room".

### Tests / build constraints
- e2e (`deeplink.spec.ts`, `play.spec.ts`) assert visibility/clicks on:
  `lobby`, `create-room`, `my-room`, `my-room-id`, `room-players`, `start-btn`.
  These MUST keep working.
- Golden screenshots `empty-lobby.png`, `full-lobby.png` (golden.spec.ts via
  `scenes.ts` lobby scenes) WILL change → re-baseline with `--update-snapshots`.
  Running Playwright locally needs the gitignored `web/playwright.local.config.ts`
  chrome override (NixOS).
- Known trap: a `position:fixed` view with only absolutely-positioned children
  collapses `document.body` to 0px height and breaks `toBeVisible()` /
  scene-ready markers. The lobby must stay in normal flow with a sized shell.
- Build: `npm -C web run build`. Dev view: `go run ./cmd/isnipes --web-dist
  web/dist --insecure-origin --addr :8080`. Frozen guard: `make check-frozen`.

## Design

### Workstream A — Visual rework ("Command Deck")

**A1. Styling mechanism.** Add `injectLobbyStyles()` in `browser.ts`, mirroring
`injectMatchStyles()`: a single `<style id="isnipes-lobby-style">` scoped to
`#lobby`, called once at boot. All visual styling lives there (classes), not
inline. No CSS files, no framework.

**A2. Layout.** Restructure the `#lobby` subtree in `buildDOM()` into:
- **Header:** neon `ISNIPES` wordmark (glow) + tagline + connection status dot +
  nick (the nick input moves here as the primary identity control; it keeps its
  `nick-input` testid).
- **Left "Play" panel** (`glow` accent): `Create Room` button, `my-room` block
  (id, `room-players`, invite link, `start-btn`, `start-hint`), then the
  **Level** grid (`level-picker` with `level-cell` buttons) + `level-preview`.
- **Right column:** **Live Games** panel (`room-list`, `room-row`, `join-room`,
  `no-rooms`) stacked over the **Last Match** recap card (Workstream C).
- **How to Play** full-width bar (Workstream B).
- **Settings** footer: collapsible `<details>`-style panel keeping the existing
  `settings` section and all its controls (preset/colorblind/highcontrast/
  retrofx/volume/server). Collapsed by default.

**A3. Theme + scanlines.** Dark neon palette via CSS custom properties seeded
from `palette.ts` values. A subtle CRT scanline overlay on the lobby shell via a
`repeating-linear-gradient` pseudo-element, `pointer-events:none`, gated by a
class toggled from `settings.retroFx` (and OS reduce-motion at first run, same
rule as the game). Selected `level-cell` gets an `on` class (glow).

**A4. Testid preservation.** Every existing testid stays on the same logical
element (may move in the tree, must remain present, visible, and clickable):
`lobby, status, create-room, nick-input, my-room, my-room-id, room-players,
start-btn, start-hint, room-link, invite, room-list, room-row, no-rooms,
join-room, level-picker, level-preview, level-cell, settings, howto, howto-box,
preset-select, colorblind-toggle, highcontrast-toggle, retrofx-toggle, volume,
server-input, server-add, server-list, server-row, server-select`. New elements
get new testids (`lobby-header`, `howto-cheat`, `howto-full`, `howto-toggle`,
`last-match`, `last-match-dismiss`).

**A5. Zero-height guard.** `#lobby` stays in normal document flow inside a
`.lobby-shell` with `min-height:100vh` so `document.body` has height (golden
lobby scenes mark `document.body` as scene-ready). No `position:fixed` on lobby
content.

### Workstream B — Instructions

**B1. Cheat bar (always visible)** inside a How-to-Play panel:
- Move: `↑ ↓ ← →` · Fire: `W A S D` · Turbo: `Space` · Chat: `T` · Scores: `Tab`
- Scoring: snipe **+1**, generator **+10**, player kill **+25**, your death **−5**

**B2. "More ▾" expander** (`howto-toggle` → shows `howto-full`) with the full
guide, all drawn from code/SPEC so it stays accurate:
- **Objective:** destroy all generators + snipes (≥1 player alive), or be the
  last player standing; if the 10-min timer expires, highest score wins.
- **Controls — Classic (default):** move arrows, fire WASD, turbo Space.
  **Modern:** move WASD, fire arrows, turbo Shift. **Vi-keys** h/j/k/l always
  move. Chat `T` (Enter send / Esc cancel). Fullscreen `f`.
- **Lives & respawn:** lives = `max(1, 10 − level number)`; 3s respawn with 2s
  spawn invulnerability; at 0 lives you enter dead-cam (watch + chat).
- **Tips:** generators are high value and spawn snipes; you can't fire while
  turboing; higher letters (T–Z) are brutal (tougher gens, faster snipes).

The legacy `howto`/`howto-box` "How to start a game" steps are folded into this
panel (kept as testids; content refreshed).

### Workstream C — Finished games: remove + recap

**C1. Server: invoke the existing cleanup.**
- Add `func (l *Lobby) MatchEnded(matchID string)` — public poster that sends
  `ctlMatchEnded{matchID}` to `l.in`, guarded by `l.stopped` and `select` on
  `l.done` (mirrors `Touch`/`Disconnect`). Non-blocking.
- Add `OnMatchEnded func(matchID string)` to `match.RegistryConfig` (nil-safe).
  Invoke it in the `Create` goroutine after `m.Run()` returns (alongside
  `RemoveEnded`).
- Wire them at the production construction site `cmd/isnipes/main.go:86–98`,
  where `registry` is built *before* `lob`. Pre-declare the lobby and close over
  it:
  ```go
  var lob *lobby.Lobby
  registry := match.NewRegistry(match.RegistryConfig{
      // …existing hooks…
      OnMatchEnded: func(id string) { lob.MatchEnded(id) },
  })
  lob = lobby.NewLobby(lobby.Config{Registry: registry, /* … */})
  ```
  The closure captures `lob` by reference; it is assigned before any match can
  start, so the callback never sees a nil lobby. The load-test harness
  (`internal/loadtest/driver.go:93–101`) leaves `OnMatchEnded` nil — nil-safe,
  no change needed.
- `handleMatchEnded` itself is unchanged (already correct). Net effect: on any
  match termination (normal end, abort, zero-player, shutdown) the room is
  removed for all clients and members' `roomID` is cleared.

**C2. Client: "Last Match" recap (no protocol change).**
- `MatchRunner` exposes `lastResult(): EndDialog | null` (returns
  `this.hud.endDialog`). The MatchOver handler already populates it.
- In `boot()`'s `backBtn.onclick` (and any path returning to the lobby), capture
  `runner.lastResult()` into a `lastMatchResult` variable before `runner.stop()`,
  then render the **Last Match** card: end reason (`reasonText`), winner
  (resolve from rows by `winnerId`, else "No single winner"), and the ordered
  score rows (`nick  score  (lives)`). Card has a dismiss (`last-match-dismiss`)
  and is cleared when a new match starts (`onMatchStarted`).
- In-memory only (the SPA toggles `.hidden`, never reloads between match and
  lobby), so no storage needed. Recap exists only for the match this client
  played; a fresh page load shows no card.

## Data flow (recap)

```
match WS ──MatchOver──▶ MatchRunner.onFrame ──buildEndDialog──▶ hud.endDialog
                                                                     │
in-match end dialog ◀────────────────────────────────────────────────┤
                                                                     │ Back to lobby
lobby "Last Match" card ◀──lastMatchResult = runner.lastResult()─────┘

lobby WS ──roomList/room_removed──▶ LobbyClient ──▶ renderRooms (finished rooms gone)
   ▲
   └── server: m.Run() ends ─OnMatchEnded─▶ Lobby.MatchEnded ─ctlMatchEnded─▶ handleMatchEnded
```

## Testing strategy

- **TDD, server:** lobby test — posting `MatchEnded(matchID)` for a room's match
  removes the room from `l.rooms`, clears each member's `roomID`, and broadcasts
  an updated room list. Registry test — `OnMatchEnded` is invoked with the
  matchID when a match's `Run()` returns (e.g. via abort/shutdown of a real
  match), exactly once.
- **TDD, client helpers:** any new pure helper (e.g. winner resolution from rows,
  recap view-model builder) gets a vitest unit test. DOM wiring in `browser.ts`
  is covered by e2e, not vitest (consistent with current split).
- **e2e regression:** `deeplink.spec.ts` + `play.spec.ts` must stay green
  (testids preserved). Optionally extend an e2e to assert the room list empties
  after a match and the Last Match card appears.
- **Goldens:** re-baseline `empty-lobby.png` + `full-lobby.png`
  (`npm -C web run test:e2e -- --update-snapshots`, with the local chrome
  override). The recap card is hidden in the golden lobby scenes (no prior
  match), so `scenes.ts` needs only minimal/no change.
- **Frozen guard:** `make check-frozen` stays green (no frozen edits).

## Risks & edge cases

- **Construction-order wiring** of `OnMatchEnded`↔`Lobby`: resolve via
  closure-over-variable; verify the real construction site during planning.
- **Double cleanup:** `handleMatchEnded` is idempotent (room already deleted →
  loop finds nothing). Safe if `MatchEnded` is posted more than once.
- **Shutdown race:** `MatchEnded` guards on `l.stopped`/`l.done`; safe during
  graceful shutdown when both lobby and matches are stopping.
- **Golden churn:** expected and intentional; baselines updated in the same
  change so CI is green.
- **Layout collapse:** mitigated by A5 (normal flow + sized shell).

## Verification commands

```
# build + view
npm -C web run build
go run ./cmd/isnipes --web-dist web/dist --insecure-origin --addr :8080   # http://localhost:8080

# tests
go test ./internal/lobby/... ./internal/match/...        # server TDD
npm -C web test                                          # vitest
npm -C web run test:e2e                                  # e2e + goldens
npm -C web run test:e2e -- --update-snapshots            # re-baseline lobby goldens
make check-frozen                                        # frozen guard
```

## File-by-file change list (anticipated)

- `web/src/browser.ts` — `injectLobbyStyles()`; restructure `#lobby` in
  `buildDOM()`; How-to-Play cheat bar + expander; Last Match card render +
  capture in `backBtn`/`onMatchStarted`; `MatchRunner.lastResult()`; settings
  collapsible; scanline class from `settings.retroFx`.
- `web/src/scenes.ts` — only if lobby scene needs the new structure to render
  deterministically (recap hidden by default).
- `internal/lobby/lobby.go` — add `MatchEnded()` poster.
- `internal/match/registry.go` — add `OnMatchEnded` to `RegistryConfig`; invoke
  after `m.Run()`.
- `cmd/isnipes/main.go` (≈86–98) — wire `OnMatchEnded → lob.MatchEnded` via the
  pre-declared-`lob` closure.
- Tests: `internal/lobby/*_test.go`, `internal/match/*_test.go`, web vitest +
  e2e; updated lobby golden snapshots.
- NOT touched: `internal/sim/**`, `internal/proto/**`, frozen web mirrors.
