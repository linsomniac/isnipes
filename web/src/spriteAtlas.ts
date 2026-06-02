// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5c, §6 —
// Direction-D baked sprite atlas. Every entity / frame / hue / damage cell is
// rendered ONCE into an offscreen canvas using the full Canvas API (gradients,
// shadowBlur for the bloom glow); the per-frame render path then only blits
// `drawImage(image, cell → world dest)`. The pixel-art draw routines (marine,
// bug/snipe, hive/generator, self-ring, projectile, muzzle, poof) are ported
// verbatim in spirit from the validated Direction-D preview
// (.superpowers/brainstorm/.../target-preview.html) — do not reinvent the look.
//
// DETERMINISM (spec §4): nothing here reads wall-clock — bakes are a pure
// function of (palette). Cell lookups are pure arithmetic over a fixed grid.
//
// HEADLESS (spec §4a): vitest runs in the node env (no document / no
// OffscreenCanvas). `makeAtlasCanvas` degrades to a no-op recording-ctx stub so
// `buildSpriteAtlas` runs harmlessly and returns a stub `image` + real cell
// math. This module is the shared canvas-factory home for render.ts + crt.ts.
//
// This file is NOT on the vitest per-file coverage COVERED list (spec §4b): it
// is a canvas/integration baking layer (e2e-covered), like browser.ts/scenes.ts.

import type { Palette } from "./palette.js";
import { MUZZLE_FLASH_TICKS, DEATH_POOF_TICKS } from "./anim.js";
import { type MazeView, TileCode } from "./sim.js";

// AtlasCanvasFactory builds an offscreen draw target. Shared by render.ts
// (maze cache) and crt.ts (CRT overlay) so all three baked surfaces use one
// headless-safe factory.
export type AtlasCanvasFactory = (
  w: number,
  h: number,
) => { surface: CanvasImageSource; ctx: CanvasRenderingContext2D };

export interface Cell {
  sx: number;
  sy: number;
  sw: number;
  sh: number;
}

export interface SpriteAtlas {
  image: CanvasImageSource;
  cell: number; // square cell px
  player(hueIndex: number, walk: number, left: boolean): Cell;
  snipe(variant: number, walk: number, left: boolean): Cell;
  generator(stage: number, pulse: number): Cell;
  projectile(): Cell;
  muzzle(frame: number): Cell;
  poof(frame: number): Cell;
  selfRing(): Cell;
}

export const PLAYER_HUE_COUNT = 7;
export const SNIPE_VARIANTS = 2;

// CELL: square atlas cell in px. Sprites are drawn centered (origin at the cell
// center). s≈3 pixel unit with the §6 routines fits comfortably in 48px.
const CELL = 48;
const S = 3; // pixel unit (sprite-local px → atlas px)
const HALF = CELL / 2;

// Walk is reduced to a 2-frame cycle (spec §2: "2-frame walk cycle"); the
// renderer passes walkFrame()'s 0..3, we fold to the low bit.
const WALK_FRAMES = 2;
const GEN_STAGES = 3;
const GEN_PULSES = 4;

// Grid layout: one ROW per category so non-overlap is trivially provable.
// Columns are the per-category frame/variant/hue/damage permutations.
const ROW = {
  player: 0, // PLAYER_HUE_COUNT * WALK_FRAMES * 2(left) = 28 cols
  snipe: 1, // SNIPE_VARIANTS * WALK_FRAMES * 2 = 8 cols
  generator: 2, // GEN_STAGES * GEN_PULSES = 12 cols
  projectile: 3, // 1 col
  muzzle: 4, // MUZZLE_FLASH_TICKS cols
  poof: 5, // DEATH_POOF_TICKS cols
  selfRing: 6, // 1 col
} as const;

const ROWS = 7;
const PLAYER_COLS = PLAYER_HUE_COUNT * WALK_FRAMES * 2;
const SNIPE_COLS = SNIPE_VARIANTS * WALK_FRAMES * 2;
const GEN_COLS = GEN_STAGES * GEN_PULSES;
const COLS = Math.max(
  PLAYER_COLS,
  SNIPE_COLS,
  GEN_COLS,
  MUZZLE_FLASH_TICKS,
  DEATH_POOF_TICKS,
  1,
);

