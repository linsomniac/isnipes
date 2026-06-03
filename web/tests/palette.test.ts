// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5a / §7 —
// the Direction-D enhanced-graphics palette fields. Verifies the new neon
// maze / self-ring / per-player-hue / CRT fields exist on every shipped
// palette and that high-contrast drops bloom + scanlines and thickens
// outlines. The original colorForKind-relevant fields are covered by
// render.test.ts; these tests own the additive enhanced-graphics surface.

import { describe, expect, test } from "vitest";
import {
  DEFAULT_PALETTE,
  COLORBLIND_PALETTE,
  PLAYER_HUE_COUNT,
  applyHighContrast,
  selectPalette,
  type Palette,
} from "../src/palette.js";

const HEX = /^#[0-9a-fA-F]{3,8}$/;

function expectEnhancedFields(p: Palette): void {
  expect(typeof p.wallStroke).toBe("string");
  expect(typeof p.wallGlow).toBe("string");
  expect(typeof p.floorDither).toBe("string");
  expect(typeof p.selfRing).toBe("string");
  expect(Array.isArray(p.playerHues)).toBe(true);
  expect(p.playerHues).toHaveLength(PLAYER_HUE_COUNT);
  for (const h of p.playerHues) expect(h).toMatch(HEX);
  expect(typeof p.bloom).toBe("boolean");
  expect(p.scanlineAlpha).toBeGreaterThanOrEqual(0);
  expect(p.scanlineAlpha).toBeLessThanOrEqual(1);
  expect(p.vignetteAlpha).toBeGreaterThanOrEqual(0);
  expect(p.vignetteAlpha).toBeLessThanOrEqual(1);
}

describe("enhanced-graphics palette fields", () => {
  test("DEFAULT_PALETTE carries all new Direction-D fields", () => {
    expectEnhancedFields(DEFAULT_PALETTE);
    // Self is hue 0 (cyan) per the spec; bloom + CRT on by default.
    expect(DEFAULT_PALETTE.playerHues[0]).toBe("#4fd1ff");
    expect(DEFAULT_PALETTE.bloom).toBe(true);
    expect(DEFAULT_PALETTE.scanlineAlpha).toBeGreaterThan(0);
    expect(DEFAULT_PALETTE.vignetteAlpha).toBeGreaterThan(0);
  });

  test("COLORBLIND_PALETTE carries all new fields and a distinct hue set", () => {
    expectEnhancedFields(COLORBLIND_PALETTE);
    // Colorblind-safe self hue differs from the default cyan.
    expect(COLORBLIND_PALETTE.playerHues[0]).not.toBe(DEFAULT_PALETTE.playerHues[0]);
    // Original colorForKind fields still intact (additive change).
    expect(COLORBLIND_PALETTE.snipe).toBe("#e69f00");
  });

  test("playerHues entries are all distinct (per-player identity)", () => {
    expect(new Set(DEFAULT_PALETTE.playerHues).size).toBe(PLAYER_HUE_COUNT);
    expect(new Set(COLORBLIND_PALETTE.playerHues).size).toBe(PLAYER_HUE_COUNT);
  });
});

describe("applyHighContrast (Direction-D behavior)", () => {
  test("on=true drops bloom + scanlines and thickens outline", () => {
    const hc = applyHighContrast(DEFAULT_PALETTE, true);
    expect(hc.bloom).toBe(false);
    expect(hc.outlineWidth).toBe(3);
    expect(hc.scanlineAlpha).toBe(0);
    // Strokes brighten; the vignette is preserved (does not obscure entities).
    expect(hc.wallStroke).not.toBe(DEFAULT_PALETTE.wallStroke);
    expect(hc.vignetteAlpha).toBe(DEFAULT_PALETTE.vignetteAlpha);
    // Pure: input is not mutated.
    expect(DEFAULT_PALETTE.bloom).toBe(true);
    expect(DEFAULT_PALETTE.outlineWidth).toBe(1);
  });

  test("on=false passes enhanced fields through and keeps thin outline", () => {
    const normal = applyHighContrast(DEFAULT_PALETTE, false);
    expect(normal.outlineWidth).toBe(1);
    expect(normal.bloom).toBe(DEFAULT_PALETTE.bloom);
    expect(normal.scanlineAlpha).toBe(DEFAULT_PALETTE.scanlineAlpha);
    expect(normal.playerHues).toEqual(DEFAULT_PALETTE.playerHues);
  });

  test("selectPalette composes base + high-contrast", () => {
    const cbHc = selectPalette(/*colorBlind*/ true, /*highContrast*/ true);
    expect(cbHc.name).toBe("colorBlind");
    expect(cbHc.bloom).toBe(false);
    expect(cbHc.scanlineAlpha).toBe(0);
    expect(cbHc.outlineWidth).toBe(3);

    const def = selectPalette(false, false);
    expect(def.name).toBe("default");
    expect(def.bloom).toBe(true);
  });
});
