# Enhanced gameplay graphics — Direction D (pixel sprites + neon maze + CRT)

**Date:** 2026-06-02
**Branch:** `feat/enhanced-graphics`
**Status:** approved (brainstormed via visual companion; user picked Direction D and approved this design)

## 1. Goal

Replace the current flat-colored-circle entity rendering with a richer **retro** look,
keeping the spirit of the original DOS character-graphics SNIPES. Cover the four gameplay
entities the user named: **the local player (you), other players, snipes, and generators**,
plus combat FX (projectile tracer, muzzle flash, death poof).

Today (`web/src/render.ts`) every entity is a flat `ctx.arc()` circle; the maze is solid
dark tiles; `web/src/anim.ts` already contains *unused* sprite scaffolding (4-frame walk,
8-direction facing, muzzle/death `OverlayManager`) that nothing draws. This work fills that gap.

## 2. Chosen direction & decisions (from brainstorming)

**Direction D — Hybrid:** pixel-art creatures on a glowing neon-vector maze under a CRT
scanline + vignette pass. Confirmed choices:

- **Maze:** full neon glowing vector walls (replaces solid-tile rasterization).
- **Retro FX:** bloom + scanlines + vignette **on by default**, behind a `retroFx` settings
  toggle; high-contrast mode drops bloom and thickens outlines; default flips to off under
  `prefers-reduced-motion` when the user has no saved setting.
- **Player identity:** same marine sprite, **per-player hue** (you = cyan = hue 0; others get a
  stable hue by id); a **glowing ground-ring under YOU** so you never lose yourself.
- **Facing:** left/right horizontal mirror + 2-frame walk cycle (not full 8-direction sprites).

## 3. Architecture

The hot path already blits a **baked maze cache** (`drawImage`) and draws entities through a
deliberately *restricted* `RenderCtx` interface (no gradients/shadows/transforms). We preserve
that: all expensive art bakes **once** into offscreen canvases using the full Canvas API; the
per-frame path stays `drawImage`-only.

```
Snapshot ─▶ Entity{kind,hp,facing,flags,vx,vy} ─▶ drawEntity:
              cell = atlas.<kind>( hueIndexForId(id,selfId),
                                   walkFrame(tick, moving = vx||vy),
                                   faceLeft(facing),
                                   damageStage(hp,maxHp) | genPulseFrame(tick) )
              ctx.drawImage(atlas.image, cell → world dest)
           ─▶ self: blit ground-ring cell under local player
           ─▶ overlays (muzzle/poof): blit atlas frame at resolved (x,y)
           ─▶ if retroFx: blit pre-baked CRT overlay (scanlines + vignette) full-canvas
```

Three baked surfaces, each built once (constructor / `setMap` / `setPalette`), never in `draw()`:
1. **Maze cache** — rewritten `defaultRasterize`: neon glowing wall strokes + dithered floor.
2. **Sprite atlas** — `spriteAtlas.ts`: every entity/frame/hue/damage cell.
3. **CRT overlay** — `crt.ts`: scanline pattern + radial vignette, sized to the canvas.

### Why baked atlas (vs per-frame procedural or external PNGs)

