// PHASE4.md §10 — TypeScript mirror of the player-only physics path
// from internal/sim/physics.go::moveAndSlide. Used by prediction.ts
// to compute the local player's next-tick state without round-
// tripping the server.
//
// Parity contract: the constants below are byte-identical with
// internal/sim/config.go. testdata/sim/physics_constants.txt is the
// oracle; both sides parity-test against it.

export const SUBTILE_PER_TILE = 256;
export const PLAYER_SPEED = 32;
export const PLAYER_TURBO_SPEED = 64;
export const PLAYER_HALF_EXT = 256;
export const FIRE_COOLDOWN_TICKS = 6;
export const PROJECTILE_SPEED = 64;
export const PROJECTILE_LIFETIME = 90;
export const GENERATOR_HALF_EXT = 256;
export const PROJECTILE_HALF_EXT = 48;

// Dir8 enum mirrors §4.3.2 of SPEC.md.
export const Dir = {
  Idle: 0,
  N: 1,
  NE: 2,
  E: 3,
  SE: 4,
  S: 5,
  SW: 6,
  W: 7,
  NW: 8,
} as const;
export type Dir = (typeof Dir)[keyof typeof Dir];

export const TileCode = {
  Wall: 0,
  Floor: 1,
  SpawnPlayer: 2,
  SpawnGenerator: 3,
} as const;
export type TileCode = (typeof TileCode)[keyof typeof TileCode];

export interface MazeView {
  W: number;
  H: number;
  at(tx: number, ty: number): TileCode;
}

export interface PlayerKinematic {
  x: number; // subtile coords
  y: number;
  vx: number;
  vy: number;
  halfExt: number;
  // Phase 4 codex sweep #3: mirror internal/sim/playerState.lastDir
  // so turbo locks direction even when the input dir changes
  // mid-hold. lastDir == Dir.Idle means "turbo not engaged".
  lastDir?: Dir;
}

export interface SolidAABB {
  cx: number;
  cy: number;
  halfExt: number;
}

// diag mirrors physics.go::diag — hard-coded √2/2 ≈ 181/256.
export function diag(s: number): number {
  // Match Go integer division (truncation toward zero).
  const product = s * 181;
  return product >= 0 ? Math.floor(product / 256) : -Math.floor(-product / 256);
}

// velocityFor mirrors physics.go::velocityFor.
export function velocityFor(d: Dir, speed: number): [number, number] {
  switch (d) {
    case Dir.N: return [0, -speed];
    case Dir.NE: return [diag(speed), -diag(speed)];
    case Dir.E: return [speed, 0];
    case Dir.SE: return [diag(speed), diag(speed)];
    case Dir.S: return [0, speed];
    case Dir.SW: return [-diag(speed), diag(speed)];
    case Dir.W: return [-speed, 0];
    case Dir.NW: return [-diag(speed), -diag(speed)];
    default: return [0, 0];
  }
}

// Go integer division (truncation toward zero) for the tile-index
// math. JS's `Math.floor` always rounds toward -∞, which gives
// different results for negative values.
function div(a: number, b: number): number {
  const r = a / b;
  return r >= 0 ? Math.floor(r) : Math.ceil(r);
}

function overlaps(ax: number, ay: number, ahe: number, bx: number, by: number, bhe: number): boolean {
  if (ax + ahe <= bx - bhe) return false;
  if (bx + bhe <= ax - ahe) return false;
  if (ay + ahe <= by - bhe) return false;
  if (by + bhe <= ay - ahe) return false;
  return true;
}

