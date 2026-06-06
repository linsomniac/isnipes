# Dead-Player Spectator Scroll — Design

- **Date:** 2026-06-05
- **Status:** Approved (ready for implementation planning)
- **Author:** brainstormed with Sean

## Summary

When a player uses their last life while the match is still going, today they
are visually **stuck**: the renderer has no self-entity to follow, so the camera
pins to world origin `(0,0)` and the minimap disappears. The player can only
chat and stare at the top-left corner until the match ends.

This feature turns that dead time into a **spectator camera**. The eliminated
player can:

1. **Free-pan** the map with the movement keys (arrows / WASD / `hjkl`, per
   preset), starting from where they died; turbo speeds the pan up.
2. **Follow a living player** — press `Tab` to snap the camera onto the next
   living player and track them; any movement key returns to free-pan.

A `SPECTATING` banner and the (re-enabled) minimap make the mode discoverable
and navigable.

**This is a purely client-side feature.** The server already streams everything
needed (see Background); no protocol, sim, or server change is required, and no
frozen file is edited.

## Goals

1. Eliminated players can look around the live match instead of being frozen at
   the origin.
2. Two camera modes: free-pan (movement keys) and follow-next-living-player
   (`Tab`), with a clear way to switch between them.
3. Clear on-screen affordance: a `SPECTATING` banner + a working minimap.
4. Logic lives in a small, pure, unit-tested module mirroring the existing
   `death.ts` `RespawnSequencer` pattern; `browser.ts` only wires it.

## Non-goals / out of scope

- **Server / protocol changes.** The dead-cam stream already exists. We do not
  touch `internal/**` or any frozen file.
- **Spectating after the match ends.** `MatchOver` → the existing POST_MATCH end
  dialog. Spectator UI hides on `MatchOver`.
- **Spectating during the normal 3 s respawn** (when you still have lives). That
  is the existing `RespawnSequencer` dead-cam hold and is unchanged. Spectator
  scroll engages **only on elimination** (lives reach 0).
- **Prev-player cycling.** v1 cycles **next-only** with wraparound. Prev (`Q`/a
  second key) is a trivial later add.
- **Fixing the server's 64-entity dead-cam cap.** On a very busy map a far
  player can be culled from the dead-cam snapshot; we degrade gracefully
  (follow auto-advances) rather than change the frozen-adjacent server cap.

## Background — grounded findings

