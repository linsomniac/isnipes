# Death → Respawn Camera Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the jarring camera origin-jump on death with a held-at-death-spot camera, a red sting flash, and a fade-to-dark "RESPAWNING" countdown that hides the teleport to the new spawn.

**Architecture:** A new pure state machine (`web/src/death.ts` `RespawnSequencer`) decides camera-hold position, red/dim alphas, and the countdown from `{selfPresent, selfPos, livesRemaining, nowMs}`. `browser.ts` feeds it `performance.now()` each frame and passes its outputs to the renderer (new `cameraOverride` + `deathFx` on `RenderState`) and the HUD (new `respawnCountdown`). Client-only; no server/sim/proto changes.

**Tech Stack:** TypeScript, Canvas2D, vitest. Spec: `docs/superpowers/specs/2026-06-03-death-respawn-camera-design.md`.

**Working directory for all commands:** `web/` (the TS package root). Run `cd web` first.

**Do not stage unrelated files.** The tree already has unrelated modifications to `scripts/frozen.sha256`, `web/src/proto.ts`, `web/tests/proto.test.ts`. Each commit below stages only the files it names.

---

## File Structure

- **Create** `web/src/death.ts` — `RespawnSequencer` pure state machine + alpha/countdown helpers + timing constants. One responsibility: decide the death-presentation outputs from frame inputs. No DOM/canvas/clock.
- **Create** `web/tests/death.test.ts` — unit tests for the sequencer and helpers.
- **Modify** `web/src/render.ts` — add `cameraOverride` and `deathFx` to `RenderState`; use them in `camera()` and `draw()`.
- **Modify** `web/tests/render.test.ts` — extend the recording ctx to capture `fillRect`; add camera-override + deathFx tests.
- **Modify** `web/src/hud.ts` — add `respawnCountdown` to `HudModel` + `emptyHudModel`.
- **Modify** `web/tests/hud.test.ts` — assert the new field defaults to `null`.
- **Modify** `web/src/browser.ts` — add a `respawn-overlay` DOM element + CSS; render `respawnCountdown` in `renderHud`; wire `RespawnSequencer` + `lastSelfPos` + `lastKnownSelfId` into `MatchRunner.drawFrame`.

---

## Task 1: `RespawnSequencer` pure module

**Files:**
- Create: `web/src/death.ts`
- Test: `web/tests/death.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/tests/death.test.ts`:

```ts
// Death → respawn sequencer (web/src/death.ts). Pure unit tests: synthetic
// timestamps drive the curves so there is no clock/display dependence.
// Spec: docs/superpowers/specs/2026-06-03-death-respawn-camera-design.md.

import { describe, expect, test } from "vitest";
import {
  RespawnSequencer,
  redStingAlpha, dimRampAlpha, respawnCountdown,
  RED_PEAK, RED_MS, HOLD_MS, DIM_MAX, RESPAWN_MS, FADEIN_MS, SAFETY_MS,
} from "../src/death.js";

const ALIVE = (x: number, y: number, nowMs: number, lives = 3) =>
  ({ selfPresent: true, selfPos: { x, y }, livesRemaining: lives, nowMs });
const DEAD = (nowMs: number, lives = 3) =>
  ({ selfPresent: false, selfPos: null, livesRemaining: lives, nowMs });

describe("redStingAlpha", () => {
  test("peaks at t=0 and decays to 0 by RED_MS", () => {
    expect(redStingAlpha(0)).toBeCloseTo(RED_PEAK);
    expect(redStingAlpha(RED_MS / 2)).toBeCloseTo(RED_PEAK / 2);
    expect(redStingAlpha(RED_MS)).toBe(0);
    expect(redStingAlpha(RED_MS + 100)).toBe(0);
  });
});

describe("dimRampAlpha", () => {
  test("0 during the hold, ramps to DIM_MAX after HOLD_MS", () => {
    expect(dimRampAlpha(HOLD_MS - 1)).toBe(0);
    expect(dimRampAlpha(RESPAWN_MS)).toBeCloseTo(DIM_MAX);
  });
});

describe("respawnCountdown", () => {
  test("null during the hold, descending integers >= 1 after", () => {
    expect(respawnCountdown(HOLD_MS - 1)).toBeNull();
    const c = respawnCountdown(HOLD_MS + 1);
    expect(c).not.toBeNull();
    expect(c!).toBeGreaterThanOrEqual(1);
    expect(respawnCountdown(RESPAWN_MS + 500)).toBe(1); // clamped
  });
});

describe("RespawnSequencer", () => {
  test("stays idle while alive", () => {
    const s = new RespawnSequencer();
    const out = s.update(ALIVE(100, 200, 0));
    expect(out).toEqual({ cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null });
  });

  test("on death, holds camera at the last self position with a red sting", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(100, 200, 0));
    const out = s.update(DEAD(1));
    expect(out.cameraOverride).toEqual({ x: 100, y: 200 });
    expect(out.redAlpha).toBeCloseTo(RED_PEAK);
    expect(out.dimAlpha).toBe(0);
  });

  test("dims and counts down later in the dead window", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0));
    s.update(DEAD(1));
    const out = s.update(DEAD(1 + HOLD_MS + 200));
    expect(out.cameraOverride).toEqual({ x: 10, y: 20 });
    expect(out.dimAlpha).toBeGreaterThan(0);
    expect(out.countdown).toBeGreaterThanOrEqual(1);
  });

  test("eliminated (lives 0): red sting only, no hold/dim/countdown", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0, 0));
    const out = s.update(DEAD(1, 0));
    expect(out.cameraOverride).toBeNull();
    expect(out.redAlpha).toBeGreaterThan(0);
    expect(out.dimAlpha).toBe(0);
    expect(out.countdown).toBeNull();
  });

  test("respawn → fade-in (camera follows marine, dim decays) → alive", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0));
    s.update(DEAD(1));
    s.update(DEAD(1 + RESPAWN_MS)); // fully dimmed
    const fade = s.update(ALIVE(900, 900, 1 + RESPAWN_MS + 1)); // marine back at spawn
    expect(fade.cameraOverride).toBeNull(); // follows the live marine
    expect(fade.dimAlpha).toBeGreaterThan(0); // still fading in
    const done = s.update(ALIVE(900, 900, 1 + RESPAWN_MS + 1 + FADEIN_MS));
    expect(done.dimAlpha).toBe(0);
    expect(done.cameraOverride).toBeNull();
  });

  test("safety timeout clears a stuck dead state", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0));
    s.update(DEAD(1));
    const out = s.update(DEAD(1 + SAFETY_MS + 1));
    expect(out).toEqual({ cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null });
  });

  test("never enters dead without a prior self sighting", () => {
    const s = new RespawnSequencer();
    const out = s.update(DEAD(1));
    expect(out.cameraOverride).toBeNull();
    expect(out.redAlpha).toBe(0);
  });
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run tests/death.test.ts`
Expected: FAIL — `Cannot find module '../src/death.js'`.

- [ ] **Step 3: Write the implementation**

Create `web/src/death.ts`:

```ts
// Death → respawn presentation sequencer (client-only). Pure: no DOM, no
// canvas, no clock read — the caller passes performance.now() as nowMs so the
// curves are display-rate independent and unit-testable.
//
// Eliminates the camera origin-jump on death: holds the camera at the death
// spot, plays a red sting, fades to dark with a RESPAWNING countdown, then
// fades back in at the server-chosen spawn (hiding the teleport).
//
// Spec: docs/superpowers/specs/2026-06-03-death-respawn-camera-design.md.

export interface DeathFxInput {
  selfPresent: boolean;
  selfPos: { x: number; y: number } | null;
  livesRemaining: number;
  nowMs: number;
}

export interface DeathFxOutput {
  cameraOverride: { x: number; y: number } | null;
  redAlpha: number; // 0..1
  dimAlpha: number; // 0..1
  countdown: number | null; // integer seconds, or null when not shown
}

// Timing (ms) / intensity (alpha) constants. Tunable.
export const RED_PEAK = 0.55;
export const RED_MS = 400;
export const HOLD_MS = 1000;
export const DIM_RAMP_MS = 400;
export const DIM_MAX = 0.85;
export const RESPAWN_MS = 3000; // mirrors the server's 90-tick @ 30Hz timer
export const FADEIN_MS = 400;
export const SAFETY_MS = 5000; // bail-out if no respawn arrives (disconnect)

// redStingAlpha: peak at t=0, linear decay to 0 by RED_MS.
export function redStingAlpha(elapsedMs: number): number {
  if (elapsedMs <= 0) return RED_PEAK;
  if (elapsedMs >= RED_MS) return 0;
  return RED_PEAK * (1 - elapsedMs / RED_MS);
}

// dimRampAlpha: 0 until HOLD_MS, then ramps to DIM_MAX over DIM_RAMP_MS and holds.
export function dimRampAlpha(elapsedMs: number): number {
  if (elapsedMs <= HOLD_MS) return 0;
  const t = elapsedMs - HOLD_MS;
  if (t >= DIM_RAMP_MS) return DIM_MAX;
  return DIM_MAX * (t / DIM_RAMP_MS);
}

// respawnCountdown: null until the dim begins, then integer seconds left (>= 1).
export function respawnCountdown(elapsedMs: number): number | null {
  if (elapsedMs <= HOLD_MS) return null;
  return Math.max(1, Math.ceil((RESPAWN_MS - elapsedMs) / 1000));
}

type Phase = "alive" | "dead" | "fadein";

function idle(): DeathFxOutput {
  return { cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null };
}

export class RespawnSequencer {
  private phase: Phase = "alive";
  private deathPos = { x: 0, y: 0 };
  private deathAtMs = 0;
  private fadeInAtMs = 0;
  private eliminated = false;
  private lastSelfPos: { x: number; y: number } | null = null;

  update(input: DeathFxInput): DeathFxOutput {
    const { selfPresent, selfPos, livesRemaining, nowMs } = input;

    if (selfPresent && selfPos) this.lastSelfPos = { x: selfPos.x, y: selfPos.y };

    switch (this.phase) {
      case "alive": {
        if (!selfPresent && this.lastSelfPos) {
          this.phase = "dead";
          this.deathPos = { ...this.lastSelfPos };
          this.deathAtMs = nowMs;
          this.eliminated = livesRemaining <= 0;
        }
        return idle();
      }

      case "dead": {
        if (this.eliminated) {
          if (selfPresent) { this.phase = "alive"; return idle(); }
          return {
            cameraOverride: null,
            redAlpha: redStingAlpha(nowMs - this.deathAtMs),
            dimAlpha: 0,
            countdown: null,
          };
        }
        if (selfPresent) {
          // Marine reappeared at the new spawn → fade back in this frame.
          this.phase = "fadein";
          this.fadeInAtMs = nowMs;
          return this.update(input);
        }
        const elapsed = nowMs - this.deathAtMs;
        if (elapsed > SAFETY_MS) { this.phase = "alive"; return idle(); }
        return {
          cameraOverride: { ...this.deathPos },
          redAlpha: redStingAlpha(elapsed),
          dimAlpha: dimRampAlpha(elapsed),
          countdown: respawnCountdown(elapsed),
        };
      }

      case "fadein": {
        if (!selfPresent) {
          // Vanished again mid-fade — restart the dead handling.
          this.phase = "dead";
          this.deathAtMs = nowMs;
          if (this.lastSelfPos) this.deathPos = { ...this.lastSelfPos };
          this.eliminated = livesRemaining <= 0;
          return this.update(input);
        }
        const t = nowMs - this.fadeInAtMs;
        if (t >= FADEIN_MS) { this.phase = "alive"; return idle(); }
        return { cameraOverride: null, redAlpha: 0, dimAlpha: DIM_MAX * (1 - t / FADEIN_MS), countdown: null };
      }
    }
  }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npx vitest run tests/death.test.ts`
Expected: PASS (all tests green).