// moveAndSlide is the TS mirror of physics.go::moveAndSlide for the
// player kind (halfExt = PLAYER_HALF_EXT). The algorithm is
// axis-separated swept-AABB: X first (wall sweep then solid clamp),
// then Y. Returns the new (x, y, vx, vy).
export function moveAndSlide(
  maze: MazeView,
  x: number, y: number, vx: number, vy: number,
  halfExt: number,
  solids: ReadonlyArray<SolidAABB>,
): { x: number; y: number; vx: number; vy: number } {
  // X axis.
  if (vx > 0) {
    const leadX = x + halfExt + vx;
    const col = div(leadX, SUBTILE_PER_TILE);
    const rowLo = div(y - halfExt, SUBTILE_PER_TILE);
    const rowHi = div(y + halfExt - 1, SUBTILE_PER_TILE);
    let hit = false;
    for (let r = rowLo; r <= rowHi; r++) {
      if (maze.at(col, r) === TileCode.Wall) {
        hit = true;
        break;
      }
    }
    if (hit) {
      x = col * SUBTILE_PER_TILE - halfExt - 1;
      vx = 0;
    } else {
      x += vx;
    }
    for (const s of solids) {
      if (!overlaps(x, y, halfExt, s.cx, s.cy, s.halfExt)) continue;
      x = s.cx - s.halfExt - halfExt - 1;
      vx = 0;
    }
  } else if (vx < 0) {
    const leadX = x - halfExt + vx;
    const col = div(leadX, SUBTILE_PER_TILE);
    const rowLo = div(y - halfExt, SUBTILE_PER_TILE);
    const rowHi = div(y + halfExt - 1, SUBTILE_PER_TILE);
    let hit = false;
    for (let r = rowLo; r <= rowHi; r++) {
      if (maze.at(col, r) === TileCode.Wall) {
        hit = true;
        break;
      }
    }
    if (hit) {
      x = (col + 1) * SUBTILE_PER_TILE + halfExt;
      vx = 0;
    } else {
      x += vx;
    }
    for (const s of solids) {
      if (!overlaps(x, y, halfExt, s.cx, s.cy, s.halfExt)) continue;
      x = s.cx + s.halfExt + halfExt + 1;
      vx = 0;
    }
  }
  // Y axis.
  if (vy > 0) {
    const leadY = y + halfExt + vy;
    const row = div(leadY, SUBTILE_PER_TILE);
    const colLo = div(x - halfExt, SUBTILE_PER_TILE);
    const colHi = div(x + halfExt - 1, SUBTILE_PER_TILE);
    let hit = false;
    for (let c = colLo; c <= colHi; c++) {
      if (maze.at(c, row) === TileCode.Wall) {
        hit = true;
        break;
      }
    }
    if (hit) {
      y = row * SUBTILE_PER_TILE - halfExt - 1;
      vy = 0;
    } else {
      y += vy;
    }
    for (const s of solids) {
      if (!overlaps(x, y, halfExt, s.cx, s.cy, s.halfExt)) continue;
      y = s.cy - s.halfExt - halfExt - 1;
      vy = 0;
    }
  } else if (vy < 0) {
    const leadY = y - halfExt + vy;
    const row = div(leadY, SUBTILE_PER_TILE);
    const colLo = div(x - halfExt, SUBTILE_PER_TILE);
    const colHi = div(x + halfExt - 1, SUBTILE_PER_TILE);
    let hit = false;
    for (let c = colLo; c <= colHi; c++) {
      if (maze.at(c, row) === TileCode.Wall) {
        hit = true;
        break;
      }
    }
    if (hit) {
      y = (row + 1) * SUBTILE_PER_TILE + halfExt;
      vy = 0;
    } else {
      y += vy;
    }
    for (const s of solids) {
      if (!overlaps(x, y, halfExt, s.cx, s.cy, s.halfExt)) continue;
      y = s.cy + s.halfExt + halfExt + 1;
      vy = 0;
    }
  }
  return { x, y, vx, vy };
}

export interface ClientInputIntent {
  dir: Dir;
  turbo: boolean;
}

// stepPlayer applies one tick of input to a local player and returns
// the next kinematic. Pure — no side effects, no PRNG.
//
// Mirrors internal/sim/sim.go turbo-lock semantics (§10.6 of SPEC):
// while turbo is held, the direction locks to the first dir held;
// changing dir while turbo is held does NOT change direction.
// Turbo with Dir.Idle cancels the lock (§10.6 "turbo-cancel-via-idle").
export function stepPlayer(
  prev: PlayerKinematic,
  inp: ClientInputIntent,
  maze: MazeView,
  solids: ReadonlyArray<SolidAABB>,
): PlayerKinematic {
  const prevLock = prev.lastDir ?? Dir.Idle;
  let effectiveDir: Dir = inp.dir;
  let nextLock: Dir = Dir.Idle;
  if (inp.turbo && inp.dir !== Dir.Idle) {
    if (prevLock === Dir.Idle) {
      // First tick of turbo — lock to this dir.
      effectiveDir = inp.dir;
      nextLock = inp.dir;
    } else {
      // Already locked — preserve the locked dir regardless of input.
      effectiveDir = prevLock;
      nextLock = prevLock;
    }
  } else if (inp.turbo && inp.dir === Dir.Idle) {
    // Turbo + idle cancels the lock.
    effectiveDir = Dir.Idle;
    nextLock = Dir.Idle;
  } else {
    // Turbo released.
    effectiveDir = inp.dir;
    nextLock = Dir.Idle;
  }
  const speed = inp.turbo && effectiveDir !== Dir.Idle ? PLAYER_TURBO_SPEED : PLAYER_SPEED;
  const [vx, vy] = velocityFor(effectiveDir, speed);
  if (vx === 0 && vy === 0) {
    return { x: prev.x, y: prev.y, vx: 0, vy: 0, halfExt: prev.halfExt, lastDir: nextLock };
  }
  const r = moveAndSlide(maze, prev.x, prev.y, vx, vy, prev.halfExt, solids);
  return { x: r.x, y: r.y, vx: r.vx, vy: r.vy, halfExt: prev.halfExt, lastDir: nextLock };
}
