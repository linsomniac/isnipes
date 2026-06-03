# Death → Respawn Camera Hold + Red Sting + Fade/Countdown

**Status:** Approved (design)
**Date:** 2026-06-03
**Scope:** Client-only (`web/src`). No server, sim, or proto changes.

## Problem

On a normal death (the player still has lives remaining), the gameplay camera
jumps to the maze's top-left corner for the full respawn delay, then jumps again
to wherever the player respawns. There is no on-screen feedback that the player
died. It reads as two jarring, unexplained camera teleports.

### Root cause (confirmed)

When a player's HP reaches 0 with lives remaining, the server:

1. Sets `FlagDead` on the marine and starts a fixed **90-tick respawn timer**
   (`TickHz = 30` → **3.0 seconds**; `internal/match/match.go`,
   `internal/sim` respawn path).
2. Excludes the dead marine from that player's own AOI snapshot
   (`internal/match/aoi.go` only includes self when `Flags&FlagDead == 0`).
3. Sends `YourEntityID = 0` for the duration of the dead window
   (`aoi.go`: `yourID` is only set when `hasSelf`).

On the client (`web/src/browser.ts` `drawFrame`):

```ts
const selfEntity = latest.entities.find((e) => e.id === latest.yourEntityID) ?? null; // null while dead
const self = selfEntity ? { x, y, facing, flags } : null;                              // → null
```

`self` becomes `null`, so `render.ts` `camera()` falls back to
`selfPredicted?.x ?? 0` → camera origin (0,0) = maze top-left. At respawn the
marine reappears at a server-chosen tile, `YourEntityID` is restored, and the
camera snaps there.

Elimination (out of all lives) is a **separate** path: the slot transitions to
server-side **dead-cam** (`slot.DeadCam`, set only when `sim.Eliminated(pid)`).
That path also sends `YourEntityID = 0` but the player will not respawn. This
spec does **not** change dead-cam/elimination behavior.

### Relevant existing facts

- A **death-poof** overlay already exists and is armed on the kill *target*
  (`web/src/anim.ts` `OverlayManager.onEvent` → `arm("poof", target)`), but it
  anchors to the registry entry, which is rebuilt from each snapshot
  (`web/src/registry.ts`). Once the dead marine leaves the snapshot the anchor
  is gone and the poof is dropped. With the camera fix the poof at least renders
  centered for the frame(s) it survives.
- The wire proto is **frozen** (`scripts/frozen.sha256` covers `internal/proto`,
  `internal/sim`, and the five mirrors `web/src/{proto,prediction,interp,
  netClient,sim}.ts`). The files this spec touches — `web/src/browser.ts`,
  `web/src/render.ts`, `web/src/hud.ts`, and a new `web/src/death.ts` — are
  **not** frozen.
- Because the proto is frozen, the server only reveals the *new* spawn location
  at the instant the marine reappears. The client therefore cannot pre-pan to
  the spawn point; the camera move to spawn must happen at the respawn instant.
  This constraint is why the chosen design hides that move behind a fade.
- `web/src/browser.ts` `renderTick` increments once per `requestAnimationFrame`
  (display-refresh dependent), so it is **not** a reliable wall clock. The death
  sequence is timed with `performance.now()` instead.

## Behavior

A client-side state machine drives the 3-second dead window. Timing uses
`performance.now()`. The **authoritative end trigger** is the marine reappearing
in the snapshot (`selfPresent` going false → true), so the displayed countdown is
cosmetic and cannot desync from the real respawn.

```
t = 0.0s   Death detected.
           RED STING: screen flushes red (peak ~0.55 alpha).
           Camera LOCKS to the death spot.
t = 0–0.4s Red sting fades to 0. Camera still held at the death spot
           (the player sees where they died).
t ≈ 1.0s   Screen begins fading to dark (→ ~0.85 alpha black).
           "RESPAWNING" + countdown number appears (DOM overlay).
t ≈ 3.0s   Marine reappears at the server-chosen spawn.
           Camera follows the marine; dark fades back out (~0.4s).
           Because the screen was dark across the teleport, the
           death-spot → spawn camera move is invisible.
```

Net effect: the origin-jump is eliminated, death gets clear feedback, and the
unavoidable death-spot → spawn camera move is concealed by the fade.

### Phases

`Alive → Dead → FadeIn → Alive`

- **Alive → Dead:** `selfPresent` true → false while the match is live and a
  prior `lastSelfPos` exists. Record `deathPos = lastSelfPos`,
  `deathAtMs = nowMs`, and `eliminated = livesRemaining <= 0`.
- **Dead (eliminated):** red sting only — no `cameraOverride`, no dim, no
  countdown. The existing dead-cam / match-over flow takes over.
- **Dead (will respawn):** `cameraOverride = deathPos`; red sting curve over the
  first ~0.4s; dim ramps from ~1.0s to ~0.85 alpha. Countdown is `null` until the
  dim begins (`elapsed < HOLD_MS`), then `ceil((RESPAWN_MS − elapsed) / 1000)`
  clamped to ≥ 1 — i.e. it appears together with the dim and counts down (~"2",
  "1") for the remainder of the window. It is cosmetic; reappearance, not the
  countdown reaching 0, ends the sequence.
- **Dead → FadeIn:** `selfPresent` false → true while not eliminated. Camera
  follows the (now-present) marine (`cameraOverride = null`); dim decays to 0
  over ~0.4s; red = 0; countdown = null.
- **FadeIn → Alive:** dim fully decayed.

### Edge cases