- [ ] **Step 5: Commit**

```bash
cd web && git add src/death.ts tests/death.test.ts
git commit -m "Add RespawnSequencer: death-fx state machine (camera hold + sting + fade)"
```

---

## Task 2: Renderer camera override + death-fx passes

**Files:**
- Modify: `web/src/render.ts` (`RenderState` ~81-88, `camera()` ~328-333, `draw()` ~366-374)
- Test: `web/tests/render.test.ts`

- [ ] **Step 1: Write the failing tests**

In `web/tests/render.test.ts`, first extend `recordingCtx` to capture fill passes. Replace the `RecordingCtx` interface and `recordingCtx` factory (currently around lines 41-58) with:

```ts
interface RecordingCtx extends RenderCtx {
  drawImageCalls: number;
  arcCalls: number;
  fills: string[]; // fillStyle captured at each fillRect call
}
function recordingCtx(w: number, h: number): RecordingCtx {
  const rec: RecordingCtx = {
    canvas: { width: w, height: h },
    fillStyle: "", strokeStyle: "", lineWidth: 1,
    clearRect: () => {}, fillRect: () => { rec.fills.push(String(rec.fillStyle)); },
    drawImage: () => { rec.drawImageCalls++; },
    beginPath: () => {}, arc: () => { rec.arcCalls++; }, moveTo: () => {}, lineTo: () => {},
    closePath: () => {}, fill: () => {}, stroke: () => {},
    save: () => {}, restore: () => {},
    drawImageCalls: 0, arcCalls: 0, fills: [],
  };
  return rec;
}
```