function cellAt(row: number, col: number): Cell {
  return { sx: col * CELL, sy: row * CELL, sw: CELL, sh: CELL };
}

// hueIndexForId: self → 0 (cyan); everyone else gets a stable hue in 1..N-1 by
// id (spec §5c). Pure arithmetic so a given id always maps to the same suit.
export function hueIndexForId(id: number, selfId: number): number {
  if (id === selfId) return 0;
  return 1 + (((id % (PLAYER_HUE_COUNT - 1)) + (PLAYER_HUE_COUNT - 1)) % (PLAYER_HUE_COUNT - 1));
}

// ---- shared headless-safe canvas factory ----

// recordingStub: a no-op CanvasRenderingContext2D used when neither
// OffscreenCanvas nor document is available (vitest node env, spec §4a). All
// draw methods no-op, properties are settable, gradients return a stub with a
// no-op addColorStop. Built once and reused.
function makeRecordingStub(): { surface: CanvasImageSource; ctx: CanvasRenderingContext2D } {
  const grad = { addColorStop(): void {} };
  const ctx = {
    canvas: { width: 0, height: 0 },
    fillStyle: "",
    strokeStyle: "",
    lineWidth: 1,
    lineCap: "butt",
    lineJoin: "miter",
    globalAlpha: 1,
    shadowColor: "",
    shadowBlur: 0,
    imageSmoothingEnabled: false,
    save(): void {},
    restore(): void {},
    translate(): void {},
    scale(): void {},
    rotate(): void {},
    clearRect(): void {},
    fillRect(): void {},
    strokeRect(): void {},
    beginPath(): void {},
    closePath(): void {},
    moveTo(): void {},
    lineTo(): void {},
    arc(): void {},
    ellipse(): void {},
    rect(): void {},
    fill(): void {},
    stroke(): void {},
    clip(): void {},
    drawImage(): void {},
    createLinearGradient(): { addColorStop(): void } {
      return grad;
    },
    createRadialGradient(): { addColorStop(): void } {
      return grad;
    },
  } as unknown as CanvasRenderingContext2D;
  // The stub also serves as the (never-blitted) image source.
  return { surface: ctx as unknown as CanvasImageSource, ctx };
}

// makeAtlasCanvas: the shared factory. Prefers OffscreenCanvas, falls back to a
// detached <canvas>, and finally degrades to the headless recording stub so
// every bake (atlas / maze / CRT) is constructible under vitest (spec §4a).
export const makeAtlasCanvas: AtlasCanvasFactory = (w, h) => {
  if (typeof OffscreenCanvas !== "undefined") {
    const oc = new OffscreenCanvas(Math.max(1, w), Math.max(1, h));
    const ctx = oc.getContext("2d") as unknown as CanvasRenderingContext2D | null;
    if (ctx) {
      ctx.imageSmoothingEnabled = false;
      return { surface: oc as unknown as CanvasImageSource, ctx };
    }
  }
  if (typeof document !== "undefined") {
    const c = document.createElement("canvas");
    c.width = Math.max(1, w);
    c.height = Math.max(1, h);
    const ctx = c.getContext("2d");
    if (ctx) {
      ctx.imageSmoothingEnabled = false;
      return { surface: c, ctx };
    }
  }
  return makeRecordingStub();
};

// ---- pixel-art draw routines (ported from the validated Direction-D preview) ----
// P(x,y,w,h,c) fills a rect at sprite-LOCAL pixel coords (origin = cell center).
// The atlas is baked un-mirrored ("facing right"); the renderer mirrors L/R by
// swapping the cell column (left bake) — we bake both so the hot path stays
// drawImage-only (no per-frame ctx.scale, which the restricted RenderCtx lacks).

type Ctx = CanvasRenderingContext2D;

