// PHASE7.md §6.5 — decodes the MapInit `packing = 1` tile format
// (2 bits per tile, little-endian within each byte, row-major) into a
// flat tile-code array and a MazeView. Mirrors
// internal/sim/maze_pack.go::packTiles exactly. proto.ts::decodeMapInit
// returns the raw packed bytes; this module does the bit unpacking.

import type { MapInit } from "./proto.js";
import { type MazeView, type TileCode, TileCode as TC } from "./sim.js";

export const PACKING_2BIT = 1;

// Maze dimension bounds — mirror internal/sim/config.go (minMapWidth..
// maxMapWidth / minMapHeight..maxMapHeight). MapInit carries width/height
// as unrestricted u16; a hostile frame could otherwise force a huge
// allocation, so we reject out-of-range dimensions before allocating.
export const MIN_MAP_WIDTH = 50;
export const MAX_MAP_WIDTH = 120;
export const MIN_MAP_HEIGHT = 40;
export const MAX_MAP_HEIGHT = 80;

// unpackTiles decodes `packed` (packing=1) into a flat Uint8Array of
// W*H tile codes in row-major order. Rejects non-positive or
// over-large dimensions (overflow/DoS guard) and a too-short buffer.
// Arithmetic is non-bitwise so it stays correct past the 2^31 bit-shift
// overflow boundary.
export function unpackTiles(packed: Uint8Array, w: number, h: number): Uint8Array {
  if (!Number.isInteger(w) || !Number.isInteger(h) || w <= 0 || h <= 0) {
    throw new Error(`maze: bad dimensions ${w}x${h}`);
  }
  if (w > MAX_MAP_WIDTH || h > MAX_MAP_HEIGHT) {
    throw new Error(`maze: dimensions ${w}x${h} exceed max ${MAX_MAP_WIDTH}x${MAX_MAP_HEIGHT}`);
  }
  const n = w * h;
  const need = Math.ceil(n / 4); // 4 tiles per byte (2 bits each)
  if (packed.length < need) {
    throw new Error(`maze: packed too short: have ${packed.length}, need ${need} for ${w}x${h}`);
  }
  const out = new Uint8Array(n);
  for (let i = 0; i < n; i++) {
    const byteIdx = Math.floor(i / 4);
    const bitIdx = (i % 4) * 2;
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
  if (w < MIN_MAP_WIDTH || w > MAX_MAP_WIDTH || h < MIN_MAP_HEIGHT || h > MAX_MAP_HEIGHT) {
    throw new Error(
      `maze: dimensions ${w}x${h} outside [${MIN_MAP_WIDTH}..${MAX_MAP_WIDTH}]x[${MIN_MAP_HEIGHT}..${MAX_MAP_HEIGHT}]`,
    );
  }
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
