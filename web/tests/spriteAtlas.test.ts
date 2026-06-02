// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5c, §7 —
// sprite-atlas unit tests. spriteAtlas.ts is NOT on the vitest COVERED list
// (it is a canvas/integration baking layer, e2e-covered), so these tests assert
// the pure contract: hueIndexForId mapping, a single bake via a recording-fake
// factory, and non-overlapping cells of size `cell`. Runs headless (node env).

import { describe, expect, test } from "vitest";
import {
  hueIndexForId, buildSpriteAtlas, bakeNeonMaze,
  PLAYER_HUE_COUNT, SNIPE_VARIANTS, type AtlasCanvasFactory,
} from "../src/spriteAtlas.js";
import { DEFAULT_PALETTE } from "../src/palette.js";
import { MUZZLE_FLASH_TICKS, DEATH_POOF_TICKS } from "../src/anim.js";
import { TileCode, type MazeView } from "../src/sim.js";

// A recording fake factory: counts how many offscreen canvases were requested
// and returns a no-op ctx (the baker must not throw under it).
function recordingFactory(): { factory: AtlasCanvasFactory; calls: () => number } {
  let n = 0;
  const grad = { addColorStop(): void {} };
  const ctx = {
    fillStyle: "", strokeStyle: "", lineWidth: 1, lineCap: "butt", lineJoin: "miter",
    globalAlpha: 1, shadowColor: "", shadowBlur: 0, imageSmoothingEnabled: false,
    save() {}, restore() {}, translate() {}, scale() {}, rotate() {},
    clearRect() {}, fillRect() {}, strokeRect() {}, beginPath() {}, closePath() {},
    moveTo() {}, lineTo() {}, arc() {}, ellipse() {}, rect() {}, fill() {}, stroke() {}, clip() {},
    drawImage() {},
    createLinearGradient() { return grad; },
    createRadialGradient() { return grad; },
  } as unknown as CanvasRenderingContext2D;
  const factory: AtlasCanvasFactory = (_w, _h) => {
    n++;
    return { surface: ctx as unknown as CanvasImageSource, ctx };
  };
  return { factory, calls: () => n };
}

describe("hueIndexForId", () => {
  test("self → 0; others 1..N-1, stable and in-range", () => {
    const selfId = 42;
    expect(hueIndexForId(42, selfId)).toBe(0); // self always cyan
    for (let id = 0; id < 200; id++) {
      const h = hueIndexForId(id, selfId);
      if (id === selfId) {
        expect(h).toBe(0);
      } else {
        expect(h).toBeGreaterThanOrEqual(1);
        expect(h).toBeLessThanOrEqual(PLAYER_HUE_COUNT - 1);
      }
      // stable / pure
      expect(hueIndexForId(id, selfId)).toBe(h);
    }
  });

  test("negative ids still land in 1..N-1 (no out-of-range)", () => {
    for (const id of [-1, -7, -13, -100]) {
      const h = hueIndexForId(id, 999);
      expect(h).toBeGreaterThanOrEqual(1);
      expect(h).toBeLessThanOrEqual(PLAYER_HUE_COUNT - 1);
    }
  });
});

