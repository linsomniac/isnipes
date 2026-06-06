# Dead-Player Spectator Scroll — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an eliminated player (out of lives, match still running) pan the live map with the movement keys and `Tab`-follow living players, instead of being frozen at world origin.

**Architecture:** A new pure, clock-free `SpectatorCamera` state machine (`web/src/spectator.ts`) mirrors the existing `RespawnSequencer` (`web/src/death.ts`) pattern: no DOM, no canvas, no clock — the caller passes the frame delta and the current entity list, it returns a camera centre + mode. `death.ts` gets a one-field change (hold the death spot + expose a `spectating` flag). `browser.ts` (the DOM/render integration layer) wires the module into the per-frame `drawFrame`, re-enables the minimap, and shows a `SPECTATING` banner. The server already streams the full live world to dead players and ignores their input, so this is **100% client-side** — no `internal/**` change, no frozen file touched.

**Tech Stack:** TypeScript, HTML5 Canvas2D, Vite/esbuild bundle, vitest unit tests. Reuses frozen `web/src/sim.ts` (`Dir`, `velocityFor`, `PLAYER_TURBO_SPEED`) read-only.

**Spec:** `docs/superpowers/specs/2026-06-05-spectator-scroll-design.md`

---

## Key facts the engineer must know (verified against the tree)