Then append these two tests to the file (before the final closing brace of the outer `describe`, or as new top-level `test(...)` calls — match the file's existing structure):

```ts
test("cameraOverride takes precedence over selfPredicted", () => {
  const ctx = fakeCtx(320, 240);
  const r = new Renderer(ctx, DEFAULT_PALETTE);
  r.setMap(openMaze(40, 30));
  const base: RenderState = {
    map: openMaze(40, 30), selfId: 1,
    selfPredicted: { x: 5000, y: 5000, facing: 0, flags: 0 },
    entities: [], renderTick: 0,
  };
  const camFollow = r.camera(base);
  const camHeld = r.camera({ ...base, cameraOverride: { x: 100, y: 100 } });
  expect(camHeld.x).not.toBe(camFollow.x);
});

test("deathFx draws red + dark fill passes", () => {
  const ctx = recordingCtx(320, 240);
  const r = new Renderer(ctx, DEFAULT_PALETTE);
  r.setMap(openMaze(40, 30));
  const state: RenderState = {
    map: openMaze(40, 30), selfId: 1, selfPredicted: null,
    entities: [], renderTick: 0,
    cameraOverride: { x: 100, y: 100 },
    deathFx: { redAlpha: 0.5, dimAlpha: 0.8 },
  };
  r.draw(state, emptyHudModel());
  expect(ctx.fills.some((f) => f.includes("220,30,30") || f.includes("220, 30, 30"))).toBe(true);
  expect(ctx.fills.some((f) => f.startsWith("rgba(0,0,0") || f.startsWith("rgba(0, 0, 0"))).toBe(true);
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run tests/render.test.ts`
Expected: FAIL — `cameraOverride`/`deathFx` are not on `RenderState` (type error) and/or the red/dark fills are absent.

- [ ] **Step 3: Implement the renderer changes**

In `web/src/render.ts`, extend `RenderState` (the interface near line 81). Add the two optional fields after `overlays?`:

```ts
export interface RenderState {
  map: MazeView | null;
  selfId: number;
  selfPredicted: SelfPredicted | null;
  entities: Entity[]; // interpolated non-self entities (subtile coords)
  renderTick: number;
  overlays?: ResolvedOverlay[]; // resolved muzzle/poof overlays to blit
  // cameraOverride pins the camera to a world position regardless of
  // selfPredicted (used during the death→respawn hold; the dead marine is
  // not drawn because selfPredicted is null). See web/src/death.ts.
  cameraOverride?: { x: number; y: number } | null;
  // deathFx draws a red sting (pre-CRT) and a dark fade (post-CRT) over the
  // whole canvas. Alphas come from the RespawnSequencer.
  deathFx?: { redAlpha: number; dimAlpha: number };
}
```

Update `camera()` (near line 328) to prefer the override:

```ts
  camera(s: RenderState): Camera {
    const vp = { w: this.ctx.canvas.width, h: this.ctx.canvas.height };
    const sx = s.cameraOverride?.x ?? s.selfPredicted?.x ?? 0;
    const sy = s.cameraOverride?.y ?? s.selfPredicted?.y ?? 0;
    return computeCamera(sx, sy, vp, s.map ?? { W: 1, H: 1, at: () => TileCode.Wall });
  }
```

In `draw()`, add the red sting just before the CRT pass and the dark fade just after it. The current tail of `draw()` is:

```ts
    // Combat overlays (muzzle / poof) blit on top of the entities.
    if (s.overlays) {
      for (const ov of s.overlays) this.drawOverlay(cam, ov);
    }

    // CRT scanline + vignette pass over the whole canvas (spec §5e).
    if (this.retroFx) {
      ctx.drawImage(this.crt.image, 0, 0, cam.w, cam.h, 0, 0, cam.w, cam.h);
    }
  }
```

Replace it with:

```ts
    // Combat overlays (muzzle / poof) blit on top of the entities.
    if (s.overlays) {
      for (const ov of s.overlays) this.drawOverlay(cam, ov);
    }

    // Death sting: red flush UNDER the CRT pass so scanlines tint it.
    if (s.deathFx && s.deathFx.redAlpha > 0) {
      ctx.fillStyle = `rgba(220,30,30,${s.deathFx.redAlpha})`;
      ctx.fillRect(0, 0, cam.w, cam.h);
    }

    // CRT scanline + vignette pass over the whole canvas (spec §5e).
    if (this.retroFx) {
      ctx.drawImage(this.crt.image, 0, 0, cam.w, cam.h, 0, 0, cam.w, cam.h);
    }

    // Respawn fade: dark veil OVER the CRT pass for a uniform cover.
    if (s.deathFx && s.deathFx.dimAlpha > 0) {
      ctx.fillStyle = `rgba(0,0,0,${s.deathFx.dimAlpha})`;
      ctx.fillRect(0, 0, cam.w, cam.h);
    }
  }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npx vitest run tests/render.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd web && git add src/render.ts tests/render.test.ts
git commit -m "Renderer: cameraOverride + deathFx (red sting / dark fade) passes"
```

---

## Task 3: HUD model — `respawnCountdown`

**Files:**
- Modify: `web/src/hud.ts` (`HudModel` ~27-37, `emptyHudModel` ~41-47)
- Test: `web/tests/hud.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/tests/hud.test.ts`:

```ts
import { emptyHudModel } from "../src/hud.js";

test("emptyHudModel has a null respawnCountdown", () => {
  expect(emptyHudModel().respawnCountdown).toBeNull();
});
```

(If `emptyHudModel` is already imported at the top of the file, do not add a duplicate import — just add the `test(...)`.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run tests/hud.test.ts`
Expected: FAIL — `respawnCountdown` is not on the model (`undefined`, not `null`).

- [ ] **Step 3: Implement**

In `web/src/hud.ts`, add the field to `HudModel` (after `endDialog`):

```ts
export interface HudModel {
  hp: number;
  lives: number;
  score: number;
  rows: ScoreRow[]; // ordered desc score, asc id
  nickById: Map<number, string>; // cached from the latest Scoreboard
  showScoreboard: boolean;
  chat: ChatLine[]; // ring buffer, newest last
  deadCam: boolean;
  endDialog: EndDialog | null;
  // respawnCountdown: integer seconds shown in the "RESPAWNING n" overlay
  // during the death→respawn fade, or null when not respawning. Set by
  // MatchRunner from the RespawnSequencer (web/src/death.ts).
  respawnCountdown: number | null;
}
```

And initialize it in `emptyHudModel`:

```ts
export function emptyHudModel(): HudModel {
  return {
    hp: 0, lives: 0, score: 0,
    rows: [], nickById: new Map(),
    showScoreboard: false, chat: [], deadCam: false, endDialog: null,
    respawnCountdown: null,
  };
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npx vitest run tests/hud.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd web && git add src/hud.ts tests/hud.test.ts
git commit -m "HUD: add respawnCountdown field"
```

---

## Task 4: Wire it into `browser.ts` (DOM overlay + MatchRunner)

**Files:**
- Modify: `web/src/browser.ts` — `UI` interface (~57-90), UI builder (~159-184), `injectMatchStyles` (~205-218), `renderHud` (~340-368), `MatchRunner` fields (~387-403), `drawFrame` (~583-610)

There is no separate vitest for `MatchRunner.drawFrame` (it drives the live DOM/canvas); this task is verified by the type-check/build plus the full suite, then a manual `/run`. Each step still shows exact code.

- [ ] **Step 1: Add the `respawnOverlay` DOM element to the UI**

In the `UI` interface (near line 82, alongside `stats`/`scoreboard`), add:

```ts
  respawnOverlay: HTMLElement;
```

In the UI builder, create the element next to the other match overlays (after the `endDialog` line ~166):

```ts
  const respawnOverlay = el("div", { "data-testid": "respawn-overlay", hidden: "true" });
```

Append it into `matchView` (line 168) — add `respawnOverlay` to the argument list:

```ts
  matchView.append(canvas, minimap, stats, scoreboard, chatBox, chatInput, endDialog, respawnOverlay);
```

Add it to the returned `ui` object literal (lines ~174-179) — include `respawnOverlay` among the fields:

```ts
    chatBox, chatInput, endDialog, backBtn, status, respawnOverlay,
```

- [ ] **Step 2: Add CSS for the overlay**

In `injectMatchStyles`, add one rule inside the template string (after the scoreboard/end-dialog rule, line ~217):

```css
#match [data-testid="respawn-overlay"] { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); color: #ff5a5a; font: 700 28px/1.2 monospace; letter-spacing: 2px; text-shadow: 0 0 8px #000, 0 0 12px #000; pointer-events: none; }
```

- [ ] **Step 3: Render the countdown in `renderHud`**

In `renderHud` (function near line 340), add this block just before the closing brace:

```ts
  if (hud.respawnCountdown != null) {
    ui.respawnOverlay.hidden = false;
    ui.respawnOverlay.textContent = `RESPAWNING ${hud.respawnCountdown}`;
  } else {
    ui.respawnOverlay.hidden = true;
  }
```

- [ ] **Step 4: Add `MatchRunner` fields**

Add the import near the other `./*.js` imports at the top of `browser.ts`:

```ts
import { RespawnSequencer } from "./death.js";
```

In the `MatchRunner` class (fields block ~390-403), add:

```ts
  private respawn = new RespawnSequencer();
  private lastSelfPos: { x: number; y: number } | null = null;
  private lastKnownSelfId = 0;
```

- [ ] **Step 5: Drive the sequencer in `drawFrame`**

Replace the body of `drawFrame` (currently lines ~583-610) with:

```ts
  private drawFrame(): void {
    const latest = this.latest;
    if (!latest || !this.maze) { renderHud(this.ui, this.hud); return; }
    const selfEntity = latest.entities.find((e) => e.id === latest.yourEntityID) ?? null;
    const self: SelfPredicted | null = selfEntity
      ? { x: selfEntity.x, y: selfEntity.y, facing: selfEntity.facing, flags: selfEntity.flags }
      : null;
    const others = latest.entities.filter((e) => e.id !== latest.yourEntityID);

    // Remember the live self id (stable across respawn) so we can read our
    // lives from the scoreboard while dead (yourEntityID is 0 then).
    if (latest.yourEntityID !== 0) this.lastKnownSelfId = latest.yourEntityID;
    if (self) this.lastSelfPos = { x: self.x, y: self.y };
    const selfRow = this.hud.rows.find((r) => r.id === this.lastKnownSelfId);
    const livesRemaining = selfRow ? selfRow.lives : 1;
    const fx = this.respawn.update({
      selfPresent: self !== null,
      selfPos: self ? { x: self.x, y: self.y } : null,
      livesRemaining,
      nowMs: performance.now(),
    });

    this.renderer.draw(
      {
        map: this.mazeViewCache, selfId: latest.yourEntityID, selfPredicted: self,
        entities: others, renderTick: this.renderTick,
        overlays: this.resolveOverlays(),
        cameraOverride: fx.cameraOverride,
        deathFx: { redAlpha: fx.redAlpha, dimAlpha: fx.dimAlpha },
      },
      this.hud,
    );
    if (self) drawMinimap(this.ui, this.maze, self, others);
    // Test-gated self-position hook so the live e2e (#27) can assert
    // server-authoritative movement. Prod never sets __ISNIPES_TEST__.
    if (self && testMode()) {
      (window as unknown as { __isnipesSelf?: { x: number; y: number } }).__isnipesSelf = { x: self.x, y: self.y };
    }
    // self HUD stats from the self entity + the scoreboard row.
    if (selfEntity) this.hud.hp = selfEntity.hp;
    const row = this.hud.rows.find((r) => r.id === latest.yourEntityID);
    if (row) { this.hud.lives = row.lives; this.hud.score = row.score; }
    this.hud.respawnCountdown = fx.countdown;
    renderHud(this.ui, this.hud);
  }
```

- [ ] **Step 6: Type-check / build**

Run: `cd web && npx tsc --noEmit`
Expected: no errors. (If the project lacks a `tsconfig`, run `npm run build` instead and expect a clean esbuild bundle.)

- [ ] **Step 7: Commit**

```bash
cd web && git add src/browser.ts
git commit -m "Wire death→respawn sequencer into MatchRunner + RESPAWNING overlay"
```

---

## Task 5: Full verification

- [ ] **Step 1: Run the whole TS suite**

Run: `cd web && npm test`
Expected: all tests PASS (including the existing render/hud/anim suites).

- [ ] **Step 2: Lint/typecheck if configured**

Run: `cd web && npm run lint` (skip if no such script) and `cd web && npx tsc --noEmit`.
Expected: clean.

- [ ] **Step 3: Manual verify (real app)**

Use the `/run` skill (or the project's local run instructions) to launch the server + two browser clients, join the same match, and have one client kill the other. Observe:
- On death: a red flash, camera stays at the death spot (no jump to the maze corner).
- ~1s later: screen fades dark with "RESPAWNING n" counting down.
- On respawn: the view fades back in already centered on the new spawn — no visible camera teleport.

Capture a screenshot/recording of the dead-window frame for the PR if convenient.

- [ ] **Step 4: Final commit (only if anything changed in Step 2/3 fixups)**

```bash
cd web && git add -A && git commit -m "Death→respawn camera: verification fixups"
```

(Skip if nothing changed.)

---

## Self-Review notes (already applied)

- **Spec coverage:** camera hold (Task 2 `cameraOverride`), red sting (Task 1 `redStingAlpha` + Task 2 pre-CRT fill), fade-to-dark + countdown (Task 1 `dimRampAlpha`/`respawnCountdown` + Task 3 + Task 4 overlay), eliminated path (Task 1), safety timeout + no-sighting guard (Task 1), `performance.now()` timing (Task 4), `lastKnownSelfId` lives lookup (Task 4). All covered.
- **Type consistency:** `DeathFxOutput` fields (`cameraOverride`, `redAlpha`, `dimAlpha`, `countdown`) match `RenderState.cameraOverride`/`deathFx` and `HudModel.respawnCountdown` usage in Task 4. `RespawnSequencer` (single name) used everywhere.
- **No placeholders:** every code/step is concrete.
- **Out of scope (per spec):** no server/sim/proto changes; minimap stays self-driven; existing death-poof re-anchoring left for later.
