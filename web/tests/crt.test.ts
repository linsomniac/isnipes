// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5d, §7 —
// CRT-overlay unit tests. crt.ts is NOT on the vitest COVERED list (canvas
// integration layer). These assert the pure contract: it builds once via a
// recording-fake factory, emits scanline fills only when scanlineAlpha > 0, and
// runs headless (createRadialGradient returns a stub). Node env, no document.

import { describe, expect, test } from "vitest";
import { buildCrtOverlay } from "../src/crt.js";
import type { AtlasCanvasFactory } from "../src/spriteAtlas.js";

interface RecCtx {
  fillRectCalls: number;
  radialGradients: number;
}
function recordingFactory(): { factory: AtlasCanvasFactory; rec: RecCtx } {
  const rec: RecCtx = { fillRectCalls: 0, radialGradients: 0 };
  const grad = { addColorStop(): void {} };
  const ctx = {
    fillStyle: "", strokeStyle: "", lineWidth: 1,
    clearRect() {},
    fillRect() { rec.fillRectCalls++; },
    createLinearGradient() { return grad; },
    createRadialGradient() { rec.radialGradients++; return grad; },
  } as unknown as CanvasRenderingContext2D;
  const factory: AtlasCanvasFactory = (_w, _h) => ({
    surface: ctx as unknown as CanvasImageSource,
    ctx,
  });
  return { factory, rec };
}

describe("buildCrtOverlay", () => {
  test("builds once and returns an image", () => {
    let calls = 0;
    const grad = { addColorStop(): void {} };
    const ctx = {
      fillStyle: "", clearRect() {}, fillRect() {},
      createRadialGradient() { return grad; },
    } as unknown as CanvasRenderingContext2D;
    const factory: AtlasCanvasFactory = () => {
      calls++;
      return { surface: ctx as unknown as CanvasImageSource, ctx };
    };
    const overlay = buildCrtOverlay(320, 240, 0.18, 0.55, factory);
    expect(calls).toBe(1);
    expect(overlay.image).toBeDefined();
  });

  test("scanlineAlpha 0 emits no scanline fills; the vignette still bakes", () => {
    const { factory, rec } = recordingFactory();
    buildCrtOverlay(300, 99, /*scanlineAlpha*/ 0, /*vignetteAlpha*/ 0.5, factory);
    // With no scanlines, the only fillRect is the single vignette gradient fill.
    expect(rec.fillRectCalls).toBe(1);
    expect(rec.radialGradients).toBe(1);
  });

  test("scanlineAlpha > 0 emits one fill per 3px row plus the vignette", () => {
    const { factory, rec } = recordingFactory();
    const h = 99;
    buildCrtOverlay(300, h, 0.2, 0.5, factory);
    const rows = Math.ceil(h / 3); // y = 0,3,6,...
    expect(rec.fillRectCalls).toBe(rows + 1); // scanlines + vignette
  });

  test("vignetteAlpha 0 skips the radial gradient", () => {
    const { factory, rec } = recordingFactory();
    buildCrtOverlay(120, 120, 0, 0, factory);
    expect(rec.radialGradients).toBe(0);
    expect(rec.fillRectCalls).toBe(0);
  });

  test("degenerate sizes are clamped (no throw)", () => {
    expect(() => buildCrtOverlay(0, 0, 0.18, 0.55, recordingFactory().factory)).not.toThrow();
  });
});