- **Tests live in `web/tests/`, importing from `../src/X.js`** — NOT `web/src/*.test.ts` (the spec's path is wrong). Match `web/tests/death.test.ts`.
- Run tests from the `web/` directory: `cd web && npm test` (vitest). Bundle check: `cd web && npm run build` (esbuild — this is the only thing that catches an undefined import or a syntax error end-to-end; there is **no** `tsc` gate, and a bare `npx tsc --noEmit` reports pre-existing unrelated errors, so do not treat it as a gate).
- **Frozen files** are `web/src/{proto,sim,prediction,interp,netClient}.ts` (`scripts/frozen.sha256`). None of the files in this plan are frozen. We only *import read-only* from frozen `sim.ts`.
- `HudModel` is only ever constructed via `emptyHudModel()` (in `hud.ts`); `scenes.ts` builds scene huds by mutating `emptyHudModel()`. So adding fields there is safe and complete.
- `web/src/sim.ts` exports: `Dir` (object enum: `Idle:0,N:1,NE:2,E:3,SE:4,S:5,SW:6,W:7,NW:8`), `velocityFor(d, speed): [vx,vy]` (cardinal magnitude = `speed`; diagonal uses `diag()` ≈ `speed*0.707`, integer-truncated), `PLAYER_TURBO_SPEED = 224`. The sim runs at **30 ticks/sec**; `sim.ts` does NOT export a ticks-per-second constant, so define it locally in `spectator.ts`.
- `web/src/registry.ts` exports `EntityKind = { Unknown:0, Player:1, Generator:2, Projectile:3, Snipe:4 }`.
- In a dead-cam snapshot **every entity is alive** (server skips `FlagDead`), so "living players this frame" is simply `entities.filter(e => e.kind === EntityKind.Player)` — no flag check.
- `hud.ts` IS in the per-file coverage gate (`vitest.config.ts` `COVERED`); `death.ts` is NOT. Following the `death.ts` precedent, **do not add `spectator.ts` to the coverage gate** — keep `vitest.config.ts` untouched. The new fields added to `emptyHudModel()` are plain data (no branches), so the `hud.ts` coverage gate stays green.

---

## File structure

| File | Responsibility | Task |
|---|---|---|
| `web/src/spectator.ts` | **new** — pure `SpectatorCamera` state machine (free-pan + follow) | 1 |
| `web/tests/spectator.test.ts` | **new** — unit tests for the camera | 1 |
| `web/src/death.ts` | eliminated branch holds the death spot; add `spectating` to `DeathFxOutput` | 2 |
| `web/tests/death.test.ts` | update existing assertions for the new field/behaviour | 2 |
| `web/src/hud.ts` | add `spectating` / `spectatorFollow` to `HudModel` + `emptyHudModel` | 3 |
| `web/src/browser.ts` | DOM banner + scoped CSS, imports, `MatchRunner` fields, `Tab`→cycle, `drawFrame` wiring, banner render | 4 |
| (verification only) | `npm test`, `npm run build`, frozen check | 5 |

---

### Task 1: `SpectatorCamera` pure module + unit tests

**Files:**
- Create: `web/src/spectator.ts`
- Test: `web/tests/spectator.test.ts`

This is a self-contained pure module (depends only on frozen `sim.ts`), so it is built and tested in isolation first, mirroring how `death.ts` was done.

- [ ] **Step 1: Write the failing test file**

Create `web/tests/spectator.test.ts` with the complete content below.

```ts
// Spectator camera (web/src/spectator.ts). Pure unit tests: synthetic frame
// deltas + entity lists drive the camera so there is no clock/display
// dependence. Spec: docs/superpowers/specs/2026-06-05-spectator-scroll-design.md.

import { describe, expect, test } from "vitest";
import { Dir, velocityFor, PLAYER_TURBO_SPEED } from "../src/sim.js";
import {
  SpectatorCamera, PAN_SPEED, PAN_TURBO_MULT, MAX_DT_MS,
  type SpectatorInput,
} from "../src/spectator.js";

// A world big enough that pan tests never hit the clamp.
const BIG = { worldW: 1_000_000, worldH: 1_000_000 };

// in() builds a SpectatorInput with sensible defaults; override per test.
function inp(over: Partial<SpectatorInput> = {}): SpectatorInput {
  return {
    dtMs: 16, panDir: Dir.Idle, turbo: false, cycleEdge: false,
    players: [], bounds: BIG, ...over,
  };
}

describe("PAN_SPEED", () => {
  test("is player-turbo speed scaled to per-second (30Hz)", () => {
    expect(PAN_SPEED).toBe(PLAYER_TURBO_SPEED * 30); // 6720 subtiles/s
  });
});

describe("free-pan", () => {
  test("start() seeds free mode at the death spot", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 42, y: 84 });
    const out = cam.update(inp({ dtMs: 0 }));
    expect(out.mode).toBe("free");
    expect(out.center).toEqual({ x: 42, y: 84 });
    expect(out.followId).toBeNull();
  });

  test("integrates position by dt in the pan direction (East)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.E }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * 0.1); // 1672
    expect(out.center.y).toBeCloseTo(1000);
    expect(out.mode).toBe("free");
  });

  test("diagonals use velocityFor (normalized)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 5000, y: 5000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.NE }));
    const [vx, vy] = velocityFor(Dir.NE, PAN_SPEED);
    expect(out.center.x).toBeCloseTo(5000 + vx * 0.1);
    expect(out.center.y).toBeCloseTo(5000 + vy * 0.1);
  });

  test("turbo multiplies pan speed by PAN_TURBO_MULT", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.E, turbo: true }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * PAN_TURBO_MULT * 0.1);
  });

  test("clamps dt at MAX_DT_MS so a tab-out can't fling the camera", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100_000, panDir: Dir.E }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * (MAX_DT_MS / 1000));
  });

  test("clamps centre to [0, world] on the left/top edge", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 10, y: 10 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.NW, turbo: true }));
    expect(out.center.x).toBe(0);
    expect(out.center.y).toBe(0);
  });

  test("clamps centre to [0, world] on the right/bottom edge", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 90, y: 90 });
    const out = cam.update(inp({
      dtMs: 100, panDir: Dir.SE, turbo: true, bounds: { worldW: 100, worldH: 100 },
    }));
    expect(out.center.x).toBe(100);
    expect(out.center.y).toBe(100);
  });
});

describe("follow cycling", () => {
  const players = [
    { id: 5, x: 1, y: 1 },
    { id: 2, x: 2, y: 2 },
    { id: 9, x: 3, y: 3 },
  ];

  test("cycleEdge follows the next player id-ascending, wrapping", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    let out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.mode).toBe("follow");
    expect(out.followId).toBe(2);
    expect(out.center).toEqual({ x: 2, y: 2 });
    out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.followId).toBe(5);
    expect(out.center).toEqual({ x: 1, y: 1 });
    out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.followId).toBe(9);
    out = cam.update(inp({ cycleEdge: true, players })); // wrap
    expect(out.followId).toBe(2);
  });

  test("cycleEdge on an empty player list is a no-op (stays free)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 7, y: 7 });
    const out = cam.update(inp({ cycleEdge: true, players: [] }));
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 7, y: 7 });
  });
});

describe("follow tracking", () => {
  test("follow tracks a moving target across frames", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 100, y: 100 }] }));
    const out = cam.update(inp({ players: [{ id: 1, x: 150, y: 160 }] }));
    expect(out.mode).toBe("follow");
    expect(out.center).toEqual({ x: 150, y: 160 });
  });

  test("auto-advances when the followed target leaves the list", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 10, y: 10 }, { id: 3, x: 30, y: 30 }] }));
    const out = cam.update(inp({ players: [{ id: 3, x: 35, y: 35 }] })); // id 1 gone
    expect(out.mode).toBe("follow");
    expect(out.followId).toBe(3);
    expect(out.center).toEqual({ x: 35, y: 35 });
  });

  test("drops to free (holding centre) when no players remain", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 35, y: 35 }] }));
    const out = cam.update(inp({ players: [] })); // none left
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 35, y: 35 }); // last centre held
  });

  test("a non-idle panDir releases follow → free at the current centre", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 500, y: 500 }] }));
    // dtMs:0 so the released frame does not also pan — assert it released in place.
    const out = cam.update(inp({ dtMs: 0, panDir: Dir.E, players: [{ id: 1, x: 500, y: 500 }] }));
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 500, y: 500 });
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npm test -- spectator`
Expected: FAIL — `Cannot find module '../src/spectator.js'` (module not created yet).

- [ ] **Step 3: Write the implementation**

Create `web/src/spectator.ts` with this complete content.

```ts
// Spectator camera for an eliminated player (out of lives, match still
// running). Pure: no DOM, no canvas, no clock read — the caller passes the
// frame delta (dtMs) and the current living-player list each frame, so the
// curves are display-rate independent and unit-testable. Mirrors the
// RespawnSequencer pattern in web/src/death.ts.
//
// Spec: docs/superpowers/specs/2026-06-05-spectator-scroll-design.md.

import { Dir, velocityFor, PLAYER_TURBO_SPEED } from "./sim.js";

export type SpectatorMode = "free" | "follow";

export interface SpectatorPlayer {
  id: number;
  x: number;
  y: number;
}

export interface SpectatorInput {
  dtMs: number; // frame delta (performance.now() diff); clamped to MAX_DT_MS
  panDir: Dir; // movement-key Dir8 (Dir.Idle = no pan)
  turbo: boolean; // turbo key held → fast pan
  cycleEdge: boolean; // true only on the frame Tab was pressed
  players: SpectatorPlayer[]; // living players this frame (kind===Player)
  bounds: { worldW: number; worldH: number }; // subtile maze extents
}

export interface SpectatorOutput {
  center: { x: number; y: number }; // camera centre → cameraOverride
  mode: SpectatorMode;
  followId: number | null; // followed player id (follow mode), else null
}

// The sim runs at 30 ticks/sec; sim.ts speeds are per-tick subtiles, so a
// per-second pan speed is the per-tick turbo speed × the tick rate.
const TICKS_PER_SEC = 30;

// PAN_SPEED: a brisk free-look, ≈ player turbo speed (6720 subtiles/s).
export const PAN_SPEED = PLAYER_TURBO_SPEED * TICKS_PER_SEC;
// PAN_TURBO_MULT: holding turbo doubles the pan speed.
export const PAN_TURBO_MULT = 2;
// MAX_DT_MS: clamp the frame delta so a tab-out / GC pause can't fling the
// camera across the map in one frame.
export const MAX_DT_MS = 100;

// AIDEV-NOTE: SpectatorCamera is a pure state machine — no side effects, no
// clock. The caller (drawFrame) passes dtMs + the snapshot's living players;
// it returns a world centre the existing cameraOverride path consumes. The
// only mutable state is the centre, mode, and followId.
export class SpectatorCamera {
  private center = { x: 0, y: 0 };
  private mode: SpectatorMode = "free";
  private followId: number | null = null;

  // start seeds free-pan at the death spot (called once on the first
  // spectating frame).
  start(pos: { x: number; y: number }): void {
    this.center = { x: pos.x, y: pos.y };
    this.mode = "free";
    this.followId = null;
  }

  update(input: SpectatorInput): SpectatorOutput {
    const { dtMs, panDir, turbo, cycleEdge, players, bounds } = input;
    const dtSec = Math.min(dtMs, MAX_DT_MS) / 1000;

    // 1. Tab edge → advance to the next living player.
    if (cycleEdge) {
      const next = this.nextFollowId(players);
      if (next !== null) {
        this.followId = next;
        this.mode = "follow";
      }
      // none → stay in the current mode (no-op)
    }

    // 2. A movement key releases follow back to free-pan in place.
    if (panDir !== Dir.Idle && this.mode === "follow") {
      this.mode = "free";
      this.followId = null;
    }

    // 3. Apply the active mode.
    if (this.mode === "follow") {
      const target = players.find((p) => p.id === this.followId);
      if (target) {
        this.center = { x: target.x, y: target.y };
      } else {
        // Followed player left the list (eliminated, or culled by the
        // server's 64-entity dead-cam cap) → auto-advance, or drop to free.
        const next = this.nextFollowId(players);
        if (next === null) {
          this.mode = "free";
          this.followId = null;
          // hold the last centre
        } else {
          this.followId = next;
          const t = players.find((p) => p.id === next)!;
          this.center = { x: t.x, y: t.y };
        }
      }
    } else if (panDir !== Dir.Idle) {
      const speed = PAN_SPEED * (turbo ? PAN_TURBO_MULT : 1);
      const [vx, vy] = velocityFor(panDir, speed);
      this.center.x += vx * dtSec;
      this.center.y += vy * dtSec;
    }

    // 4. Clamp the centre to the world so free/follow stay coherent and the
    // centre never drifts off-map. The renderer's computeCamera does the
    // final viewport-edge clamp on top of this.
    this.center.x = clamp(this.center.x, 0, bounds.worldW);
    this.center.y = clamp(this.center.y, 0, bounds.worldH);

    return {
      center: { x: this.center.x, y: this.center.y },
      mode: this.mode,
      followId: this.mode === "follow" ? this.followId : null,
    };
  }

  // nextFollowId returns the living player id immediately after the current
  // followId in id-ascending order, wrapping to the first; the first id when
  // not yet following; or null when there are no players.
  private nextFollowId(players: SpectatorPlayer[]): number | null {
    if (players.length === 0) return null;
    const ids = players.map((p) => p.id).sort((a, b) => a - b);
    if (this.followId === null) return ids[0];
    for (const id of ids) if (id > this.followId) return id;
    return ids[0]; // wrap
  }
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(v, hi));
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && npm test -- spectator`
Expected: PASS — all `spectator.test.ts` cases green.

- [ ] **Step 5: Commit**

```bash
git add web/src/spectator.ts web/tests/spectator.test.ts
git commit -m "feat(spectator): pure SpectatorCamera state machine (free-pan + follow)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `death.ts` — hold the death spot + expose `spectating`

**Files:**
- Modify: `web/src/death.ts` (the `DeathFxOutput` type, `idle()`, the eliminated branch, and the two other return sites)
- Test: `web/tests/death.test.ts` (update 4 existing assertions; add 1 test)

The eliminated branch currently returns `cameraOverride: null` (the origin-jump bug). Change it to hold the death spot and flag `spectating: true`. Add `spectating: false` to every other `DeathFxOutput`. `this.deathPos` is already set before the eliminated branch runs (the "alive" case sets it on the alive→dead transition, then falls through), so it is safe to return.

- [ ] **Step 1: Update the existing tests to assert the new behaviour (write the failing tests first)**

In `web/tests/death.test.ts`, make these four edits.

Edit 1 — the "stays idle while alive" assertion (currently around line 49):

```ts
    const out = s.update(ALIVE(100, 200, 0));
    expect(out).toEqual({ cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null, spectating: false });
```

Edit 2 — replace the whole "eliminated (lives 0)" test (currently around lines 71–79) with:

```ts
  test("eliminated (lives 0): holds death spot, red sting, spectating, no dim/countdown", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0, 0));
    const out = s.update(DEAD(1, 0));
    expect(out.cameraOverride).toEqual({ x: 10, y: 20 });
    expect(out.spectating).toBe(true);
    expect(out.redAlpha).toBeGreaterThan(0);
    expect(out.dimAlpha).toBe(0);
    expect(out.countdown).toBeNull();
  });
