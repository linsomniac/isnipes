// PHASE7.md §6 — Canvas2D render pipeline. Camera follows the local
// player (drawn from prediction); other entities come from the §7.3
// interpolation buffer. The static maze is rasterised once per setMap to
// an OffscreenCanvas and blitted each frame (§6.5). Pure geometry
// (camera, world↔screen, y-sort, visible range) is exported for unit
// tests; the imperative draw path is thin.
//
// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5e, §6 —
// Direction-D enhanced graphics: the flat-circle entity draw is replaced by
// baked pixel-art atlas blits (per-player hue, walk cycle, L/R facing,
// generator damage + pulse), a glowing self ground-ring under the local
// player, muzzle/poof overlay blits, and an optional CRT scanline+vignette
// pass. The heavy impure baking (neon maze, sprite atlas, CRT) lives in the
// NON-covered modules (spriteAtlas.ts, crt.ts) so render.ts stays thin and the
// "rasterize once, never in draw()" invariant holds: atlas/CRT/maze are all
// built in the constructor / setMap / setPalette, never in draw().

import { SUBTILE_PER_TILE, type MazeView, TileCode } from "./sim.js";
import type { Entity } from "./proto.js";
import type { Palette } from "./palette.js";
import type { HudModel } from "./hud.js";
import {
  walkFrame, faceLeft, damageStage, genPulseFrame,
} from "./anim.js";
import {
  buildSpriteAtlas, bakeNeonMaze, hueIndexForId, makeAtlasCanvas,
  SNIPE_VARIANTS, type SpriteAtlas,
} from "./spriteAtlas.js";
import { buildCrtOverlay, type CrtOverlay } from "./crt.js";

export const TILE_PX = 32;
export const PX_PER_SUBTILE = TILE_PX / SUBTILE_PER_TILE;

// GEN_MAX_HP: the BASE generator spawn hp (letters A..S; internal/sim/levels.go
// generatorHPBase). Exposed as the default damageStage(hp, maxHp) denominator
// and the seed for per-generator observed-max tracking.
//
// AIDEV-NOTE: brutal levels T..Z spawn generators at hp 5 (generatorHPBrutal),
// not 3, so a fixed denominator made the brutal hive's damage sprite mis-track
// true HP fraction (a 60%-HP brutal generator read as "healthy"). The wire
// proto (frozen Entity = current hp only; MapInit/MatchStarted carry no level
// letter or max-HP) gives the client no max-HP, so the Renderer derives each
// generator's max from the highest hp it has been observed at — its spawn HP —
// and uses that as the per-entity denominator (DrawItem.maxHp). This is a pure
// function of the deterministic snapshot stream (no wall-clock), so golden
// frames stay reproducible. See spec §5g (premise "hardcode 3" was wrong for
// brutal) and the brutal-generator finding.
export const GEN_MAX_HP = 3;

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

// A combat overlay resolved to a world position + animation frame, ready to
// blit (spec §5e). browser.ts maps OverlayManager.active() → these by looking
// up the anchor's position and computing frame = muzzleFrame/poofFrame(age).
export interface ResolvedOverlay {
  kind: "muzzle" | "poof";
  x: number;
  y: number;
  frame: number;
}

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

// One drawable: world position, the entity kind, and the resolved color, plus
// the per-entity data the atlas needs to pick a cell (spec §5e): id (→ hue),
// facing (→ L/R mirror), vx/vy (→ walk-moving), hp (→ generator damage stage),
// and isSelf (→ ground-ring + self hue). The original {x,y,kind,color} fields
// are preserved so the existing buildDrawList test keeps passing.
// `selfRank` (0 for non-self, 1 for self) breaks y-ties so the local
// player draws just above a co-located other for visibility, while still
// honoring the ascending-y depth order against entities further south.
export interface DrawItem {
  x: number;
  y: number;
  kind: number;
  color: string;
  id: number;
  facing: number;
  vx: number;
  vy: number;
  hp: number;
  // maxHp: the entity's spawn/maximum hp, used as the damageStage denominator
  // (generators only). Defaults to GEN_MAX_HP; the Renderer overrides it with
  // each generator's observed spawn HP so brutal (hp 5) hives track true HP.
  maxHp: number;
  isSelf: boolean;
}

