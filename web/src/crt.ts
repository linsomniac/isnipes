// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5d, §6 —
// Direction-D CRT overlay. Pre-renders horizontal scanlines + a radial vignette
// into a w×h canvas ONCE; the per-frame render path blits it full-canvas when
// `retroFx` is on. Ported from the validated Direction-D preview's CRT pass.
//
// DETERMINISM (spec §4): pure function of (w, h, scanlineAlpha, vignetteAlpha) —
// no wall-clock. HEADLESS (spec §4a): uses the shared `makeAtlasCanvas` factory
// which degrades to a no-op recording stub (createRadialGradient returns a stub
// with a no-op addColorStop) so this is constructible under vitest.
//
// NOT on the vitest COVERED list (spec §4b): a canvas/integration layer.

import { makeAtlasCanvas, type AtlasCanvasFactory } from "./spriteAtlas.js";

export interface CrtOverlay {
  image: CanvasImageSource;
}

// buildCrtOverlay renders scanlines every 3px + a radial vignette. When
// scanlineAlpha is 0 no scanline fills are emitted; when vignetteAlpha is 0 the
// vignette is skipped — so high-contrast (scanlineAlpha 0) bakes a clean overlay.
export function buildCrtOverlay(
  w: number,
  h: number,
  scanlineAlpha: number,
  vignetteAlpha: number,
  makeCanvas: AtlasCanvasFactory = makeAtlasCanvas,
): CrtOverlay {
  const W = Math.max(1, w | 0);
  const H = Math.max(1, h | 0);
  const { surface, ctx } = makeCanvas(W, H);
  ctx.clearRect(0, 0, W, H);

  if (scanlineAlpha > 0) {
    ctx.fillStyle = `rgba(0,0,8,${scanlineAlpha})`;
    for (let y = 0; y < H; y += 3) ctx.fillRect(0, y, W, 1);
  }

  if (vignetteAlpha > 0) {
    const cx = W / 2;
    const cy = H / 2;
    const inner = Math.min(W, H) * 0.42;
    const outer = Math.hypot(W, H) * 0.6;
    const vg = ctx.createRadialGradient(cx, cy, inner, cx, cy, outer);
    vg.addColorStop(0, "rgba(0,0,0,0)");
    vg.addColorStop(1, `rgba(0,0,12,${vignetteAlpha})`);
    ctx.fillStyle = vg as unknown as string;
    ctx.fillRect(0, 0, W, H);
  }

  return { image: surface };
}