```

Edit 3 — the "safety timeout clears a stuck dead state" assertion (currently around line 99):

```ts
    const out = s.update(DEAD(1 + SAFETY_MS + 1));
    expect(out).toEqual({ cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null, spectating: false });
```

Edit 4 — the "eliminated: self-reappearance resets to alive" assertion (currently around line 124):

```ts
    const out = s.update(ALIVE(10, 20, 2, 1));
    expect(out).toEqual({ cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null, spectating: false });
```

Then add this new test at the end of the `describe("RespawnSequencer", ...)` block (before its closing `});`):

```ts
  test("spectating is false in the normal (non-eliminated) respawn flow", () => {
    const s = new RespawnSequencer();
    s.update(ALIVE(10, 20, 0));
    const dead = s.update(DEAD(1)); // lives 3 → not eliminated
    expect(dead.spectating).toBe(false);
    const dim = s.update(DEAD(1 + HOLD_MS + 200));
    expect(dim.spectating).toBe(false);
    const fade = s.update(ALIVE(900, 900, 1 + RESPAWN_MS + 1)); // fade-in
    expect(fade.spectating).toBe(false);
  });
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npm test -- death`
Expected: FAIL — `spectating` is `undefined` (not yet on `DeathFxOutput`), and the eliminated test expects `cameraOverride` to equal `{x:10,y:20}` but gets `null`.

- [ ] **Step 3: Implement the `death.ts` changes**

Edit A — add `spectating` to the output interface. Change:

```ts
export interface DeathFxOutput {
  cameraOverride: { x: number; y: number } | null;
  redAlpha: number; // 0..1
  dimAlpha: number; // 0..1
  countdown: number | null; // integer seconds, or null when not shown
}
```

to:

```ts
export interface DeathFxOutput {
  cameraOverride: { x: number; y: number } | null;
  redAlpha: number; // 0..1
  dimAlpha: number; // 0..1
  countdown: number | null; // integer seconds, or null when not shown
  // spectating: true only on the eliminated-dead branch (out of lives, match
  // still running). browser.ts uses this to engage the SpectatorCamera and
  // cameraOverride (= death spot) to seed it. See web/src/spectator.ts.
  spectating: boolean;
}
```

Edit B — `idle()`. Change:

```ts
function idle(): DeathFxOutput {
  return { cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null };
}
```

to:

```ts
function idle(): DeathFxOutput {
  return { cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null, spectating: false };
}
```

Edit C — the eliminated branch. Change:

```ts
          return {
            cameraOverride: null,
            redAlpha: redStingAlpha(nowMs - this.deathAtMs),
            dimAlpha: 0,
            countdown: null,
          };
