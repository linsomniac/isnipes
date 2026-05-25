// PHASE7.md §6 — Canvas2D render pipeline. Camera follows the local
// player (drawn from prediction); other entities come from the §7.3
// interpolation buffer. The static maze is rasterised once per setMap to
// an OffscreenCanvas and blitted each frame (§6.5). Pure geometry
// (camera, world↔screen, y-sort, visible range) is exported for unit
// tests; the imperative draw path is thin.

import { SUBTILE_PER_TILE, type MazeView, TileCode } from "./sim.js";
import type { Entity } from "./proto.js";
import type { Palette } from "./palette.js";
import type { HudModel } from "./hud.js";

export const TILE_PX = 32;
export const PX_PER_SUBTILE = TILE_PX / SUBTILE_PER_TILE;

// Camera: top-left world position (subtile coords) shown at the viewport
// origin, plus the viewport size in CSS pixels.
export interface Camera {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Viewport {
  w: number;
  h: number;
}

export interface SelfPredicted {
  x: number;
  y: number;
  facing: number;
  flags: number;
}

export interface RenderState {
  map: MazeView | null;
  selfId: number;
  selfPredicted: SelfPredicted | null;
  entities: Entity[]; // interpolated non-self entities (subtile coords)
  renderTick: number;
}

// computeCamera centers (selfX, selfY) in the viewport, clamped so the
// view never extends past the maze bounds. Coordinates are subtile.
export function computeCamera(
  selfX: number,
  selfY: number,
  vp: Viewport,
  maze: MazeView,
): Camera {
  const viewSubW = vp.w / PX_PER_SUBTILE;
  const viewSubH = vp.h / PX_PER_SUBTILE;
  const worldW = maze.W * SUBTILE_PER_TILE;
  const worldH = maze.H * SUBTILE_PER_TILE;
  let x = selfX - viewSubW / 2;
  let y = selfY - viewSubH / 2;
  // Clamp; if the view is wider than the world, pin to 0 (letterbox).
  x = viewSubW >= worldW ? 0 : Math.max(0, Math.min(x, worldW - viewSubW));
  y = viewSubH >= worldH ? 0 : Math.max(0, Math.min(y, worldH - viewSubH));
  return { x, y, w: vp.w, h: vp.h };
}

export function worldToScreen(cam: Camera, wx: number, wy: number): [number, number] {
  return [(wx - cam.x) * PX_PER_SUBTILE, (wy - cam.y) * PX_PER_SUBTILE];
}

// sortEntitiesForDraw returns entities in ascending world-y (pseudo
// depth). Stable on ties by id for deterministic golden frames.
export function sortEntitiesForDraw(entities: readonly Entity[]): Entity[] {
  return [...entities].sort((a, b) => (a.y - b.y) || (a.id - b.id));
}

// One drawable: world position, the entity kind, and the resolved color.
// `selfRank` (0 for non-self, 1 for self) breaks y-ties so the local
// player draws just above a co-located other for visibility, while still
// honoring the ascending-y depth order against entities further south.
export interface DrawItem {
  x: number;
  y: number;
  kind: number;
  color: string;
}

// colorForKind picks the palette slot for an entity kind (1=player,
// 2=generator, 3=projectile, 4=snipe; see internal/sim/config.go).
export function colorForKind(kind: number, palette: Palette): string {
  switch (kind) {
    case 2: return palette.generator;
    case 3: return palette.projectile;
    case 4: return palette.snipe;
    default: return palette.player;
  }
}

// radiusForKind sizes an entity's circle to match its sim hitbox half-extent
// (MAZE_REVAMP.md §4), as a fraction of a tile: player/generator span ~2 tiles
// (halfExt 256 = 1.0 tile radius), a snipe ~1 tile (halfExt 128 = 0.5), and a
// projectile is small (halfExt 48 ≈ 0.1875). So the player draws ≈ 2× a snipe.
export function radiusForKind(kind: number): number {
  switch (kind) {
    case 2: return TILE_PX * 1.0; // generator
    case 3: return TILE_PX * 0.1875; // projectile
    case 4: return TILE_PX * 0.5; // snipe
    default: return TILE_PX * 1.0; // player / self
  }
}

// buildDrawList merges self + non-self entities into one ascending-y draw
// order (DoD #7 invariant applies to the local player too). Self is
// colored with palette.self and, on a y-tie, sorts after a co-located
// other (selfRank) so it stays visible without breaking depth ordering.
export function buildDrawList(s: RenderState, palette: Palette): DrawItem[] {
  const items: (DrawItem & { id: number; selfRank: number })[] = [];
  for (const e of s.entities) {
    items.push({ x: e.x, y: e.y, kind: e.kind, color: colorForKind(e.kind, palette), id: e.id, selfRank: 0 });
  }
  if (s.selfPredicted) {
    items.push({
      x: s.selfPredicted.x, y: s.selfPredicted.y, kind: 1,
      color: palette.self, id: s.selfId, selfRank: 1,
    });
  }
  items.sort((a, b) => (a.y - b.y) || (a.selfRank - b.selfRank) || (a.id - b.id));
  return items.map(({ x, y, kind, color }) => ({ x, y, kind, color }));
}

// ---- maze cache ----

// A CacheImage is anything ctx.drawImage accepts (OffscreenCanvas or a
// detached HTMLCanvasElement). Kept opaque so tests can inject a stub.
export type CacheImage = CanvasImageSource;

export type RasterizeFn = (maze: MazeView, palette: Palette) => CacheImage;

// makeCanvas builds an offscreen draw target, preferring OffscreenCanvas
// with a detached <canvas> fallback (§6.5 / §17).
function makeCanvas(w: number, h: number): {
  surface: CacheImage;
  ctx: CanvasRenderingContext2D;
} {
  if (typeof OffscreenCanvas !== "undefined") {
    const oc = new OffscreenCanvas(w, h);
    const ctx = oc.getContext("2d") as unknown as CanvasRenderingContext2D;
    return { surface: oc as unknown as CacheImage, ctx };
  }
  const c = document.createElement("canvas");
  c.width = w;
  c.height = h;
  return { surface: c, ctx: c.getContext("2d")! };
}

export const defaultRasterize: RasterizeFn = (maze, palette) => {
  const { surface, ctx } = makeCanvas(maze.W * TILE_PX, maze.H * TILE_PX);
  ctx.fillStyle = palette.bg;
  ctx.fillRect(0, 0, maze.W * TILE_PX, maze.H * TILE_PX);
  for (let ty = 0; ty < maze.H; ty++) {
    for (let tx = 0; tx < maze.W; tx++) {
      const t = maze.at(tx, ty);
      if (t === TileCode.Wall) ctx.fillStyle = palette.wall;
      else ctx.fillStyle = (tx + ty) % 2 === 0 ? palette.floor : palette.floorAlt;
      ctx.fillRect(tx * TILE_PX, ty * TILE_PX, TILE_PX, TILE_PX);
    }
  }
  return surface;
};

// RenderCtx is the structural subset of CanvasRenderingContext2D the
// Renderer uses; lets tests inject a recording fake.
export interface RenderCtx {
  canvas: { width: number; height: number };
  fillStyle: string;
  strokeStyle: string;
  lineWidth: number;
  clearRect(x: number, y: number, w: number, h: number): void;
  fillRect(x: number, y: number, w: number, h: number): void;
  drawImage(
    img: CacheImage,
    sx: number, sy: number, sw: number, sh: number,
    dx: number, dy: number, dw: number, dh: number,
  ): void;
  beginPath(): void;
  arc(x: number, y: number, r: number, a0: number, a1: number): void;
  moveTo(x: number, y: number): void;
  lineTo(x: number, y: number): void;
  closePath(): void;
  fill(): void;
  stroke(): void;
  save(): void;
  restore(): void;
}

export class Renderer {
  private ctx: RenderCtx;
  private palette: Palette;
  private rasterize: RasterizeFn;
  private maze: MazeView | null = null;
  private cache: CacheImage | null = null;