- **Guard:** never enter `Dead` without a prior `lastSelfPos` (died before first
  sighting).
- **Safety timeout:** if `Dead` and not eliminated for more than
  ~5s without reappearance (disconnect, match end), force-clear so the overlay
  cannot get stuck.
- **Lives lookup during the dead window:** `YourEntityID == 0` while dead, so the
  scoreboard-row lookup keyed on `yourEntityID` fails. The entity id is stable
  across respawn, so `browser.ts` remembers `lastKnownSelfId` (the last non-zero
  `yourEntityID`) and uses it to read self's `lives` from `hud.rows` while dead.

## Architecture

Designed for isolation: the decision logic is a pure module; `browser.ts` wires
it; `render.ts` and `hud.ts` only render the outputs.

### New: `web/src/death.ts` — `RespawnSequencer` (pure)

```ts
export interface DeathFxInput {
  selfPresent: boolean;
  selfPos: { x: number; y: number } | null;
  livesRemaining: number;
  nowMs: number;
}
export interface DeathFxOutput {
  cameraOverride: { x: number; y: number } | null;
  redAlpha: number;   // 0..1
  dimAlpha: number;   // 0..1
  countdown: number | null; // integer seconds, or null
}
export class RespawnSequencer {
  update(in: DeathFxInput): DeathFxOutput;
}
```

- Holds phase state, `deathPos`, `deathAtMs`, `lastSelfPos`, `eliminated`,
  `fadeInStartMs`, and all timing constants / alpha curves.
- No DOM, no canvas, no direct clock read (caller passes `nowMs`) → fully
  unit-testable with synthetic timestamps.

**Timing constants (tunable):** `RED_PEAK ≈ 0.55`, `RED_MS ≈ 400`,
`HOLD_MS ≈ 1000`, `DIM_RAMP_MS ≈ 400`, `DIM_MAX ≈ 0.85`, `RESPAWN_MS ≈ 3000`,
`FADEIN_MS ≈ 400`, `SAFETY_MS ≈ 5000`.

### `web/src/render.ts`

Decouple the camera from the self marine (today both flow through
`selfPredicted`):

- `RenderState.cameraOverride?: { x: number; y: number } | null`.
  `camera()` becomes `cameraOverride ?? selfPredicted ?? {0,0}`. While dead,
  `selfPredicted` stays `null` (no marine is drawn) and `cameraOverride` holds
  the camera at the death spot.
- `RenderState.deathFx?: { redAlpha: number; dimAlpha: number }`.
  In `draw()`: red `fillRect` over the canvas **before** the CRT pass (scanlines
  tint the red); dark `fillRect` over the canvas **after** the CRT pass (uniform
  cover). Both use `fillStyle = "rgba(...)"`; no `globalAlpha` needed (keeps the
  existing fake-ctx test pattern usable).

### `web/src/browser.ts`

- Add fields: `respawn = new RespawnSequencer()`, `lastSelfPos`,
  `lastKnownSelfId`.
- In `drawFrame`: compute `self`/`selfPresent` as today; resolve self's `lives`
  via `lastKnownSelfId`; call
  `const fx = this.respawn.update({ selfPresent, selfPos: self, livesRemaining, nowMs: performance.now() })`.
- Pass `cameraOverride: fx.cameraOverride` and
  `deathFx: { redAlpha: fx.redAlpha, dimAlpha: fx.dimAlpha }` into
  `renderer.draw(...)`. Keep `selfPredicted = self` (null while dead).
- Set `this.hud.respawnCountdown = fx.countdown` before `renderHud`.

### `web/src/hud.ts`

- Add `respawnCountdown: number | null` to `HudModel` (default `null` in
  `emptyHudModel`).
- `renderHud` shows/hides a centered "RESPAWNING n" DOM element in the match UI
  (crisp scalable text above the canvas dim; consistent with the existing
  DOM-HUD pattern). Element created in the match UI scaffold (`browser.ts` UI
  builder).

## Testing

- **`web/tests/death.test.ts` (new, vitest):**
  - Alive → Dead sets `cameraOverride = deathPos`.
  - Red-sting curve: peak near `t0`, `redAlpha == 0` by ~`RED_MS`.
  - Dim ramps only after `HOLD_MS`; reaches `DIM_MAX`.
  - Countdown returns descending integers ≥ 1.
  - `livesRemaining == 0` → sting only (no `cameraOverride`, no countdown, no
    dim).
  - Reappearance → FadeIn (`cameraOverride == null`, dim decays) → Alive.
  - Safety timeout clears stuck `Dead` state.
  - No-`lastSelfPos` guard: never enters `Dead`.
- **`web/tests/render.test.ts` (extend):**
  - `cameraOverride` takes precedence over `selfPredicted` in `camera()`.
  - `deathFx` alphas produce the red (pre-CRT) and dark (post-CRT) `fillRect`
    passes (recording-ctx pattern already present).
- **`web/tests/hud.test.ts` (extend):** `respawnCountdown` shows/hides the DOM
  element and renders the number.
- **Manual `/run` verify:** two clients, kill one, observe the sequence.
- **No Playwright goldens** added for this (standing note on CI golden
  flakiness), unless requested.

## Out of scope

- Server / sim / proto changes (proto frozen).
- Pre-panning to the spawn point before respawn (impossible without a proto
  change; the fade hides the move instead).
- Re-anchoring the existing death-poof to the held death spot (optional future
  enhancement; the red sting carries the moment).
- Dead-cam / elimination / match-over camera behavior.
