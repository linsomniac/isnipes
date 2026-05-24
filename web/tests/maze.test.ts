// PHASE7.md §15.2 — DoD #8. Maze packing=1 unpacker parity with
// internal/sim/maze_pack.go (2 bits/tile, little-endian, row-major).

import { describe, expect, test } from "vitest";
import { unpackTiles, mazeViewFromMapInit, PACKING_2BIT } from "../src/maze.js";
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
    // 4x2 grid, all 4 tile codes exercised across byte boundaries.
    const grid = [
      TileCode.Wall, TileCode.Floor, TileCode.SpawnPlayer, TileCode.SpawnGenerator,
      TileCode.SpawnGenerator, TileCode.SpawnPlayer, TileCode.Floor, TileCode.Wall,
    ];
    const packed = pack(grid);
    // 8 tiles * 2 bits = 16 bits = 2 bytes.
    expect(packed.length).toBe(2);
    // byte0 = W(0)|F(1<<2)|SP(2<<4)|SG(3<<6) = 0 | 4 | 32 | 192 = 228.
    expect(packed[0]).toBe(0b11_10_01_00);
    const out = unpackTiles(packed, 4, 2);
    expect(Array.from(out)).toEqual(grid);
  });

  test("rejects a short buffer", () => {
    // 60x40 needs ceil(2400*2/8)=600 bytes; give it 599.
    expect(() => unpackTiles(new Uint8Array(599), 60, 40)).toThrow(/too short/);
  });

  test("mazeViewFromMapInit clamps OOB reads to Wall", () => {
    const grid = [TileCode.Floor, TileCode.Floor, TileCode.Floor, TileCode.Floor];
    const m: MapInit = {
      seed: 1, width: 2, height: 2, packing: PACKING_2BIT, packedTiles: pack(grid),
    };
    const view = mazeViewFromMapInit(m);
    expect(view.at(0, 0)).toBe(TileCode.Floor);
    expect(view.at(1, 1)).toBe(TileCode.Floor);
    expect(view.at(-1, 0)).toBe(TileCode.Wall);
    expect(view.at(2, 0)).toBe(TileCode.Wall);
    expect(view.at(0, 2)).toBe(TileCode.Wall);
  });

  test("mazeViewFromMapInit rejects unsupported packing", () => {
    const m: MapInit = {
      seed: 1, width: 2, height: 2, packing: 2, packedTiles: new Uint8Array(1),
    };
    expect(() => mazeViewFromMapInit(m)).toThrow(/unsupported packing/);
  });
});