```

to:

```ts
          return {
            cameraOverride: { ...this.deathPos },
            redAlpha: redStingAlpha(nowMs - this.deathAtMs),
            dimAlpha: 0,
            countdown: null,
            spectating: true,
          };
```

Edit D — the non-eliminated dead branch (the `return` near the end of `case "dead"`). Change:

```ts
        return {
          cameraOverride: { ...this.deathPos },
          redAlpha: redStingAlpha(elapsed),
          dimAlpha: dimRampAlpha(elapsed),
          countdown: respawnCountdown(elapsed),
        };
```

to:

```ts
        return {
          cameraOverride: { ...this.deathPos },
          redAlpha: redStingAlpha(elapsed),
          dimAlpha: dimRampAlpha(elapsed),
          countdown: respawnCountdown(elapsed),
          spectating: false,
        };
```

Edit E — the fadein branch `return`. Change:

```ts
        return {
          cameraOverride: null,
          redAlpha: 0,
          dimAlpha: DIM_MAX * (1 - t / FADEIN_MS),
          countdown: null,
        };
```

to:

```ts
        return {
          cameraOverride: null,
          redAlpha: 0,
          dimAlpha: DIM_MAX * (1 - t / FADEIN_MS),
          countdown: null,
          spectating: false,
        };
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd web && npm test -- death`
Expected: PASS — all `death.test.ts` cases green.

- [ ] **Step 5: Commit**

```bash
git add web/src/death.ts web/tests/death.test.ts
git commit -m "feat(death): hold death spot + expose spectating on elimination

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: `hud.ts` — add spectator fields to the HUD model