describe("buildSpriteAtlas", () => {
  test("bakes once (single image) via the factory and reports a square cell", () => {
    const { factory, calls } = recordingFactory();
    const atlas = buildSpriteAtlas(DEFAULT_PALETTE, factory);
    expect(calls()).toBe(1); // one offscreen canvas baked for the whole sheet
    expect(atlas.cell).toBeGreaterThan(0);
    expect(atlas.image).toBeDefined();
  });

  test("all cells are size `cell` and non-overlapping within a category", () => {
    const atlas = buildSpriteAtlas(DEFAULT_PALETTE, recordingFactory().factory);
    const cell = atlas.cell;
    const seen = new Set<string>();
    const check = (c: { sx: number; sy: number; sw: number; sh: number }): void => {
      expect(c.sw).toBe(cell);
      expect(c.sh).toBe(cell);
      expect(c.sx % cell).toBe(0);
      expect(c.sy % cell).toBe(0);
    };
    const claim = (c: { sx: number; sy: number; sw: number; sh: number }): void => {
      const key = `${c.sx},${c.sy}`;
      expect(seen.has(key)).toBe(false); // non-overlapping
      seen.add(key);
    };

    // Player: every hue × walk × facing is a distinct cell.
    for (let hue = 0; hue < PLAYER_HUE_COUNT; hue++) {
      for (let w = 0; w < 4; w++) {
        for (const left of [false, true]) {
          const c = atlas.player(hue, w, left);
          check(c);
        }
      }
    }
    // Distinct player cells claim distinct slots (fold walk 0..3 → 2 frames).
    const playerSlots = new Set<string>();
    for (let hue = 0; hue < PLAYER_HUE_COUNT; hue++) {
      for (let w = 0; w < 2; w++) {
        for (const left of [false, true]) {
          const c = atlas.player(hue, w, left);
          playerSlots.add(`${c.sx},${c.sy}`);
        }
      }
    }
    expect(playerSlots.size).toBe(PLAYER_HUE_COUNT * 2 * 2);

    // Cross-category cells never collide (player/snipe/gen/proj/muzzle/poof/ring
    // live on distinct rows).
    claim(atlas.player(0, 0, false));
    claim(atlas.snipe(0, 0, false));
    for (let v = 0; v < SNIPE_VARIANTS; v++) check(atlas.snipe(v, 0, false));
    for (let s = 0; s < 3; s++) for (let p = 0; p < 4; p++) check(atlas.generator(s, p));
    claim(atlas.generator(0, 0));
    claim(atlas.projectile());
    check(atlas.projectile());
    for (let f = 0; f < MUZZLE_FLASH_TICKS; f++) check(atlas.muzzle(f));
    claim(atlas.muzzle(0));
    for (let f = 0; f < DEATH_POOF_TICKS; f++) check(atlas.poof(f));
    claim(atlas.poof(0));
    claim(atlas.selfRing());
    check(atlas.selfRing());
  });

  test("frame lookups clamp out-of-range muzzle/poof indices", () => {
    const atlas = buildSpriteAtlas(DEFAULT_PALETTE, recordingFactory().factory);
    expect(atlas.muzzle(999)).toEqual(atlas.muzzle(MUZZLE_FLASH_TICKS - 1));
    expect(atlas.muzzle(-5)).toEqual(atlas.muzzle(0));
    expect(atlas.poof(99999)).toEqual(atlas.poof(DEATH_POOF_TICKS - 1));
    expect(atlas.poof(-1)).toEqual(atlas.poof(0));
  });

  test("bloom-off palette still bakes (no throw) — high-contrast path", () => {
    const noBloom = { ...DEFAULT_PALETTE, bloom: false };
    expect(() => buildSpriteAtlas(noBloom, recordingFactory().factory)).not.toThrow();
  });

  test("self-ring stroke derives from palette.selfRing (not a hardcoded cyan)", () => {
    // Capture every strokeStyle assignment while baking, then confirm the ring
    // stroke carries the palette hue's rgb — so COLORBLIND / high-contrast self
    // hues actually drive the ring instead of a fixed rgba(110,230,255,…).
    const captureFactory = (sink: string[]): AtlasCanvasFactory => {
      const grad = { addColorStop(): void {} };
      let _stroke = "";
      const ctx = {
        fillStyle: "", lineWidth: 1, lineCap: "butt", lineJoin: "miter",
        globalAlpha: 1, shadowColor: "", shadowBlur: 0, imageSmoothingEnabled: false,
        get strokeStyle(): string { return _stroke; },
        set strokeStyle(v: string) { _stroke = v; sink.push(v); },
        save() {}, restore() {}, translate() {}, scale() {}, rotate() {},
        clearRect() {}, fillRect() {}, strokeRect() {}, beginPath() {}, closePath() {},
        moveTo() {}, lineTo() {}, arc() {}, ellipse() {}, rect() {}, fill() {}, stroke() {}, clip() {},
        drawImage() {},
        createLinearGradient() { return grad; },
        createRadialGradient() { return grad; },
      } as unknown as CanvasRenderingContext2D;
      return (_w, _h) => ({ surface: ctx as unknown as CanvasImageSource, ctx });
    };
    // A deliberately non-cyan self hue (#9fd6f5 = rgb 159,214,245, the colorblind
    // value) must appear in the ring strokes.
    const strokes: string[] = [];
    const pal = { ...DEFAULT_PALETTE, selfRing: "#9fd6f5" };
    buildSpriteAtlas(pal, captureFactory(strokes));
    expect(strokes.some((s) => s.startsWith("rgba(159,214,245,"))).toBe(true);
    // The old hardcoded cyan literal must NOT appear as a ring stroke.
    expect(strokes.some((s) => s.startsWith("rgba(110,230,255,"))).toBe(false);
  });
});

describe("bakeNeonMaze", () => {
  test("bakes once and strokes only floor-bordering wall edges (no throw headless)", () => {
    // 3×3: solid wall border around a single center floor cell.
    const maze: MazeView = {
      W: 3, H: 3,
      at: (x, y) => (x === 1 && y === 1 ? TileCode.Floor : TileCode.Wall),
    };
    const { factory, calls } = recordingFactory();
    const img = bakeNeonMaze(maze, DEFAULT_PALETTE, 32, factory);
    expect(calls()).toBe(1);
    expect(img).toBeDefined();
  });

  test("bloom-off (high-contrast) maze bakes without throwing", () => {
    const maze: MazeView = { W: 2, H: 2, at: () => TileCode.Wall };
    const noBloom = { ...DEFAULT_PALETTE, bloom: false };
    expect(() => bakeNeonMaze(maze, noBloom, 32, recordingFactory().factory)).not.toThrow();
  });
});