// drawWith centers on the cell, optionally mirrors, and exposes a sprite-local
// P() rect painter. Mirroring negates the x-axis so the same routine bakes both
// facings.
function drawWith(
  ctx: Ctx,
  cx: number,
  cy: number,
  left: boolean,
  fn: (P: (x: number, y: number, w: number, h: number, c: string) => void) => void,
): void {
  ctx.save();
  ctx.translate(cx, cy);
  if (left) ctx.scale(-1, 1);
  const P = (x: number, y: number, w: number, h: number, c: string): void => {
    ctx.fillStyle = c;
    // When mirrored, the rect's left edge moves to -(x+w); shift origin so the
    // same local coords paint the mirror image.
    const lx = left ? -(x + w) : x;
    ctx.fillRect(Math.round(lx * S), Math.round(y * S), Math.ceil(w * S), Math.ceil(h * S));
  };
  fn(P);
  ctx.restore();
}

// shade derives a darker tone from a #rrggbb hue for arms/legs (suitLo).
function shade(hex: string, k: number): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return hex;
  const n = parseInt(m[1], 16);
  const r = Math.max(0, Math.min(255, Math.round(((n >> 16) & 0xff) * k)));
  const g = Math.max(0, Math.min(255, Math.round(((n >> 8) & 0xff) * k)));
  const b = Math.max(0, Math.min(255, Math.round((n & 0xff) * k)));
  return `#${((1 << 24) | (r << 16) | (g << 8) | b).toString(16).slice(1)}`;
}

// rgba converts a #rrggbb hue + alpha to an `rgba(r,g,b,a)` string. Lets baked
// strokes derive their color from a palette hue (e.g. the self-ring color)
// rather than hardcoding a literal. Falls back to the raw value if unparsable.
function rgba(hex: string, alpha: number): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return hex;
  const n = parseInt(m[1], 16);
  return `rgba(${(n >> 16) & 0xff},${(n >> 8) & 0xff},${n & 0xff},${alpha})`;
}

// Marine (player / other players) — helmet + visor + suit torso + arms + 2-frame
// boot walk. Hue = playerHues[hueIndex]; arms/legs a darker derived tone.
function bakeMarine(ctx: Ctx, cx: number, cy: number, frame: number, left: boolean, suit: string): void {
  const suitLo = shade(suit, 0.62);
  const visor = shade(suit, 0.18);
  const glint = "#eef9ff";
  const helmet = "#cfd6e0";
  const helmetLo = "#8a94a6";
  const boot = "#272c36";
  drawWith(ctx, cx, cy, left, (P) => {
    P(-3, -7, 6, 4, helmet);
    P(-3, -7, 6, 1, "#eef3f9");
    P(-2, -6, 4, 2, visor);
    P(-2, -6, 1, 1, glint);
    P(-3, -3, 6, 5, suit);
    P(-3, -3, 6, 1, helmetLo);
    P(-1, -1, 2, 2, glint);
    P(-4, -3, 1, 4, suitLo);
    P(3, -3, 1, 4, suitLo);
    if (frame === 0) {
      P(-3, 2, 2, 3, boot);
      P(1, 2, 2, 2, boot);
    } else {
      P(-3, 2, 2, 2, boot);
      P(1, 2, 2, 3, boot);
    }
  });
}