**Files:**
- Modify: `web/src/hud.ts` (the `HudModel` interface + `emptyHudModel()`)

No new test file needed — `hud.ts` is in the coverage gate and these are plain data fields exercised by `emptyHudModel()` (called by existing tests and by `scenes.ts`). The existing `hud.test.ts` continues to pass.

- [ ] **Step 1: Add the fields to the interface**

In `web/src/hud.ts`, change the tail of the `HudModel` interface:

```ts
  // respawnCountdown: integer seconds shown in the "RESPAWNING n" overlay
  // during the death→respawn fade, or null when not respawning. Set by
  // MatchRunner from the RespawnSequencer (web/src/death.ts).
  respawnCountdown: number | null;
}
```

to:

```ts
  // respawnCountdown: integer seconds shown in the "RESPAWNING n" overlay
  // during the death→respawn fade, or null when not respawning. Set by
  // MatchRunner from the RespawnSequencer (web/src/death.ts).
  respawnCountdown: number | null;
  // spectating: true while eliminated and free-/follow-panning the live match
  // (spectator scroll). spectatorFollow is the followed player's nick in
  // follow mode, or null in free-pan. Set by MatchRunner from SpectatorCamera.
  spectating: boolean;
  spectatorFollow: string | null;
}
```

- [ ] **Step 2: Add the defaults to `emptyHudModel()`**

