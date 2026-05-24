// PHASE7.md §6.5 — decodes the MapInit `packing = 1` tile format
// (2 bits per tile, little-endian within each byte, row-major) into a
// flat tile-code array and a MazeView. Mirrors
// internal/sim/maze_pack.go::packTiles exactly. proto.ts::decodeMapInit
// returns the raw packed bytes; this module does the bit unpacking.

import type { MapInit } from "./proto.js";
import { type MazeView, type TileCode, TileCode as TC } from "./sim.js";

export const PACKING_2BIT = 1;

// unpackTiles decodes `packed` (packing=1) into a flat Uint8Array of
// W*H tile codes in row-major order. Throws if `packed` is too short to
// hold W*H 2-bit tiles.
export function unpackTiles(packed: Uint8Array, w: number, h: number): Uint8Array {
  const n = w * h;
  const need = (n * 2 + 7) >> 3;
  if (packed.length < need) {
    throw new Error(`maze: packed too short: have ${packed.length}, need ${need} for ${w}x${h}`);
  }
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    const byteIdx = (i * 2) >> 3;
    const bitIdx = (i * 2) & 7;
    out[i] = (packed[byteIdx] >> bitIdx) & 0x3;
  }
  return out;
}

// mazeViewFromMapInit unpacks a MapInit into a MazeView. Out-of-bounds
// reads clamp to Wall so the renderer/physics never index past the grid.
export function mazeViewFromMapInit(m: MapInit): MazeView {
  if (m.packing !== PACKING_2BIT) {
    throw new Error(`maze: unsupported packing ${m.packing} (only ${PACKING_2BIT} in v1)`);
  }
  const w = m.width;
  const h = m.height;
  const tiles = unpackTiles(m.packedTiles, w, h);
  return {
    W: w,
    H: h,
    at(tx: number, ty: number): TileCode {
      if (tx < 0 || ty < 0 || tx >= w || ty >= h) return TC.Wall;
      return tiles[ty * w + tx] as TileCode;
    },
  };
}