// MaxHpLookup resolves an entity id to its observed spawn/maximum hp. Supplied
// by the Renderer (which tracks the highest hp seen per generator); pure-frame
// callers (tests) may omit it, falling back to GEN_MAX_HP.
export type MaxHpLookup = (id: number, hp: number) => number;

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
export function buildDrawList(s: RenderState, palette: Palette, maxHpOf?: MaxHpLookup): DrawItem[] {
  const items: (DrawItem & { selfRank: number })[] = [];
  for (const e of s.entities) {
    items.push({
      x: e.x, y: e.y, kind: e.kind, color: colorForKind(e.kind, palette),
      id: e.id, facing: e.facing, vx: e.vx, vy: e.vy, hp: e.hp,
      maxHp: maxHpOf ? maxHpOf(e.id, e.hp) : GEN_MAX_HP, isSelf: false,
      selfRank: 0,
    });
  }
  if (s.selfPredicted) {
    // Self motion isn't carried on SelfPredicted (spec §5e accepts rendering
    // the local player idle when velocity is unavailable): vx/vy = 0.
    items.push({
      x: s.selfPredicted.x, y: s.selfPredicted.y, kind: 1, color: palette.self,
      id: s.selfId, facing: s.selfPredicted.facing, vx: 0, vy: 0, hp: 0,
      maxHp: GEN_MAX_HP, isSelf: true,
      selfRank: 1,
    });
  }
  items.sort((a, b) => (a.y - b.y) || (a.selfRank - b.selfRank) || (a.id - b.id));
  return items.map(({ selfRank: _selfRank, ...item }) => item);
}

// ---- maze cache ----

// A CacheImage is anything ctx.drawImage accepts (OffscreenCanvas or a
// detached HTMLCanvasElement). Kept opaque so tests can inject a stub.
export type CacheImage = CanvasImageSource;

export type RasterizeFn = (maze: MazeView, palette: Palette) => CacheImage;

// defaultRasterize bakes the neon maze (spec §6). Thin wrapper delegating to
// bakeNeonMaze in the NON-covered spriteAtlas module so the heavy impure
// canvas baking stays off the per-file coverage gate (spec §4b). Still a pure
// function of (maze, palette) — built once per setMap/setPalette, never in
// draw() (the maze-cache-reuse invariant test asserts this).
export const defaultRasterize: RasterizeFn = (maze, palette) => bakeNeonMaze(maze, palette, TILE_PX);

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

// blitSizeForKind sizes the on-screen sprite (the square dest the atlas cell is
// scaled to). Mirrors the old circle footprints (radiusForKind) so the visual
// scale is preserved: player/generator ≈ 2 tiles, snipe ≈ 1.4, projectile small.
function blitSizeForKind(kind: number): number {
  switch (kind) {
    case 2: return TILE_PX * 2.0; // generator
    case 3: return TILE_PX * 0.9; // projectile
    case 4: return TILE_PX * 1.6; // snipe
    default: return TILE_PX * 2.0; // player / self
  }
}

const OVERLAY_BLIT_PX = TILE_PX * 1.8;

export class Renderer {
  private ctx: RenderCtx;
  private palette: Palette;
  private rasterize: RasterizeFn;
  private maze: MazeView | null = null;
  private cache: CacheImage | null = null;
  // Baked Direction-D surfaces (spec §5e): built ONCE in the constructor and on
  // setPalette, never in draw(). The CRT overlay is also rebuilt when the
  // canvas size changes (it is canvas-sized).
  private atlas: SpriteAtlas;
  private crt: CrtOverlay;
  private crtW = 0;
  private crtH = 0;
  private retroFx: boolean;
  // genMaxHp: per-generator observed maximum hp (its spawn HP). A generator
  // first appears at full health, so the running max recovers the true max
  // (3 base / 5 brutal) without any wire data, and never decreases as the
  // hive takes damage. Pure over the deterministic snapshot stream.
  // AIDEV-NOTE: seeded/read in draw() via maxHpFor(); never wall-clock driven.
  private genMaxHp = new Map<number, number>();

  constructor(
    ctx: RenderCtx,
    palette: Palette,
    rasterize: RasterizeFn = defaultRasterize,
    opts?: { retroFx?: boolean },
  ) {
    this.ctx = ctx;
    this.palette = palette;
    this.rasterize = rasterize;
    this.retroFx = opts?.retroFx ?? true;
    this.atlas = buildSpriteAtlas(palette);
    this.crtW = ctx.canvas.width;
    this.crtH = ctx.canvas.height;
    this.crt = buildCrtOverlay(this.crtW, this.crtH, palette.scanlineAlpha, palette.vignetteAlpha);
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
    // Rebuild the baked atlas + CRT (hues / bloom / scanline alpha changed).
    this.atlas = buildSpriteAtlas(palette);
    this.crt = buildCrtOverlay(this.crtW, this.crtH, palette.scanlineAlpha, palette.vignetteAlpha);
  }

  // setRetroFx toggles the CRT scanline+vignette pass live (spec §5e).
  setRetroFx(on: boolean): void {
    this.retroFx = on;
  }

  // maxHpFor returns the highest hp this generator id has been observed at —
  // its spawn HP, which equals its max HP. Seeded on first sight and only ever
  // raised, so a damaged generator keeps its true denominator. Used as the
  // damageStage denominator so brutal (spawn hp 5) hives crack at the correct
  // HP fraction instead of against a fixed 3. Defaults to GEN_MAX_HP if unseen.
  private maxHpFor(id: number, hp: number): number {
    const prev = this.genMaxHp.get(id) ?? GEN_MAX_HP;
    const m = hp > prev ? hp : prev;
    if (m !== prev) this.genMaxHp.set(id, m);
    else if (!this.genMaxHp.has(id)) this.genMaxHp.set(id, m);
    return m;
  }