### Server already streams the full world to dead players (no change needed)
- On elimination the match flips the slot into dead-cam:
  `slot.DeadCam = true` (`internal/match/match.go:860–868`, "Phase 5 §8.2:
  transition newly-eliminated slots into dead-cam").
- `buildSnapshotFor` (`internal/match/snapshot.go:15–23`) routes dead-cam slots
  to `buildDeadCamSnapshotFor` (`snapshot.go:25–95`): an **unfiltered** snapshot
  of every *living* entity (`e.Flags&FlagDead` are skipped, `snapshot.go:40`),
  capped at 64 by Chebyshev distance from **map centre**, emitted ID-ascending,
  with `YourEntityID = 0`.
- Dead-cam slots **ignore Input frames** server-side (`match.go:587–589`,
  "dead-cam slots accept Chat only"). So repurposing the movement keys
  client-side is free of any server contract — input we keep sending is dropped.
- Consequence for the client: **every player in a dead-cam snapshot is alive**,
  so the client's "living players" filter is just `kind === EntityKind.Player`
  (`EntityKind` from `web/src/registry.ts:18`, `Player = 1`) — no flag check
  needed.

### The client gap (what we fix)
- `MatchRunner.drawFrame` (`web/src/browser.ts:797–840`): when eliminated,
  `latest.yourEntityID === 0` ⇒ `selfEntity = null` ⇒ `self = null`.
- `RespawnSequencer.update` eliminated branch (`web/src/death.ts:92–101`)
  returns `cameraOverride: null` (plus a decaying red sting, `dimAlpha = 0`).
- `Renderer.camera` (`web/src/render.ts:335–339`) then falls back to
  `s.cameraOverride?.x ?? s.selfPredicted?.x ?? 0` ⇒ **camera origin `(0,0)`**.
- The minimap call is gated on `self` (`browser.ts:828`), so it vanishes too.

### Camera + input plumbing we reuse
- `computeCamera(selfX, selfY, vp, maze)` (`render.ts:99–115`) centres a world
  point in the viewport and clamps to maze bounds. `cameraOverride` already pins
  this centre (`render.ts:337`). The spectator module outputs a world centre;
  the existing override path consumes it unchanged.
- `InputController.intent()` (`web/src/input.ts:133–141`) already turns held
  movement keys into a `Dir8` (`dir`) plus `turbo` — preset-aware and including
  the always-on `hjkl` aliases. We read `dir`/`turbo` for pan; **no new pan
  input plumbing**.
- Pan-speed grounding (`web/src/sim.ts`, frozen — imported, not edited):
  `SUBTILE_PER_TILE = 256`, `PLAYER_SPEED = 112`, `PLAYER_TURBO_SPEED = 224`
  (subtiles/tick @ 30 Hz).

### HUD + minimap surfaces
- `renderHud` (`browser.ts:541–574`) updates DOM overlays; the `RESPAWNING n`
  overlay (`respawn-overlay`, created `browser.ts:217`, rendered `:569–573`) is
  the exact pattern for a new `spectator-banner` overlay. `HudModel` already has
  an (unused-in-render) `deadCam` field and `respawnCountdown` (`hud.ts:27–41`).
- `drawMinimap(ui, maze, self, entities)` (`browser.ts:577–590`) +
  `plotMinimap` (`hud.ts:183–199`) plot `self` as a cyan reticle and other
  entities as red dots. We reuse it during spectating with the **camera centre**
  as the `self` reticle and all snapshot entities as dots.

### Frozen-file check
- Frozen client mirrors are `web/src/{proto,sim,prediction,interp,netClient}.ts`
  (`scripts/frozen.sha256`). **None of the files we edit are frozen.** We
  *import read-only* from frozen `sim.ts` (constants) and `proto`/`interp`
  (types), which does not modify them.

## Design (Approach A — pure module + thin wiring)

### 1. New module `web/src/spectator.ts` (pure, clock-free)

Mirrors `death.ts`: no DOM, no canvas, no clock read — the caller passes the
frame delta so it is display-rate independent and unit-testable.

```ts
import { Dir } from "./sim.js";

export type SpectatorMode = "free" | "follow";

export interface SpectatorPlayer { id: number; x: number; y: number }

export interface SpectatorInput {
  dtMs: number;                 // frame delta (performance.now() diff), clamped
  panDir: Dir;                  // movement-key Dir8 (Dir.Idle = no pan)
  turbo: boolean;               // turbo key held → fast pan
  cycleEdge: boolean;           // true only on the frame Tab was pressed
  players: SpectatorPlayer[];   // living players this frame (kind===Player)
  bounds: { worldW: number; worldH: number }; // subtile maze extents
}

export interface SpectatorOutput {
  center: { x: number; y: number }; // camera centre → cameraOverride
  mode: SpectatorMode;
  followId: number | null;          // for the banner ("Following …")
}

export class SpectatorCamera {
  start(pos: { x: number; y: number }): void; // seed free-pan at death spot
  update(input: SpectatorInput): SpectatorOutput;
}
```

**Tunable constants (top of file):**
- `PAN_SPEED` ≈ `PLAYER_TURBO_SPEED * TICKS_PER_SEC` (~6720 subtiles/s) — a
  brisk free look.
- `PAN_TURBO_MULT = 2` — turbo key doubles pan speed.
- `MAX_DT_MS = 100` — clamp the frame delta so a tab-out / GC pause cannot fling
  the camera across the map.

**Behaviour:**
- **Seed:** `start(deathPos)` sets `mode="free"`, `center=deathPos`,
  `followId=null`.
- **Free-pan:** `center += velocityFor(panDir, PAN_SPEED·turboMult) · dt/1000`
  (reuse `sim.velocityFor` so diagonals match game movement), then clamp
  `center` to `[0, worldW]×[0, worldH]`. The renderer's `computeCamera` does the
  final viewport-edge clamp; clamping the centre too keeps free/follow coherent
  and stops the centre drifting off-map.
- **Cycle (`cycleEdge`):** advance `followId` to the next living player after the
  current one in **id-ascending** order (wrap to first; if none, stay free); set
  `mode="follow"`.
- **Follow:** `center =` the followed player's current position (looked up in
  `players` by id) each frame.
  - Target missing from `players` (eliminated, or culled by the 64-cap) →
    auto-advance to the next living player; if the list is empty, drop to
    `mode="free"` holding the last centre.
  - A non-idle `panDir` → release to `mode="free"` at the current centre.