Change:

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

to:

```ts
export function emptyHudModel(): HudModel {
  return {
    hp: 0, lives: 0, score: 0,
    rows: [], nickById: new Map(),
    showScoreboard: false, chat: [], deadCam: false, endDialog: null,
    respawnCountdown: null,
    spectating: false, spectatorFollow: null,
  };
}
```

- [ ] **Step 3: Run the existing HUD tests to verify nothing broke**

Run: `cd web && npm test -- hud`
Expected: PASS — existing `hud.test.ts` still green (the new fields are additive).

- [ ] **Step 4: Commit**

```bash
git add web/src/hud.ts
git commit -m "feat(hud): add spectating / spectatorFollow to HudModel

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: `browser.ts` — wire the spectator camera, banner, and minimap

**Files:**
- Modify: `web/src/browser.ts` (imports, `UI` type, `buildDOM`, `injectMatchStyles`, `MatchRunner` fields, `keydown`, `drawFrame`, `renderHud`)

This is the integration layer (DOM + render). It is verified by `npm run build` (bundles cleanly) and the live e2e, not by a unit test. Make the edits in order, then build.

- [ ] **Step 1: Add the imports**

Change (currently line 20):

```ts
import { EntityRegistry } from "./registry.js";
```

to:

```ts
import { EntityRegistry, EntityKind } from "./registry.js";
```

And immediately after the `RespawnSequencer` import (currently line 30):

```ts
import { RespawnSequencer } from "./death.js";
```

add:

```ts
import { SpectatorCamera } from "./spectator.js";
```

- [ ] **Step 2: Add `spectatorBanner` to the `UI` interface**

In the `interface UI { ... }` block, after `respawnOverlay: HTMLElement;` add:

```ts
  spectatorBanner: HTMLElement;
```

- [ ] **Step 3: Create the banner element in `buildDOM` and append it**

In `buildDOM`, find (currently line 217):

```ts
  const respawnOverlay = el("div", { "data-testid": "respawn-overlay", hidden: "true" });
```

and add immediately after it:

```ts
  const spectatorBanner = el("div", { "data-testid": "spectator-banner", hidden: "true" });
```

Then change the `matchView.append(...)` line (currently line 219):

```ts
  matchView.append(canvas, minimap, stats, scoreboard, chatBox, chatInput, endDialog, respawnOverlay);
```

to:

```ts
  matchView.append(canvas, minimap, stats, scoreboard, chatBox, chatInput, endDialog, respawnOverlay, spectatorBanner);
```

Then add `spectatorBanner` to the `ui` object literal (currently lines 225–230). Change the trailing line:

```ts
    chatBox, chatInput, endDialog, backBtn, status, respawnOverlay, connStatus, lastMatch,
  };
```

to:

```ts
    chatBox, chatInput, endDialog, backBtn, status, respawnOverlay, connStatus, lastMatch,
    spectatorBanner,
  };
```

- [ ] **Step 4: Add the banner CSS in `injectMatchStyles`**

In `injectMatchStyles`, find the `respawn-overlay` rule (currently line 312) inside the template string:

```
#match [data-testid="respawn-overlay"] { position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); color: #ff5a5a; font: 700 28px/1.2 monospace; letter-spacing: 2px; text-shadow: 0 0 8px #000, 0 0 12px #000; pointer-events: none; }
```

and add this line immediately after it (still inside the backtick template):

```
#match [data-testid="spectator-banner"] { position: absolute; left: 50%; bottom: 64px; transform: translateX(-50%); color: #8af0ff; font: 700 16px/1.3 monospace; letter-spacing: 1.5px; text-shadow: 0 0 8px #0b1a2a, 0 0 12px #0b1a2a; pointer-events: none; white-space: nowrap; }
```

- [ ] **Step 5: Add the `MatchRunner` fields**

In `class MatchRunner`, find (currently lines 614–615):

```ts
  private respawn = new RespawnSequencer();
  private lastKnownSelfId = 0;
