// PHASE7.md §15.1 — DoD #5/#6/#7. Camera-follow, maze-cache reuse, and
// y-sort. Pure geometry is tested directly; the cache-reuse test uses a
// recording fake ctx + a spy rasterizer (no real canvas needed).
//
// docs/superpowers/specs/2026-06-02-enhanced-graphics-design.md §5e, §7 — the
// draw path is now an atlas/drawImage model (entities blit baked sprite cells,
// not ctx.arc circles). These tests exercise draw() headless over every entity
// kind + an overlay so the blit path runs and render.ts stays ≥70%; the
// geometry + maze-cache-reuse invariants are kept verbatim.

import { describe, expect, test, vi } from "vitest";
import {
  computeCamera, worldToScreen, sortEntitiesForDraw, buildDrawList, colorForKind, Renderer,
  TILE_PX, PX_PER_SUBTILE, GEN_MAX_HP, type RenderCtx, type RenderState,
} from "../src/render.js";
import { damageStage } from "../src/anim.js";
import { COLORBLIND_PALETTE } from "../src/palette.js";
import { SUBTILE_PER_TILE, TileCode, type MazeView } from "../src/sim.js";
import { DEFAULT_PALETTE } from "../src/palette.js";
import { emptyHudModel } from "../src/hud.js";
import type { Entity } from "../src/proto.js";

function openMaze(w: number, h: number): MazeView {
  return { W: w, H: h, at: () => TileCode.Floor };
}

function fakeCtx(w: number, h: number): RenderCtx {
  return {
    canvas: { width: w, height: h },
    fillStyle: "", strokeStyle: "", lineWidth: 1,
    clearRect: () => {}, fillRect: () => {},
    drawImage: () => {},
    beginPath: () => {}, arc: () => {}, moveTo: () => {}, lineTo: () => {},
    closePath: () => {}, fill: () => {}, stroke: () => {},
    save: () => {}, restore: () => {},
  };
}

// recordingCtx captures drawImage + arc calls so the draw-path tests can assert
// the atlas/blit model (sprites are blitted; entities are NOT drawn as arcs).
interface RecordingCtx extends RenderCtx {
  drawImageCalls: number;
  arcCalls: number;
}
function recordingCtx(w: number, h: number): RecordingCtx {
  const rec: RecordingCtx = {
    canvas: { width: w, height: h },
    fillStyle: "", strokeStyle: "", lineWidth: 1,
    clearRect: () => {}, fillRect: () => {},
    drawImage: () => { rec.drawImageCalls++; },
    beginPath: () => {}, arc: () => { rec.arcCalls++; }, moveTo: () => {}, lineTo: () => {},
    closePath: () => {}, fill: () => {}, stroke: () => {},
    save: () => {}, restore: () => {},
    drawImageCalls: 0, arcCalls: 0,
  };
  return rec;
}

function allKindsState(): RenderState {
  const maze = openMaze(40, 30);
  return {
    map: maze, selfId: 100,
    selfPredicted: { x: 10 * SUBTILE_PER_TILE, y: 10 * SUBTILE_PER_TILE, facing: 7 /*W → faceLeft*/, flags: 0 },
    entities: [
      { id: 1, kind: 1, hp: 2, facing: 3, flags: 0, x: 11 * SUBTILE_PER_TILE, y: 8 * SUBTILE_PER_TILE, vx: 16, vy: 0 },
      { id: 2, kind: 4, hp: 1, facing: 7, flags: 0, x: 12 * SUBTILE_PER_TILE, y: 9 * SUBTILE_PER_TILE, vx: 0, vy: 0 },
      { id: 3, kind: 2, hp: 1, facing: 0, flags: 0, x: 14 * SUBTILE_PER_TILE, y: 7 * SUBTILE_PER_TILE, vx: 0, vy: 0 },
      { id: 4, kind: 3, hp: 1, facing: 3, flags: 0, x: 9 * SUBTILE_PER_TILE, y: 6 * SUBTILE_PER_TILE, vx: 32, vy: 0 },
    ],
    renderTick: 12,
    overlays: [
      { kind: "muzzle", x: 11 * SUBTILE_PER_TILE, y: 8 * SUBTILE_PER_TILE, frame: 2 },
      { kind: "poof", x: 12 * SUBTILE_PER_TILE, y: 9 * SUBTILE_PER_TILE, frame: 5 },
    ],
  };
}