Per-frame procedural drawing would force heavy `RenderCtx` extensions (shadow/gradient/transform)
and break the recording-fake used in tests; external PNG sheets need an asset pipeline + image
loading and break embedded-build determinism (and there's no image tooling to author them). The
baked atlas mirrors the existing maze-cache pattern: fast, deterministic, minimal test surface.

## 4. Determinism & frozen-file constraints (HARD)

- **Frozen files — DO NOT EDIT** (verify with `bash scripts/check-frozen.sh` after changes):
  `web/src/{proto,sim,interp,netClient,prediction}.ts` and all `internal/sim`, `internal/proto`.
  We only *import* from `sim.ts`/`proto.ts`.
- **Golden determinism:** rendering must be a pure function of `renderTick` — **no `Date.now()`,
  no `Math.random()`** anywhere in `render.ts`/`anim.ts`/`spriteAtlas.ts`/`crt.ts`. Walk, pulse,
  muzzle/poof frames all derive from `renderTick`. Atlas/CRT/maze bakes are pure functions of
  `(palette, size)`. The `?scene=` golden harness renders at `renderTick=0`.
- Keep `src/` strict-tsc-clean (DOM + ES2022 libs only; no node builtins in `src/`).

### 4a. Headless test env (CRITICAL)
Vitest runs in the **node** environment — `document` and `OffscreenCanvas` are **undefined at test
time**. The shared canvas factory (`makeAtlasCanvas`) MUST therefore degrade to a **no-op recording
ctx stub** when neither is available (never throw): all draw methods no-op, properties settable,
`createLinearGradient`/`createRadialGradient` return `{ addColorStop(){} }`. Then `buildSpriteAtlas`/
`buildCrtOverlay`/neon-`rasterize` run harmlessly headless (returning a stub `image` + real cell
math), so a `Renderer` is constructible in unit tests and `drawImage(stub,…)` is a no-op on the
recording `fakeCtx`. Real canvases are used only in the browser/e2e.

### 4b. Per-file coverage gate (CRITICAL)
`vitest.config.ts` enforces **per-file ≥70% statements/lines** on a COVERED list that includes
`render.ts`, `anim.ts`, `settings.ts`, `palette.ts`. Therefore:
- **Do NOT** add `spriteAtlas.ts`, `crt.ts`, or any new maze-art baker to the COVERED list — they
  are canvas/integration layers (e2e-covered), like `browser.ts`/`scenes.ts`.
- Keep `render.ts` **thin**: put the heavy impure baking (neon maze, atlas, CRT) in the non-covered
  new modules; `defaultRasterize` becomes a thin wrapper delegating to the maze baker. Ensure
  `render.test.ts` constructs a `Renderer` (headless factory) and calls `draw()` over players/snipe/
  generator/projectile + an overlay so the blit path executes and stays ≥70%.
- New code added to `anim.ts`/`settings.ts`/`palette.ts` must be exercised by unit tests to hold ≥70%.

## 5. Module contracts (code to these exactly — anti-drift)

### 5a. `web/src/palette.ts` (extend; not frozen)
Add to `Palette` (additive — keep all existing fields so `colorForKind` tests pass):
```ts
  wallStroke: string;     // neon maze line color
  wallGlow: string;       // maze stroke glow (shadowColor) color
  floorDither: string;    // subtle floor speckle color
  selfRing: string;       // glowing ground-ring under local player
  playerHues: string[];   // per-player suit hues; [0] = self (cyan). length == PLAYER_HUE_COUNT
  bloom: boolean;         // bake glow into atlas/maze (false under high-contrast)
  scanlineAlpha: number;  // 0..1 CRT scanline darkness (0 → none)
  vignetteAlpha: number;  // 0..1 CRT vignette strength
```
- `DEFAULT_PALETTE`: neon cyan walls, hostile-red snipes, magenta/purple generator, 7 distinct
  player hues ([0]=#4fd1ff cyan, then green/orange/purple/teal/pink/yellow), bloom true,
  scanlineAlpha ~0.18, vignetteAlpha ~0.55.
- `COLORBLIND_PALETTE`: hostiles orange, friendlies blue axis; colorblind-safe `playerHues`.
- `applyHighContrast(p,true)`: `bloom:false`, `outlineWidth:3`, `scanlineAlpha:0`, brighter strokes.

### 5b. `web/src/anim.ts` (extend; not frozen — keep existing exports)
```ts
export const GEN_PULSE_TICKS = 8;
export function genPulseFrame(renderTick: number): 0|1|2|3;   // floor(tick/GEN_PULSE_TICKS) % 4
export function faceLeft(facing: number): boolean;            // Dir8 SW(6)/W(7)/NW(8) → true
export function damageStage(hp: number, maxHp: number): 0|1|2;// 0 healthy ≥⅔, 1 cracked, 2 critical
export function muzzleFrame(age: number): number;             // 0..(MUZZLE_FLASH_TICKS-1) clamp
export function poofFrame(age: number): number;               // 0..(DEATH_POOF_TICKS-1) clamp
```
`walkFrame`, `dir8FromFacing`, `OverlayManager`, tick constants stay as-is.

### 5c. `web/src/spriteAtlas.ts` (NEW; not frozen)
```ts
import type { Palette } from "./palette.js";
export type AtlasCanvasFactory =
  (w: number, h: number) => { surface: CanvasImageSource; ctx: CanvasRenderingContext2D };
export interface Cell { sx: number; sy: number; sw: number; sh: number; }
export interface SpriteAtlas {
  image: CanvasImageSource;
  cell: number;                                            // square cell px (use 48)
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
export function hueIndexForId(id: number, selfId: number): number; // self→0; else 1..HUE_COUNT-1 stable by id
export function buildSpriteAtlas(palette: Palette, makeCanvas?: AtlasCanvasFactory): SpriteAtlas;
```
- Lookups are pure arithmetic over a fixed grid layout (one row per category, columns per frame);
  `buildSpriteAtlas` bakes all cells once via `makeCanvas` (defaults to the OffscreenCanvas /
  detached-`<canvas>` factory shared with `render.ts` — extract `makeAtlasCanvas` and reuse).
- `hueIndexForId(id, selfId)`: returns 0 for `id === selfId`; else `1 + (id % (PLAYER_HUE_COUNT-1))`.
- Port the **validated pixel-art routines in §6** into the baker. `bloom` palette flag controls
  whether glowing `shadowBlur` is applied while baking.

### 5d. `web/src/crt.ts` (NEW; not frozen)
```ts
import type { AtlasCanvasFactory } from "./spriteAtlas.js";
export interface CrtOverlay { image: CanvasImageSource; }
export function buildCrtOverlay(
  w: number, h: number, scanlineAlpha: number, vignetteAlpha: number,
  makeCanvas?: AtlasCanvasFactory,
): CrtOverlay;
```
Pre-renders horizontal scanlines (every 3px) + a radial vignette into a `w×h` canvas.

### 5e. `web/src/render.ts` (rewrite draw path; keep geometry exports & maze-cache contract)
- Keep & unchanged: `computeCamera`, `worldToScreen`, `sortEntitiesForDraw`,
  `colorForKind`, `radiusForKind`, `TILE_PX`, `PX_PER_SUBTILE`, camera/cache logic, and the
  **"rasterize once, never in draw()"** invariant (existing test asserts it).
- **Extend `DrawItem`** to carry the per-entity data the atlas needs:
  `{ x, y, kind, color, id, facing, vx, vy, hp, isSelf }` (keep the existing `x,y,kind,color`
  fields so the current `buildDrawList` test still passes). `buildDrawList` populates them: other
  entities from `Entity`; self → `id: selfId, facing: selfPredicted.facing, vx: 0, vy: 0, hp: 0,
  isSelf: true`. `drawEntity` reads these to pick the atlas cell (hue, walk-moving, facing, damage).
- Rewrite `defaultRasterize` → neon maze (full-API offscreen ctx; glowing wall strokes via the
  §6 routine; dithered floor).
- Extend `RenderState` with resolved overlays:
  ```ts
  export interface ResolvedOverlay { kind: "muzzle" | "poof"; x: number; y: number; frame: number; }
  // RenderState gains:  overlays?: ResolvedOverlay[];
  ```
- `Renderer`: build sprite atlas + CRT overlay once in the constructor and on `setPalette`
  (alongside the maze cache); add `retroFx` (default `true`) via a 4th constructor arg
  `opts?: { retroFx?: boolean }` and `setRetroFx(on: boolean)`. **Do not** rebuild bakes in `draw()`.
- `drawEntity`: replace the circle with an atlas blit. Compute the cell from kind:
  - player(1): `atlas.player(hueIndexForId(id, selfId), walkFrame(tick, moving), faceLeft(facing))`,
    `moving = vx!==0 || vy!==0` (self: treat as moving when its `flags`/position imply motion — if
    unknown, `false`). Blit `atlas.selfRing()` centered under the local player first.
  - snipe(4): `atlas.snipe(id % SNIPE_VARIANTS, walkFrame(...), faceLeft(facing))`.
  - generator(2): `atlas.generator(damageStage(hp, GEN_MAX_HP), genPulseFrame(tick))`.
  - projectile(3): `atlas.projectile()`.
  - Dead entities (`flags & FlagBits.dead`, if present) draw dimmed (lower the blit via a faded cell
    or skip — keep simple: still blit, no special-case if a dead flag isn't reliably set).
  - The renderer needs `selfId`, `renderTick`, and per-entity `id/facing/vx/vy/hp` — all already on
    `RenderState`/`Entity`/`SelfPredicted` except self `vx/vy`; pass self motion via existing fields
    (acceptable to render self with `moving=false`/idle if velocity unavailable).
- After entities: blit each `overlays[]` cell at world→screen (x,y); then if `retroFx`, blit the
  CRT overlay over the whole canvas.
- Hot path must use only `drawImage` (+ existing arc/fill if any self-ring fallback). If a new ctx
  capability is unavoidable, extend the `RenderCtx` interface **and** the `fakeCtx` in
  `render.test.ts` together. Prefer baked cells to avoid this.

### 5f. `web/src/settings.ts` (extend; not frozen)
- Add `retroFx: boolean` to `Settings`; `defaultSettings().retroFx = true`; `loadSettings` parses
  `parsed.retroFx !== false` (default true); `saveSettings` already serializes the whole object.

### 5g. `web/src/browser.ts` (wire; not frozen)
- Settings panel: add a **"Retro FX"** checkbox (mirror the high-contrast toggle), persisted, and
  call `renderer.setRetroFx(...)` live.
- Construct `Renderer` with `{ retroFx: settings.retroFx }` in both `MatchRunner` and
  `renderMatchScene` (golden scenes render with retroFx **on**).
- Resolve overlays for the renderer: from `overlays.active(renderTick)`, map each `Overlay`
  (anchorId/kind/armedAtTick) to `ResolvedOverlay{ kind, x, y, frame }` by looking up the anchor's
  position in the latest snapshot / registry and computing `frame = muzzleFrame/poofFrame(tick -
  armedAtTick)`; drop overlays whose anchor is unknown. Pass via `RenderState.overlays`.
- At boot, if there is **no** saved settings blob and `matchMedia('(prefers-reduced-motion: reduce)')`
  matches, default `retroFx` to `false`.
- `GEN_MAX_HP`: read the generator's initial hp from config or hardcode the known max (generators
  spawn with hp 3 per the scene/sim); expose a small constant so `damageStage` has a denominator.

## 6. Validated pixel-art reference (port these into the atlas baker)

These routines were validated in the visual companion (Direction D) and are the source of truth for
the look. `s` = pixel unit (atlas bakes at `s≈3`, cell 48px, sprite centered in the cell). `P(x,y,w,h,c)`
fills a rect at sprite-local pixel coords. `bloom` gates the `shadowBlur` glow.

**Marine (player / other players):** helmet block + bright visor + glint; suit torso with shoulder
highlight + chest light (the hue); side arms; 2-frame walk (alternate boot lengths). Hue comes from
`palette.playerHues[hueIndex]` (suit), a darker derived tone for arms/legs, near-black visor, bright
glint. Mirror horizontally for `left`.

```
helmet  P(-3,-7,6,4)  +top hi P(-3,-7,6,1)
visor   P(-2,-6,4,2)  +glint P(-2,-6,1,1)
torso   P(-3,-3,6,5)  +shoulder hi P(-3,-3,6,1)  +chest light P(-1,-1,2,2)=glint
arms    P(-4,-3,1,4) P(3,-3,1,4)
legs frame0: P(-3,2,2,3) P(1,2,2,2) ;  frame1: P(-3,2,2,2) P(1,2,2,3)
```

**Snipe (pixel bug, 2 variants by hue):** dome body, two glowing eyes (shadowBlur if bloom) + white
glint, antennae, mandibles, skittering legs alternating by walk frame. Variant 0 red `#c0324a/#ff4d6d`,
variant 1 purple `#a23ab8/#ff6bd6`.

```
antennae P(-2,-5,1,2)+tip eye ; P(1,-5,1,2)+tip eye
legs     L:P(-5,a,1,2)P(-5,2,1,2)  R:P(4,b,1,2)P(4,2,1,2)  (a,b swap by frame)
dome     P(-3,-3,6,1) P(-4,-2,8,4) P(-4,2,8,1=lo)
mandibles P(-4,1,1,1) P(3,1,1,1)
eyes(glow) P(-3,-1,2,2) P(1,-1,2,2) + white glint P(-3,-1,1,1) P(1,-1,1,1)
```

**Generator (alien hive):** base slab, dome with top highlight + side/seam shading, a glowing core
(`shadowBlur` if bloom; brighter at higher `pulse`), and **damage cracks** at stage ≥1 (yellow glowing
polyline), more at stage 2.

```
base P(-5,2,10,3) ; dome P(-4,-4,8,6)+top hi P(-4,-4,8,1)+seams
core(glow) P(-2,-1,4,3) color brightens with pulse
stage>=1: yellow crack polyline ; stage 2: add a second crack + dim core
```

**Self ring:** a flattened glowing ellipse ring (cyan `palette.selfRing`) baked as its own cell,
blitted centered under the local player. (Bake with `scale(1,0.42)` + `shadowBlur`.)

**Projectile:** small chunky bright bolt (`#ffe27a` core + white center).

**Muzzle (MUZZLE_FLASH_TICKS frames):** expanding bright ring / pixel burst, fading by frame.

**Poof (DEATH_POOF_TICKS frames):** expanding orange ring + radial sparks, fading by frame.

**Neon maze (`defaultRasterize`):** for each wall tile, stroke the edges that border a floor tile
with `palette.wallStroke` (lineWidth ~ TILE_PX*0.09, round caps/joins) under `shadowColor =
palette.wallGlow, shadowBlur ~ TILE_PX*0.35` when `bloom`. Floor: two-tone checker (`floor/floorAlt`)
plus a sparse `floorDither` speckle.

**CRT overlay (`crt.ts`):** horizontal `rgba(0,0,8,scanlineAlpha)` lines every 3px, plus a radial
vignette gradient (transparent center → `rgba(0,0,12,vignetteAlpha)` edge).

> The complete animated reference lives (gitignored) at
> `.superpowers/brainstorm/1214339-1780420784/content/target-preview.html` and `art-directions.html`.

## 7. Testing & goldens

- **Unit (vitest, `npm -C web test`):**
  - Keep render geometry + maze-cache-reuse tests; **rewrite** `render.test.ts` draw-path assertions
    to the atlas/`drawImage` model (entities blit, not arc). Update `fakeCtx` only if `RenderCtx` grew.
  - New `spriteAtlas.test.ts`: `hueIndexForId` (self→0, stable, in-range); `buildSpriteAtlas` bakes
    once (recording-fake factory called once) and returns non-overlapping cells of size `cell`.
  - New `crt.test.ts`: builds once; `scanlineAlpha 0` ⇒ no scanline fills (recording fake).
  - Extend `anim.test.ts`: `genPulseFrame`, `faceLeft`, `damageStage`, `muzzle/poofFrame` ranges,
    all pure in `renderTick`.
  - `settings.test.ts` (if present) / add: `retroFx` round-trips and defaults true.
- **Golden e2e (`npm -C web run test:e2e -- --update-snapshots`, Chromium):** the `in-match-hud`,
  `scoreboard`, `end-screen` baselines **change** (entities are sprites now) and must be regenerated
  and eyeballed; `empty-lobby`/`full-lobby` are unaffected. `maxDiffPixelRatio` stays 0.02.
- **Frozen guard:** `bash scripts/check-frozen.sh` must pass (no frozen file touched).

## 8. Gate commands

```
npm -C web test                                  # vitest (124+ tests)
npm -C web run build                             # esbuild bundle (catches import/type breakage)
npm -C web run test:e2e -- --update-snapshots    # regenerate goldens
npm -C web run test:e2e                           # verify goldens green
bash scripts/check-frozen.sh                     # frozen-file guard
```

## 9. Out of scope (this pass)

Full 8-direction sprites (chose L/R mirror); HUD/minimap visual redesign; audio; new maze geometry;
server/sim changes of any kind.
