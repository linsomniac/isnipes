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