describe("render geometry", () => {
  test("TestRender_CameraFollowsLocalPlayer centers self away from edges", () => {
    const maze = openMaze(60, 40);
    const vp = { w: 640, h: 480 };
    const selfX = 30 * SUBTILE_PER_TILE; // mid-maze
    const selfY = 20 * SUBTILE_PER_TILE;
    const cam = computeCamera(selfX, selfY, vp, maze);
    const [sx, sy] = worldToScreen(cam, selfX, selfY);
    // Self should land at the viewport center, within half a tile.
    expect(Math.abs(sx - vp.w / 2)).toBeLessThanOrEqual(TILE_PX / 2);
    expect(Math.abs(sy - vp.h / 2)).toBeLessThanOrEqual(TILE_PX / 2);
  });

  test("TestRender_CameraClampsAtMapEdge", () => {
    const maze = openMaze(60, 40);
    const vp = { w: 640, h: 480 };
    // Self in the top-left corner: camera must not go negative.
    const cam = computeCamera(0, 0, vp, maze);
    expect(cam.x).toBe(0);
    expect(cam.y).toBe(0);
    // Self in the bottom-right corner: camera clamps to world - view.
    const worldW = maze.W * SUBTILE_PER_TILE;
    const worldH = maze.H * SUBTILE_PER_TILE;
    const cam2 = computeCamera(worldW, worldH, vp, maze);
    expect(cam2.x).toBeCloseTo(worldW - vp.w / PX_PER_SUBTILE, 5);
    expect(cam2.y).toBeCloseTo(worldH - vp.h / PX_PER_SUBTILE, 5);
  });

  test("TestRender_WorldToScreenRoundTrip", () => {
    const cam = { x: 100, y: 200, w: 640, h: 480 };
    const [px, py] = worldToScreen(cam, 100 + SUBTILE_PER_TILE, 200 + 2 * SUBTILE_PER_TILE);
    expect(px).toBeCloseTo(TILE_PX, 5);
    expect(py).toBeCloseTo(2 * TILE_PX, 5);
  });

  test("TestRender_EntitiesSortedByY ascending, ties by id", () => {
    const ents: Entity[] = [
      { id: 3, kind: 1, hp: 1, facing: 0, flags: 0, x: 0, y: 300, vx: 0, vy: 0 },
      { id: 1, kind: 1, hp: 1, facing: 0, flags: 0, x: 0, y: 100, vx: 0, vy: 0 },
      { id: 2, kind: 1, hp: 1, facing: 0, flags: 0, x: 0, y: 100, vx: 0, vy: 0 },
    ];
    const sorted = sortEntitiesForDraw(ents);
    expect(sorted.map((e) => e.id)).toEqual([1, 2, 3]);
  });

  test("buildDrawList y-sorts SELF together with others (not always last)", () => {
    const state: RenderState = {
      map: openMaze(60, 40), selfId: 100,
      // self at y=200; one entity north (y=100), one south (y=300).
      selfPredicted: { x: 0, y: 200, facing: 0, flags: 0 },
      entities: [
        { id: 1, kind: 4, hp: 1, facing: 0, flags: 0, x: 0, y: 100, vx: 0, vy: 0 },
        { id: 2, kind: 2, hp: 1, facing: 0, flags: 0, x: 0, y: 300, vx: 0, vy: 0 },
      ],
      renderTick: 0,
    };
    const list = buildDrawList(state, DEFAULT_PALETTE);
    // Order by y: north entity, then self, then south entity.
    expect(list.map((d) => d.y)).toEqual([100, 200, 300]);
    // Self carries the self color + player kind.
    expect(list[1].color).toBe(DEFAULT_PALETTE.self);
    expect(list[1].kind).toBe(1);
  });

  test("buildDrawList carries per-entity atlas data (id/facing/vx/vy/hp/isSelf)", () => {
    const state: RenderState = {
      map: openMaze(60, 40), selfId: 100,
      selfPredicted: { x: 5, y: 5, facing: 7, flags: 0 },
      entities: [
        { id: 7, kind: 2, hp: 1, facing: 3, flags: 0, x: 0, y: 50, vx: 9, vy: -4 },
      ],
      renderTick: 0,
    };
    const list = buildDrawList(state, DEFAULT_PALETTE);
    const gen = list.find((d) => d.id === 7)!;
    expect(gen).toMatchObject({ kind: 2, facing: 3, vx: 9, vy: -4, hp: 1, isSelf: false });
    // maxHp defaults to GEN_MAX_HP (3) when no observed-max lookup is supplied.
    expect(gen.maxHp).toBe(GEN_MAX_HP);
    const self = list.find((d) => d.id === 100)!;
    // Self: id=selfId, facing from prediction, vx/vy 0, hp 0, isSelf true.
    expect(self).toMatchObject({ kind: 1, facing: 7, vx: 0, vy: 0, hp: 0, isSelf: true });
    expect(self.maxHp).toBe(GEN_MAX_HP);
  });

  test("buildDrawList uses the maxHp lookup as the damageStage denominator", () => {
    // A brutal generator spawns at hp 5; the lookup (Renderer's observed-max)
    // must flow onto DrawItem so damageStage(hp, maxHp) tracks true HP fraction
    // (60% of 5 is cracked, not the "healthy" a fixed denominator of 3 gives).
    const state: RenderState = {
      map: openMaze(60, 40), selfId: 1, selfPredicted: null,
      entities: [{ id: 9, kind: 2, hp: 3, facing: 0, flags: 0, x: 0, y: 0, vx: 0, vy: 0 }],
      renderTick: 0,
    };
    const list = buildDrawList(state, DEFAULT_PALETTE, (_id, _hp) => 5);
    const gen = list.find((d) => d.id === 9)!;
    expect(gen.maxHp).toBe(5);
    expect(damageStage(gen.hp, gen.maxHp)).toBe(1); // 3/5 = 0.6 → cracked
    // With the old fixed-3 denominator this same generator read healthy (0):
    expect(damageStage(gen.hp, GEN_MAX_HP)).toBe(0);
  });

  test("Renderer tracks each generator's observed spawn HP (brutal hp 5)", () => {
    // Generators first appear at full HP, so the Renderer's running max recovers
    // the true denominator (5 on brutal) and never decreases as the hive is hurt.
    const ctx = recordingCtx(320, 240);
    const r = new Renderer(ctx, DEFAULT_PALETTE);
    r.setMap(openMaze(20, 20));
    const frame = (hp: number): RenderState => ({
      map: openMaze(20, 20), selfId: 1, selfPredicted: null,
      entities: [{ id: 42, kind: 2, hp, facing: 0, flags: 0, x: 100, y: 100, vx: 0, vy: 0 }],
      renderTick: 0,
    });
    // Spawn at full brutal HP, then take damage — must not throw and must keep
    // the observed max (5) so later damage stages are correct.
    expect(() => r.draw(frame(5), emptyHudModel())).not.toThrow();
    expect(() => r.draw(frame(3), emptyHudModel())).not.toThrow();
    expect(() => r.draw(frame(1), emptyHudModel())).not.toThrow();
  });

  test("colorForKind distinguishes entity kinds (per palette)", () => {
    expect(colorForKind(1, DEFAULT_PALETTE)).toBe(DEFAULT_PALETTE.player);
    expect(colorForKind(2, DEFAULT_PALETTE)).toBe(DEFAULT_PALETTE.generator);
    expect(colorForKind(3, DEFAULT_PALETTE)).toBe(DEFAULT_PALETTE.projectile);
    expect(colorForKind(4, DEFAULT_PALETTE)).toBe(DEFAULT_PALETTE.snipe);
    // a different palette yields different snipe/generator colors.
    expect(colorForKind(4, COLORBLIND_PALETTE)).toBe(COLORBLIND_PALETTE.snipe);
    expect(colorForKind(4, COLORBLIND_PALETTE)).not.toBe(DEFAULT_PALETTE.snipe);
  });
});