```

and add after them:

```ts
  private spectator = new SpectatorCamera();
  // spectatorSeeded latches the one-time SpectatorCamera.start() per death,
  // reset when spectating ends. lastFrameMs feeds the pan dt. cyclePressed is
  // the Tab edge captured by keydown, consumed once per frame.
  private spectatorSeeded = false;
  private lastFrameMs = 0;
  private cyclePressed = false;
```

- [ ] **Step 6: Add the `consumeCycle` helper**

Add this method to `MatchRunner` (e.g. immediately after the `setRetroFx` method, before `drawFrame`):

```ts
  // consumeCycle returns the pending Tab cycle edge and clears it, so each
  // press advances the followed player exactly once.
  private consumeCycle(): boolean {
    const c = this.cyclePressed;
    this.cyclePressed = false;
    return c;
  }
```

- [ ] **Step 7: Make `Tab` cycle while spectating (keydown)**

In the `keydown` handler, change (currently line 722):

```ts
    if (e.code === "Tab") { e.preventDefault(); this.hud.showScoreboard = true; return; }
```

to:

```ts
    if (e.code === "Tab") {
      e.preventDefault();
      // While spectating the scoreboard is always shown, so Tab cycles the
      // followed player instead of toggling it.
      if (this.hud.spectating) this.cyclePressed = true;
      else this.hud.showScoreboard = true;
      return;
    }
```

(Leave the `keyup` Tab handler unchanged: it sets `showScoreboard = false`, which `drawFrame` overrides back to `true` every frame while spectating.)

- [ ] **Step 8: Wire the spectator camera into `drawFrame`**

Replace the body of `drawFrame` from the `const fx = this.respawn.update({...});` block through the `if (self) drawMinimap(...)` line. Specifically, find this region (currently ~lines 811–828):

```ts
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
```

and replace it with:

```ts
    const fx = this.respawn.update({
      selfPresent: self !== null,
      selfPos: self ? { x: self.x, y: self.y } : null,
      livesRemaining,
      nowMs: performance.now(),
    });

    // Spectator camera: engaged only while eliminated and the match is still
    // running (fx.spectating). fx.cameraOverride holds the death spot, which
    // seeds the free-pan; from there the movement keys pan and Tab follows a
    // living player. When not spectating, the camera/minimap behave as before.
    let cameraOverride = fx.cameraOverride;
    let minimapSelf: SelfPredicted | null = self;
    if (fx.spectating) {
      const now = performance.now();
      if (!this.spectatorSeeded) {
        this.spectator.start(fx.cameraOverride ?? { x: 0, y: 0 });
        this.spectatorSeeded = true;
        this.lastFrameMs = now;
      }
      const dt = now - this.lastFrameMs;
      this.lastFrameMs = now;
      const intent = this.input.intent();
      const players = latest.entities
        .filter((e) => e.kind === EntityKind.Player)
        .map((e) => ({ id: e.id, x: e.x, y: e.y }));
      const out = this.spectator.update({
        dtMs: dt,
        panDir: intent.dir,
        turbo: intent.turbo,
        cycleEdge: this.consumeCycle(),
        players,
        bounds: { worldW: this.maze.W * 256, worldH: this.maze.H * 256 },
      });
      cameraOverride = out.center;
      minimapSelf = { x: out.center.x, y: out.center.y, facing: 0, flags: 0 };
      this.hud.spectating = true;
      this.hud.spectatorFollow =
        out.mode === "follow" && out.followId !== null
          ? (this.hud.nickById.get(out.followId) ?? "…")
          : null;
      this.hud.showScoreboard = true; // standings stay visible while watching
    } else {
      this.spectatorSeeded = false;
      this.hud.spectating = false;
      this.hud.spectatorFollow = null;
    }

    this.renderer.draw(
      {
        map: this.mazeViewCache, selfId: latest.yourEntityID, selfPredicted: self,
        entities: others, renderTick: this.renderTick,
        overlays: this.resolveOverlays(),
        cameraOverride,
        deathFx: { redAlpha: fx.redAlpha, dimAlpha: fx.dimAlpha },
      },
      this.hud,
    );
    if (minimapSelf) drawMinimap(this.ui, this.maze, minimapSelf, others);