  constructor(ctx: RenderCtx, palette: Palette, rasterize: RasterizeFn = defaultRasterize) {
    this.ctx = ctx;
    this.palette = palette;
    this.rasterize = rasterize;
  }

  // setMap rebuilds the maze cache. Called once per MapInit (or on a
  // palette change). draw() never rebuilds — DoD #6.
  setMap(maze: MazeView): void {
    this.maze = maze;
    this.cache = this.rasterize(maze, this.palette);
  }

  setPalette(palette: Palette): void {
    this.palette = palette;
    if (this.maze) this.cache = this.rasterize(this.maze, this.palette);
  }

  // camera computes the current camera from the self position. Exposed
  // for tests + HUD/minimap reuse.
  camera(s: RenderState): Camera {
    const vp = { w: this.ctx.canvas.width, h: this.ctx.canvas.height };
    const sx = s.selfPredicted?.x ?? 0;
    const sy = s.selfPredicted?.y ?? 0;
    return computeCamera(sx, sy, vp, s.map ?? { W: 1, H: 1, at: () => TileCode.Wall });
  }

  draw(s: RenderState, _hud: HudModel): void {
    const ctx = this.ctx;
    const cam = this.camera(s);
    ctx.fillStyle = this.palette.bg;
    ctx.fillRect(0, 0, cam.w, cam.h);

    // Blit the visible maze sub-rect from the cache.
    if (this.cache && this.maze) {
      const sxPx = cam.x * PX_PER_SUBTILE;
      const syPx = cam.y * PX_PER_SUBTILE;
      ctx.drawImage(this.cache, sxPx, syPx, cam.w, cam.h, 0, 0, cam.w, cam.h);
    }

    // Draw all entities (self included) in one ascending-y order so the
    // local player is correctly occluded by / occludes others by depth.
    for (const item of buildDrawList(s, this.palette)) {
      this.drawEntity(cam, item.x, item.y, item.kind, item.color);
    }
  }

  private drawEntity(cam: Camera, wx: number, wy: number, kind: number, color: string): void {
    const [px, py] = worldToScreen(cam, wx, wy);
    const ctx = this.ctx;
    ctx.fillStyle = color;
    ctx.strokeStyle = this.palette.bg;
    ctx.lineWidth = this.palette.outlineWidth;
    ctx.beginPath();
    ctx.arc(px, py, radiusForKind(kind), 0, Math.PI * 2);
    ctx.closePath();
    ctx.fill();
    ctx.stroke();
  }
}