### 2. `web/src/death.ts` (small change)

The eliminated branch (`death.ts:92–101`) is the origin-jump bug. Change it to:
- return `cameraOverride: { ...this.deathPos }` (hold the death spot during the
  red sting, instead of `null`), and
- add `spectating: boolean` to `DeathFxOutput`, `true` exactly while the
  eliminated-dead branch is active (and `false` everywhere else).

`browser.ts` uses `fx.spectating` to know when to engage the spectator camera
and `fx.cameraOverride` (= death spot) to seed it on the first spectating frame.
The red sting (`redAlpha`) still plays over the spectator view for its first
~400 ms; `dimAlpha` stays 0 so the view is bright.

### 3. `web/src/browser.ts` wiring

- **`MatchRunner` fields:** add `private spectator = new SpectatorCamera()`, a
  `private spectating = false` latch (to seed once), a `lastFrameMs` for `dt`,
  and an edge flag `cyclePressed` set by the keydown handler.
- **`drawFrame`:** after computing `fx = this.respawn.update(...)`, branch:
  - If `fx.spectating`:
    - On the first spectating frame, `this.spectator.start(fx.cameraOverride!)`.
    - `const dt = now - this.lastFrameMs` (clamped in the module);
      `const intent = this.input.intent()`.
    - `const players = latest.entities.filter(e => e.kind === EntityKind.Player)
        .map(e => ({ id: e.id, x: e.x, y: e.y }))` (add `EntityKind` to the
        existing `./registry.js` import in `browser.ts:20`).
    - `const out = this.spectator.update({ dtMs: dt, panDir: intent.dir,
        turbo: intent.turbo, cycleEdge: this.consumeCycle(), players,
        bounds: { worldW: maze.W*256, worldH: maze.H*256 } })`.
    - Use `out.center` as `cameraOverride` in the `renderer.draw(...)` call
      (overriding the static death spot once panning starts).
    - Draw the minimap: `drawMinimap(ui, maze, out.center, latest.entities)`
      (camera reticle + all entities).
    - Set `hud.spectating = true` and `hud.spectatorFollow =
      out.mode === "follow" ? nickById.get(out.followId) ?? "…" : null`; force
      `hud.showScoreboard = true` (auto-show while spectating, see Controls).
  - Else (alive / normal respawn): unchanged; reset the seed latch and
    `hud.spectating = false`.
- **Cycle key (keydown):** in the existing `keydown` handler (where `Tab`/`F`
  are special-cased, `browser.ts:716–730`), when spectating, treat `Tab` as the
  cycle edge (`this.cyclePressed = true; e.preventDefault()`) **instead of** the
  scoreboard toggle. `consumeCycle()` returns and clears the flag.
- **`tickInput` unchanged** — it keeps sending movement intent; the server's
  dead-cam guard drops it. Chat focus already suppresses key capture
  (`browser.ts:718`), so panning naturally pauses while typing.

### 4. Controls (resolved)

| Action | Key | Notes |
|---|---|---|
| Free-pan | movement keys (arrows/WASD per preset + `hjkl`) | releases follow |
| Fast pan | turbo (`Space` classic / `Shift` modern) | `PAN_TURBO_MULT×` |
| Follow next living player | `Tab` | wraps; auto-advances if target drops |
| Scoreboard | auto-shown while spectating | frees `Tab` for cycling |