  // camera computes the current camera from the self position. Exposed
  // for tests + HUD/minimap reuse.
  camera(s: RenderState): Camera {
    const vp = { w: this.ctx.canvas.width, h: this.ctx.canvas.height };
    const sx = s.cameraOverride?.x ?? s.selfPredicted?.x ?? 0;
    const sy = s.cameraOverride?.y ?? s.selfPredicted?.y ?? 0;
    return computeCamera(sx, sy, vp, s.map ?? { W: 1, H: 1, at: () => TileCode.Wall });
  }

  draw(s: RenderState, _hud: HudModel): void {
    const ctx = this.ctx;
    const cam = this.camera(s);

    // The CRT overlay is canvas-sized: rebuild it (NOT the atlas/maze) if the
    // canvas was resized since the last bake. Still never per-frame in the
    // steady state — only on an actual size change.
    if (cam.w !== this.crtW || cam.h !== this.crtH) {
      this.crtW = cam.w;
      this.crtH = cam.h;
      this.crt = buildCrtOverlay(this.crtW, this.crtH, this.palette.scanlineAlpha, this.palette.vignetteAlpha);
    }

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
    // maxHpFor resolves each generator's observed spawn HP as the damageStage
    // denominator (so brutal hp-5 hives track true HP fraction).
    for (const item of buildDrawList(s, this.palette, (id, hp) => this.maxHpFor(id, hp))) {
      this.drawEntity(cam, item, s.selfId, s.renderTick);
    }

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

  // drawEntity blits the atlas cell for `item` (spec §5e). The cell is chosen
  // per kind from the per-entity data carried on the DrawItem: hue (id), walk
  // (vx/vy moving), facing (L/R), generator damage (hp) + pulse (renderTick).
  // The local player also gets a glowing ground-ring blitted under it.
  private drawEntity(cam: Camera, item: DrawItem, selfId: number, renderTick: number): void {
    const [px, py] = worldToScreen(cam, item.x, item.y);
    const ctx = this.ctx;
    const atlas = this.atlas;
    const moving = item.vx !== 0 || item.vy !== 0;
    const left = faceLeft(item.facing);

    // Self ground-ring first (under the marine).
    if (item.isSelf) {
      this.blitSelfRing(px, py);
    }

    let cell;
    switch (item.kind) {
      case 1: // player / self
        cell = atlas.player(hueIndexForId(item.id, selfId), walkFrame(item.id, renderTick, moving), left);
        break;
      case 2: // generator
        cell = atlas.generator(damageStage(item.hp, item.maxHp), genPulseFrame(renderTick));
        break;
      case 3: // projectile
        cell = atlas.projectile();
        break;
      case 4: // snipe
        cell = atlas.snipe(item.id % SNIPE_VARIANTS, walkFrame(item.id, renderTick, moving), left);
        break;
      default:
        cell = atlas.player(hueIndexForId(item.id, selfId), walkFrame(item.id, renderTick, moving), left);
    }
    this.blitCell(cell, px, py, blitSizeForKind(item.kind));
  }

  private drawOverlay(cam: Camera, ov: ResolvedOverlay): void {
    const [px, py] = worldToScreen(cam, ov.x, ov.y);
    const cell = ov.kind === "muzzle" ? this.atlas.muzzle(ov.frame) : this.atlas.poof(ov.frame);
    this.blitCell(cell, px, py, OVERLAY_BLIT_PX);
  }

  // blitSelfRing draws the flattened ground-ring under the local player. The
  // ring lives in a wider, non-square atlas region (so its glow isn't cropped),
  // so it blits at the source aspect — not squashed into a square — and is
  // dropped to the marine's feet. Sized a touch under the marine footprint so it
  // reads as a halo at the feet, not a circle the player is clipped inside.
  private blitSelfRing(px: number, py: number): void {
    const cell = this.atlas.selfRing();
    const w = TILE_PX * 2.5; // on-screen ring footprint width
    const h = w * (cell.sh / cell.sw); // preserve the flattened aspect
    const footY = TILE_PX * 0.5; // sit the ring at the marine's feet
    this.ctx.drawImage(
      this.atlas.image,
      cell.sx, cell.sy, cell.sw, cell.sh,
      px - w / 2, py - h / 2 + footY, w, h,
    );
  }

  // blitCell draws an atlas cell centered at (px,py), scaled to `size` px.
  private blitCell(cell: { sx: number; sy: number; sw: number; sh: number }, px: number, py: number, size: number): void {
    this.ctx.drawImage(
      this.atlas.image,
      cell.sx, cell.sy, cell.sw, cell.sh,
      px - size / 2, py - size / 2, size, size,
    );
  }
}
