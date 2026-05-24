// PHASE7.md §15.2 — DoD #8. Maze packing=1 unpacker parity with
// internal/sim/maze_pack.go (2 bits/tile, little-endian, row-major),
// plus dimension/overflow guards (codex iter-1 finding).

import { describe, expect, test } from "vitest";
import {
  unpackTiles, mazeViewFromMapInit, PACKING_2BIT,
  MIN_MAP_WIDTH, MIN_MAP_HEIGHT,
} from "../src/maze.js";
import { TileCode } from "../src/sim.js";
import type { MapInit } from "../src/proto.js";

// Reference packer mirroring internal/sim/maze_pack.go::packTiles.
function pack(tiles: number[]): Uint8Array {
  const out = new Uint8Array((tiles.length * 2 + 7) >> 3);
  for (let i = 0; i < tiles.length; i++) {
    const v = tiles[i] & 0x3;
    out[(i * 2) >> 3] |= v << ((i * 2) & 7);
  }
  return out;
}

describe("maze unpack (packing=1)", () => {
  test("TestMaze_UnpackPacking1 round-trips a known grid", () => {
    // 8 tiles, all 4 codes across a byte boundary.
    const grid = [
      TileCode.Wall, TileCode.Floor, TileCode.SpawnPlayer, TileCode.SpawnGenerator,
      TileCode.SpawnGenerator, TileCode.SpawnPlayer, TileCode.Floor, TileCode.Wall,
    ];
    const packed = pack(grid);
    expect(packed.length).toBe(2);
    // byte0 = W(0)|F(1<<2)|SP(2<<4)|SG(3<<6).
    expect(packed[0]).toBe(0b11_10_01_00);
    expect(Array.from(unpackTiles(packed, 4, 2))).toEqual(grid);
  });

  test("decodes a non-multiple-of-4 tile count (final-byte padding)", () => {
    const grid = [TileCode.Wall, TileCode.Floor, TileCode.SpawnPlayer]; // 3 tiles → 1 byte
    const packed = pack(grid);
    expect(packed.length).toBe(1);
    expect(Array.from(unpackTiles(packed, 3, 1))).toEqual(grid);
  });

  test("rejects a short buffer", () => {
    // 60x40 needs ceil(2400/4)=600 bytes; give 599.
    expect(() => unpackTiles(new Uint8Array(599), 60, 40)).toThrow(/too short/);
  });

  test("rejects non-positive and over-large dimensions (overflow guard)", () => {
    expect(() => unpackTiles(new Uint8Array(0), 0, 10)).toThrow(/bad dimensions/);
    expect(() => unpackTiles(new Uint8Array(0), -1, 10)).toThrow(/bad dimensions/);
    // The codex overflow case: a hostile 32768x32768 must NOT pass length
    // validation via signed-shift overflow and must NOT allocate.
    expect(() => unpackTiles(new Uint8Array(0), 32768, 32768)).toThrow(/exceed max/);
  });

  test("mazeViewFromMapInit unpacks a valid-size map and clamps OOB to Wall", () => {
    const w = MIN_MAP_WIDTH, h = MIN_MAP_HEIGHT; // 30x20, smallest legal map
    const grid = new Array(w * h).fill(TileCode.Floor);
    grid[0] = TileCode.SpawnPlayer;          // (0,0)
    grid[(h - 1) * w + (w - 1)] = TileCode.Wall; // (w-1,h-1)
    const m: MapInit = { seed: 1, width: w, height: h, packing: PACKING_2BIT, packedTiles: pack(grid) };
    const view = mazeViewFromMapInit(m);
    expect(view.W).toBe(w);
    expect(view.H).toBe(h);
    expect(view.at(0, 0)).toBe(TileCode.SpawnPlayer);
    expect(view.at(w - 1, h - 1)).toBe(TileCode.Wall);
    expect(view.at(1, 1)).toBe(TileCode.Floor);
    // OOB clamps to Wall.
    expect(view.at(-1, 0)).toBe(TileCode.Wall);
    expect(view.at(w, 0)).toBe(TileCode.Wall);
    expect(view.at(0, h)).toBe(TileCode.Wall);
  });

  test("mazeViewFromMapInit rejects dimensions outside protocol limits", () => {
    const small: MapInit = { seed: 1, width: 2, height: 2, packing: PACKING_2BIT, packedTiles: new Uint8Array(1) };
    expect(() => mazeViewFromMapInit(small)).toThrow(/outside/);
    const big: MapInit = { seed: 1, width: 200, height: 200, packing: PACKING_2BIT, packedTiles: new Uint8Array(0) };
    expect(() => mazeViewFromMapInit(big)).toThrow(/outside/);
  });

  test("mazeViewFromMapInit rejects unsupported packing", () => {
    const m: MapInit = { seed: 1, width: 30, height: 20, packing: 2, packedTiles: new Uint8Array(1) };
    expect(() => mazeViewFromMapInit(m)).toThrow(/unsupported packing/);
  });
});