// Snipe (pixel bug) — dome body, glowing eyes (shadowBlur if bloom), antennae,
// mandibles, skittering legs alternating by walk frame. 2 hue variants.
function bakeBug(
  ctx: Ctx,
  cx: number,
  cy: number,
  frame: number,
  left: boolean,
  body: string,
  bodyLo: string,
  eye: string,
  bloom: boolean,
): void {
  drawWith(ctx, cx, cy, left, (P) => {
    // antennae + tip eyes
    P(-2, -5, 1, 2, bodyLo);
    P(-2, -5, 1, 1, eye);
    P(1, -5, 1, 2, bodyLo);
    P(1, -5, 1, 1, eye);
    // skittering legs (swap by frame)
    const a = frame ? 0 : 1;
    const b = frame ? 1 : 0;
    P(-5, a, 1, 2, bodyLo);
    P(-5, 2, 1, 2, bodyLo);
    P(4, b, 1, 2, bodyLo);
    P(4, 2, 1, 2, bodyLo);
    // dome
    P(-3, -3, 6, 1, body);
    P(-4, -2, 8, 4, body);
    P(-4, 2, 8, 1, bodyLo);
    // mandibles
    P(-4, 1, 1, 1, bodyLo);
    P(3, 1, 1, 1, bodyLo);
  });
  // glowing eyes (separate save so the glow does not bleed into the body)
  ctx.save();
  ctx.translate(cx, cy);
  if (left) ctx.scale(-1, 1);
  if (bloom) {
    ctx.shadowColor = eye;
    ctx.shadowBlur = 8;
  }
  const PE = (x: number, y: number, w: number, h: number, c: string): void => {
    ctx.fillStyle = c;
    const lx = left ? -(x + w) : x;
    ctx.fillRect(Math.round(lx * S), Math.round(y * S), Math.ceil(w * S), Math.ceil(h * S));
  };
  PE(-3, -1, 2, 2, eye);
  PE(1, -1, 2, 2, eye);
  ctx.shadowBlur = 0;
  PE(-3, -1, 1, 1, "#fff");
  PE(1, -1, 1, 1, "#fff");
  ctx.restore();
}

// Generator (alien hive) — base slab, dome + top highlight + seam shading, a
// glowing core (brighter at higher pulse), damage cracks at stage ≥1.
function bakeHive(ctx: Ctx, cx: number, cy: number, stage: number, pulse: number, glow: string, bloom: boolean): void {
  const shell = "#7d5fa6";
  const shellLo = "#553f73";
  const shellHi = "#a98fce";
  const base = "#33263f";
  const p = pulse / (GEN_PULSES - 1); // 0..1
  const P = (x: number, y: number, w: number, h: number, c: string): void => {
    ctx.fillStyle = c;
    ctx.fillRect(Math.round(cx + x * S), Math.round(cy + y * S), Math.ceil(w * S), Math.ceil(h * S));
  };
  P(-5, 2, 10, 3, base);
  P(-4, -4, 8, 6, shell);
  P(-4, -4, 8, 1, shellHi);
  P(-4, -2, 1, 4, shellLo);
  P(3, -2, 1, 4, shellLo);
  P(-1, -4, 1, 6, shellLo);
  // glowing core — brighter / hotter with pulse; dimmed at critical damage.
  ctx.save();
  if (bloom) {
    ctx.shadowColor = glow;
    ctx.shadowBlur = 4 + p * 14;
  }
  const dim = stage >= 2 ? 0.5 : 1;
  const gG = Math.round((120 + p * 100) * dim);
  ctx.fillStyle = `rgba(255,${gG},230,1)`;
  ctx.fillRect(Math.round(cx + -2 * S), Math.round(cy + -1 * S), Math.ceil(4 * S), Math.ceil(3 * S));
  ctx.restore();
  // damage cracks (yellow glowing polyline)
  if (stage >= 1) {
    ctx.save();
    ctx.strokeStyle = "#ffec70";
    ctx.lineWidth = 1.5;
    if (bloom) {
      ctx.shadowColor = "#ffec70";
      ctx.shadowBlur = 6;
    }
    ctx.beginPath();
    ctx.moveTo(cx + -1 * S, cy + -4 * S);
    ctx.lineTo(cx + 1.5 * S, cy + -1 * S);
    ctx.lineTo(cx + -0.5 * S, cy + 1 * S);
    ctx.stroke();
    if (stage >= 2) {
      // second crack for the critical stage
      ctx.beginPath();
      ctx.moveTo(cx + 3 * S, cy + -3 * S);
      ctx.lineTo(cx + 1 * S, cy + 0 * S);
      ctx.lineTo(cx + 3 * S, cy + 2 * S);
      ctx.stroke();
    }
    ctx.shadowBlur = 0;
    ctx.restore();
  }
}