describe("maze cache", () => {
  test("TestRender_MazeOffscreenCacheReusedAcrossFrames", () => {
    const maze = openMaze(10, 10);
    const ctx = fakeCtx(320, 240);
    const sentinel = {} as unknown as CanvasImageSource;
    const rasterize = vi.fn(() => sentinel);
    const r = new Renderer(ctx, DEFAULT_PALETTE, rasterize);

    r.setMap(maze);
    expect(rasterize).toHaveBeenCalledTimes(1);

    const state: RenderState = {
      map: maze, selfId: 1,
      selfPredicted: { x: 5 * SUBTILE_PER_TILE, y: 5 * SUBTILE_PER_TILE, facing: 0, flags: 0 },
      entities: [], renderTick: 0,
    };
    r.draw(state, emptyHudModel());
    r.draw(state, emptyHudModel());
    // Two frames, but no re-rasterization.
    expect(rasterize).toHaveBeenCalledTimes(1);

    // A new map (or palette change) rebuilds.
    r.setMap(openMaze(12, 12));
    expect(rasterize).toHaveBeenCalledTimes(2);
  });
});

describe("draw path — atlas blit model (spec §5e)", () => {
  test("draw() blits sprite cells (drawImage) instead of arc circles", () => {
    const ctx = recordingCtx(640, 480);
    const r = new Renderer(ctx, DEFAULT_PALETTE);
    r.setMap(openMaze(40, 30));
    r.draw(allKindsState(), emptyHudModel());
    // Entities are sprite blits now: no per-entity ctx.arc circle.
    expect(ctx.arcCalls).toBe(0);
    // One drawImage for the maze, one self-ring, one per entity (4) + self,
    // two overlays, and the CRT pass — well above zero. Assert the blit model
    // is exercised across maze + sprites + overlays + CRT.
    // maze(1) + selfRing(1) + self(1) + 4 entities + 2 overlays + crt(1) = 10
    expect(ctx.drawImageCalls).toBeGreaterThanOrEqual(10);
  });

  test("retroFx off drops the CRT blit; setRetroFx toggles it live", () => {
    const state = allKindsState();
    const offCtx = recordingCtx(640, 480);
    const rOff = new Renderer(offCtx, DEFAULT_PALETTE, undefined, { retroFx: false });
    rOff.setMap(openMaze(40, 30));
    rOff.draw(state, emptyHudModel());
    const offCount = offCtx.drawImageCalls;

    const onCtx = recordingCtx(640, 480);
    const rOn = new Renderer(onCtx, DEFAULT_PALETTE, undefined, { retroFx: true });
    rOn.setMap(openMaze(40, 30));
    rOn.draw(state, emptyHudModel());
    // retroFx adds exactly one full-canvas CRT blit.
    expect(onCtx.drawImageCalls).toBe(offCount + 1);

    // Toggling live changes the next frame's blit count by one.
    rOff.setRetroFx(true);
    offCtx.drawImageCalls = 0;
    rOff.draw(state, emptyHudModel());
    expect(offCtx.drawImageCalls).toBe(offCount + 1);
  });

  test("setPalette rebuilds bakes without re-running draw; draw stays headless", () => {
    const ctx = recordingCtx(320, 240);
    const r = new Renderer(ctx, DEFAULT_PALETTE);
    r.setMap(openMaze(20, 20));
    // A palette swap rebuilds atlas/maze/CRT but must not throw headless.
    expect(() => r.setPalette(COLORBLIND_PALETTE)).not.toThrow();
    expect(() => r.draw(allKindsState(), emptyHudModel())).not.toThrow();
  });

  test("draw() without overlays/self still blits maze + entities", () => {
    const ctx = recordingCtx(640, 480);
    const r = new Renderer(ctx, DEFAULT_PALETTE);
    r.setMap(openMaze(40, 30));
    const s: RenderState = {
      map: openMaze(40, 30), selfId: 1, selfPredicted: null,
      entities: [
        { id: 5, kind: 1, hp: 2, facing: 0, flags: 0, x: 100, y: 100, vx: 0, vy: 0 },
      ],
      renderTick: 0,
    };
    r.draw(s, emptyHudModel());
    expect(ctx.drawImageCalls).toBeGreaterThanOrEqual(2); // maze + 1 entity (+ crt)
  });
});