While spectating the scoreboard is always visible (you're watching — standings
are relevant), which is what frees `Tab` from its alive-only hold-to-show role
and lets it cycle players, matching the approved mockup.

### 5. Visuals

- **`spectator-banner`** DOM overlay (created like `respawn-overlay`,
  `browser.ts:217`; styled in the injected match `<style>`): bottom-centre, neon
  text. Free: `SPECTATING — move to look around · Tab: next player`. Follow:
  `SPECTATING — Following <nick> · move: free look`. Rendered in `renderHud`
  from `hud.spectating` / `hud.spectatorFollow`; hidden otherwise and on
  `MatchOver` (gate on `!endDialog`).
- **Minimap** re-enabled during spectating (camera reticle cyan, all entities
  red — existing `plotMinimap` colours).
- View stays bright (no dim); the brief red death sting is unchanged.

## Edge cases

- **Match ends while spectating:** `MatchOver` builds the end dialog and calls
  `onEnd()`; banner + minimap hide (`!endDialog` gate). Existing flow.
- **Eliminated as the last player** (last-standing / all-eliminated): `MatchOver`
  arrives essentially immediately; spectator may never visibly engage. Fine.
- **Followed player culled by the 64-entity cap** (busy map, far from centre):
  treated as "left the list" → auto-advance; no crash, no stuck camera.
- **Reconnect while dead-cam:** server-handled; client resumes the dead-cam
  stream. Death spot is unknown after reconnect, so seed at map centre. Minor.
- **Chat while spectating:** keydown early-returns when the chat field is
  focused (`browser.ts:718`), so held keys aren't captured → pan pauses; keyup
  still clears, so no stuck pan.

## Testing

- **`web/src/spectator.test.ts` (vitest), the core coverage:**
  - free-pan integrates position by `dt` in the `panDir` direction; diagonals
    use `velocityFor`; `dt` clamped at `MAX_DT_MS`.
  - centre clamps to `[0, world]` on every edge.
  - turbo multiplies pan speed by `PAN_TURBO_MULT`.
  - `cycleEdge` advances next-player id-ascending, wraps, and no-ops on an empty
    list.
  - follow tracks a moving target across frames.
  - follow auto-advances when the target leaves `players`; drops to free when
    none remain.
  - a non-idle `panDir` releases follow → free at the current centre.
  - `start()` seeds free-pan at the death spot.
- **`web/src/death.test.ts`:** eliminated branch now returns
  `cameraOverride === deathPos` and `spectating === true` (and `false` in alive
  / respawn / fadein).
- **Live e2e (Playwright), optional/stretch:** drive a 2-player match to one
  player's elimination, assert the `spectator-banner` appears and the camera
  responds to a movement key. Heavier (needs a real elimination); the unit tests
  carry the logic, so this is a nice-to-have, not a gate.
- No golden-frame change is required; the spectator camera is dynamic. A single
  static "spectator framing at a known centre" golden could be added if cheap,
  but is not planned.

## File-by-file change list

| File | Change | Frozen? |
|---|---|---|
| `web/src/spectator.ts` | **new** pure `SpectatorCamera` state machine | no |
| `web/src/spectator.test.ts` | **new** unit tests | no |
| `web/src/death.ts` | eliminated branch holds `deathPos`; add `spectating` to output | no |
| `web/src/death.test.ts` | assert new eliminated-branch behaviour | no |
| `web/src/browser.ts` | wire spectator into `drawFrame`; `Tab`→cycle while dead; banner + minimap; injected `<style>` for `spectator-banner` | no |
| `web/src/hud.ts` | add `spectating` / `spectatorFollow` to `HudModel` (+ `emptyHudModel`) | no |
| `web/src/render.ts` | none expected (reuses `cameraOverride`) | no |

## Open questions / future work

- **Prev-player cycling** (`Q`, or `Shift+Tab`) — trivial extension once
  next-only lands.
- **Highlight the followed player** on the minimap / with a ground ring — minor
  polish, deferred.
- **Smoothed follow** (lerp toward the target instead of hard-centre) — only if
  hard-centre feels jumpy in playtest.