// Self ground-ring — flattened glowing ellipse baked once, blitted under the
// local player. (scale(1,0.42) + shadowBlur per the preview.) The ring stroke
// is derived from `color` (palette.selfRing) so the per-palette self hue
// actually drives it: DEFAULT #6ee6ff stays the validated cyan, while the
// COLORBLIND (#9fd6f5) and high-contrast (#e6f6ff) self hues now take effect.
// AIDEV-NOTE: bloom:false (high-contrast) skips shadowColor, so the visible
// stroke must carry the palette hue itself — hence the rgba(color,…) strokes
// rather than the old hardcoded cyan literals.
function bakeSelfRing(ctx: Ctx, cx: number, cy: number, color: string, bloom: boolean): void {
  ctx.save();
  ctx.translate(cx, cy + 5 * S);
  ctx.scale(1, 0.42);
  ctx.strokeStyle = rgba(color, 0.9);
  ctx.lineWidth = 2.5;
  if (bloom) {
    ctx.shadowColor = color;
    ctx.shadowBlur = 12;
  }
  ctx.beginPath();
  ctx.arc(0, 0, 8 * S, 0, Math.PI * 2);
  ctx.stroke();
  ctx.strokeStyle = rgba(color, 0.35);
  ctx.lineWidth = 1.5;
  ctx.shadowBlur = 0;
  ctx.beginPath();
  ctx.arc(0, 0, 5 * S, 0, Math.PI * 2);
  ctx.stroke();
  ctx.restore();
}

// Projectile — small chunky bright bolt (#ffe27a core + white center).
function bakeProjectile(ctx: Ctx, cx: number, cy: number, bloom: boolean): void {
  ctx.save();
  if (bloom) {
    ctx.shadowColor = "#ffe27a";
    ctx.shadowBlur = 8;
  }
  ctx.fillStyle = "#ffe27a";
  ctx.fillRect(Math.round(cx - 1.5 * S), Math.round(cy - 1 * S), Math.ceil(3 * S), Math.ceil(2 * S));
  ctx.shadowBlur = 0;
  ctx.fillStyle = "#ffffff";
  ctx.fillRect(Math.round(cx - 0.5 * S), Math.round(cy - 0.5 * S), Math.ceil(1 * S), Math.ceil(1 * S));
  ctx.restore();
}

// Muzzle flash — expanding bright ring / burst, fading by frame.
function bakeMuzzle(ctx: Ctx, cx: number, cy: number, frame: number, bloom: boolean): void {
  const k = frame / Math.max(1, MUZZLE_FLASH_TICKS - 1); // 0..1
  const alpha = 1 - k;
  ctx.save();
  if (bloom) {
    ctx.shadowColor = "#bfefff";
    ctx.shadowBlur = 12;
  }
  ctx.strokeStyle = `rgba(223,246,255,${alpha})`;
  ctx.lineWidth = 2;
  ctx.beginPath();
  ctx.arc(cx, cy, 3 + k * 9, 0, Math.PI * 2);
  ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.restore();
}

// Death poof — expanding orange ring + radial sparks, fading by frame.
function bakePoof(ctx: Ctx, cx: number, cy: number, frame: number, bloom: boolean): void {
  const k = frame / Math.max(1, DEATH_POOF_TICKS - 1); // 0..1
  const alpha = 1 - k;
  ctx.save();
  if (bloom) {
    ctx.shadowColor = "#ff7a5a";
    ctx.shadowBlur = 12;
  }
  ctx.strokeStyle = `rgba(255,140,90,${alpha})`;
  ctx.lineWidth = 2.5;
  ctx.beginPath();
  ctx.arc(cx, cy, 4 + k * 18, 0, Math.PI * 2);
  ctx.stroke();
  ctx.shadowBlur = 0;
  ctx.fillStyle = `rgba(255,200,120,${alpha})`;
  for (let i = 0; i < 7; i++) {
    const a = (i * Math.PI * 2) / 7;
    ctx.fillRect(cx + Math.cos(a) * k * 18 - 1.5, cy + Math.sin(a) * k * 18 - 1.5, 3, 3);
  }
  ctx.restore();
}

// ---- column math (pure; same arithmetic used by lookups + baker) ----

