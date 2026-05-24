// PHASE7.md §15.1 — DoD #5/#6/#7. Camera-follow, maze-cache reuse, and
// y-sort. Pure geometry is tested directly; the cache-reuse test uses a
// recording fake ctx + a spy rasterizer (no real canvas needed).

import { describe, expect, test, vi } from "vitest";
import {
  computeCamera, worldToScreen, sortEntitiesForDraw, buildDrawList, colorForKind, Renderer,
  TILE_PX, PX_PER_SUBTILE, type RenderCtx, type RenderState,
} from "../src/render.js";
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