```

(`others` while dead = every entity, since `yourEntityID` is 0 and no entity has id 0 — so the minimap shows all live entities as dots with the camera centre as the cyan reticle.)

- [ ] **Step 9: Render the banner in `renderHud`**

In `renderHud`, find the respawn-overlay block (currently lines 569–574):

```ts
  if (hud.respawnCountdown != null) {
    ui.respawnOverlay.hidden = false;
    ui.respawnOverlay.textContent = `RESPAWNING ${hud.respawnCountdown}`;
  } else {
    ui.respawnOverlay.hidden = true;
  }
}
```

and insert the banner block just before the closing `}` of the function:

```ts
  if (hud.respawnCountdown != null) {
    ui.respawnOverlay.hidden = false;
    ui.respawnOverlay.textContent = `RESPAWNING ${hud.respawnCountdown}`;
  } else {
    ui.respawnOverlay.hidden = true;
  }
  // Spectator banner: shown while spectating, hidden once the match ends
  // (the end dialog takes over).
  if (hud.spectating && !hud.endDialog) {
    ui.spectatorBanner.hidden = false;
    ui.spectatorBanner.textContent = hud.spectatorFollow
      ? `SPECTATING — Following ${hud.spectatorFollow} · move: free look`
      : "SPECTATING — move to look around · Tab: next player";
  } else {
    ui.spectatorBanner.hidden = true;
  }
}
```

- [ ] **Step 10: Build to verify the bundle is clean**

Run: `cd web && npm run build`
Expected: esbuild completes with no errors (writes `dist/app.js`). A typo'd import or undefined symbol fails here.

- [ ] **Step 11: Commit**

```bash
git add web/src/browser.ts
git commit -m "feat(spectator): wire SpectatorCamera + banner + minimap into the match loop

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full unit suite**

Run: `cd web && npm test`
Expected: PASS — all suites green, including the new `spectator.test.ts` and updated `death.test.ts`. No golden-frame changes (the spectator banner is hidden in every scene; `emptyHudModel().spectating === false`).

- [ ] **Step 2: Build the production bundle**

Run: `cd web && npm run build`
Expected: clean esbuild bundle.

- [ ] **Step 3: Confirm no frozen file was touched**

Run: `bash scripts/check-frozen.sh`
Expected: PASS — none of `spectator.ts` / `death.ts` / `hud.ts` / `browser.ts` are in `scripts/frozen.sha256`; the guard is unaffected. (If the script needs a working directory, run it from the repo root.)

- [ ] **Step 4 (optional / stretch): Live e2e sanity**

Per the spec, a Playwright test that drives a 2-player match to one player's elimination and asserts the `spectator-banner` appears and the camera responds to a movement key is a nice-to-have, not a gate (the unit tests carry the logic, and a real elimination is heavy to stage). If added, it lives under `web/tests/e2e/` and needs the gitignored `web/playwright.local.config.ts` chrome override to run locally. Skip unless requested.

- [ ] **Step 5: Manual smoke (when a human is available)**

In a real 2-player match, get one player eliminated (lose all lives) while the other plays on. Confirm: the view holds at the death spot with a brief red sting (not a jump to the top-left); the movement keys pan the map; turbo pans faster; `Tab` snaps to and follows the other player (banner reads "Following <nick>"); a movement key returns to free-pan; the minimap is visible; the scoreboard stays up; and on match end the banner/minimap give way to the end dialog.

---

## Self-review (run after implementing; checklist, not a dispatch)

- **Spec coverage:** free-pan ✓ (Task 1), turbo ✓, `Tab` follow + cycle/wrap/auto-advance/release ✓, dt clamp ✓, centre clamp ✓, `start()` seed ✓, death-spot hold + `spectating` ✓ (Task 2), HUD fields ✓ (Task 3), banner + minimap + `Tab`-cycle + scoreboard-auto-show wiring ✓ (Task 4), `!endDialog` gate ✓, frozen-file non-impact ✓ (Task 5). Reconnect-while-already-dead is a documented minor limitation (the sequencer needs a prior self sighting to flag `spectating`); not implemented, per spec "Minor".
- **Type consistency:** `SpectatorInput`/`SpectatorOutput`/`SpectatorMode` names match between `spectator.ts`, its test, and `browser.ts`. `SpectatorCamera.start()` / `.update()` signatures match all call sites. `DeathFxOutput.spectating` is read in `browser.ts` as `fx.spectating`. `HudModel.spectating` / `.spectatorFollow` match `renderHud` and `drawFrame`. `EntityKind.Player` is the imported registry constant.
- **No placeholders:** every step shows the exact code/command and expected result.
```