function playerCol(hueIndex: number, walk: number, left: boolean): number {
  const hue = ((hueIndex % PLAYER_HUE_COUNT) + PLAYER_HUE_COUNT) % PLAYER_HUE_COUNT;
  const w = ((walk % WALK_FRAMES) + WALK_FRAMES) % WALK_FRAMES;
  return hue * (WALK_FRAMES * 2) + w * 2 + (left ? 1 : 0);
}
function snipeCol(variant: number, walk: number, left: boolean): number {
  const v = ((variant % SNIPE_VARIANTS) + SNIPE_VARIANTS) % SNIPE_VARIANTS;
  const w = ((walk % WALK_FRAMES) + WALK_FRAMES) % WALK_FRAMES;
  return v * (WALK_FRAMES * 2) + w * 2 + (left ? 1 : 0);
}
function genCol(stage: number, pulse: number): number {
  const s = Math.max(0, Math.min(GEN_STAGES - 1, stage));
  const p = ((pulse % GEN_PULSES) + GEN_PULSES) % GEN_PULSES;
  return s * GEN_PULSES + p;
}

// buildSpriteAtlas bakes every cell once and returns pure lookups. The `bloom`
// palette flag gates the glowing shadowBlur passes.
export function buildSpriteAtlas(palette: Palette, makeCanvas: AtlasCanvasFactory = makeAtlasCanvas): SpriteAtlas {
  const { surface, ctx } = makeCanvas(COLS * CELL, ROWS * CELL);
  const bloom = palette.bloom;

  // Snipe variant tones (spec §6): 0 red, 1 purple.
  const snipeTones: Array<[string, string, string]> = [
    ["#c0324a", "#7e2233", "#ff4d6d"],
    ["#a23ab8", "#6c2680", "#ff6bd6"],
  ];

  // Player row.
  for (let hue = 0; hue < PLAYER_HUE_COUNT; hue++) {
    const suit = palette.playerHues[hue] ?? palette.playerHues[0];
    for (let w = 0; w < WALK_FRAMES; w++) {
      for (let l = 0; l < 2; l++) {
        const left = l === 1;
        const col = playerCol(hue, w, left);
        const cx = col * CELL + HALF;
        const cy = ROW.player * CELL + HALF;
        bakeMarine(ctx, cx, cy, w, left, suit);
      }
    }
  }

  // Snipe row.
  for (let v = 0; v < SNIPE_VARIANTS; v++) {
    const [body, bodyLo, eye] = snipeTones[v];
    for (let w = 0; w < WALK_FRAMES; w++) {
      for (let l = 0; l < 2; l++) {
        const left = l === 1;
        const col = snipeCol(v, w, left);
        const cx = col * CELL + HALF;
        const cy = ROW.snipe * CELL + HALF;
        bakeBug(ctx, cx, cy, w, left, body, bodyLo, eye, bloom);
      }
    }
  }

  // Generator row.
  for (let s = 0; s < GEN_STAGES; s++) {
    for (let p = 0; p < GEN_PULSES; p++) {
      const col = genCol(s, p);
      const cx = col * CELL + HALF;
      const cy = ROW.generator * CELL + HALF;
      bakeHive(ctx, cx, cy, s, p, palette.generator, bloom);
    }
  }

  // Projectile (single cell).
  bakeProjectile(ctx, HALF, ROW.projectile * CELL + HALF, bloom);

  // Muzzle row.
  for (let f = 0; f < MUZZLE_FLASH_TICKS; f++) {
    bakeMuzzle(ctx, f * CELL + HALF, ROW.muzzle * CELL + HALF, f, bloom);
  }

  // Poof row.
  for (let f = 0; f < DEATH_POOF_TICKS; f++) {
    bakePoof(ctx, f * CELL + HALF, ROW.poof * CELL + HALF, f, bloom);
  }

  // Self ring (single cell).
  bakeSelfRing(ctx, HALF, ROW.selfRing * CELL + HALF, palette.selfRing, bloom);

  return {
    image: surface,
    cell: CELL,
    player: (hueIndex, walk, left) => cellAt(ROW.player, playerCol(hueIndex, walk, left)),
    snipe: (variant, walk, left) => cellAt(ROW.snipe, snipeCol(variant, walk, left)),
    generator: (stage, pulse) => cellAt(ROW.generator, genCol(stage, pulse)),
    projectile: () => cellAt(ROW.projectile, 0),
    muzzle: (frame) => cellAt(ROW.muzzle, Math.max(0, Math.min(MUZZLE_FLASH_TICKS - 1, frame | 0))),
    poof: (frame) => cellAt(ROW.poof, Math.max(0, Math.min(DEATH_POOF_TICKS - 1, frame | 0))),
    selfRing: () => cellAt(ROW.selfRing, 0),
  };
}

// ---- neon maze baker (spec §6) ----
//
// bakeNeonMaze rewrites the old solid-tile rasterization into glowing neon
// vector walls over a dithered checker floor. For each wall tile we stroke only
// the edges that border a FLOOR tile (so interior wall faces stay dark), under
// a shadowBlur glow when `bloom`. render.ts's `defaultRasterize` is a thin
// wrapper around this (keeps the heavy impure baking OFF the coverage list,
// spec §4b). Pure function of (maze, palette, tilePx) — no wall-clock.
export function bakeNeonMaze(
  maze: MazeView,
  palette: Palette,
  tilePx: number,
  makeCanvas: AtlasCanvasFactory = makeAtlasCanvas,
): CanvasImageSource {
  const W = maze.W * tilePx;
  const H = maze.H * tilePx;
  const { surface, ctx } = makeCanvas(W, H);

  // Dark background base.
  ctx.fillStyle = palette.bg;
  ctx.fillRect(0, 0, W, H);

  const isWall = (tx: number, ty: number): boolean =>
    tx < 0 || ty < 0 || tx >= maze.W || ty >= maze.H || maze.at(tx, ty) === TileCode.Wall;

  // Two-tone checker floor + sparse dither speckle.
  for (let ty = 0; ty < maze.H; ty++) {
    for (let tx = 0; tx < maze.W; tx++) {
      if (isWall(tx, ty)) continue;
      ctx.fillStyle = (tx + ty) % 2 === 0 ? palette.floor : palette.floorAlt;
      ctx.fillRect(tx * tilePx, ty * tilePx, tilePx, tilePx);
    }
  }
  // Sparse floor dither speckle (deterministic grid, not random — golden-safe).
  // One speckle at each tile center keeps the floor textured without per-pixel
  // noise (which would make the bake size-sensitive and golden-fragile).
  ctx.fillStyle = palette.floorDither;
  for (let y = Math.round(tilePx / 2); y < H; y += tilePx) {
    for (let x = Math.round(tilePx / 2); x < W; x += tilePx) {
      ctx.fillRect(x, y, 1, 1);
    }
  }

  // Neon wall strokes: only edges bordering a floor tile.
  ctx.strokeStyle = palette.wallStroke;
  ctx.lineWidth = Math.max(1, tilePx * 0.09);
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  if (palette.bloom) {
    ctx.shadowColor = palette.wallGlow;
    ctx.shadowBlur = tilePx * 0.35;
  }
  for (let ty = 0; ty < maze.H; ty++) {
    for (let tx = 0; tx < maze.W; tx++) {
      if (!isWall(tx, ty)) continue;
      const x = tx * tilePx;
      const y = ty * tilePx;
      ctx.beginPath();
      if (!isWall(tx, ty - 1)) {
        ctx.moveTo(x, y);
        ctx.lineTo(x + tilePx, y);
      }
      if (!isWall(tx, ty + 1)) {
        ctx.moveTo(x, y + tilePx);
        ctx.lineTo(x + tilePx, y + tilePx);
      }
      if (!isWall(tx - 1, ty)) {
        ctx.moveTo(x, y);
        ctx.lineTo(x, y + tilePx);
      }
      if (!isWall(tx + 1, ty)) {
        ctx.moveTo(x + tilePx, y);
        ctx.lineTo(x + tilePx, y + tilePx);
      }
      ctx.stroke();
    }
  }
  ctx.shadowBlur = 0;

  return surface;
}
