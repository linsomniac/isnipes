// PHASE4.md §18.8 — prediction ring buffer + reconcile tests.

import { describe, expect, test } from "vitest";
import {
  Dir,
  SUBTILE_PER_TILE,
  PLAYER_HALF_EXT,
  TileCode,
  type MazeView,
  stepPlayer,
} from "../src/sim.js";
import {
  PredictionBuffer,
  makeInitialPlayer,
  DIVERGE_THRESHOLD,
} from "../src/prediction.js";

// allFloorMaze: 60×40 with outer wall.
function allFloorMaze(): MazeView {
  const W = 60;
  const H = 40;
  const tiles = new Uint8Array(W * H);
  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      if (x === 0 || y === 0 || x === W - 1 || y === H - 1) {
        tiles[y * W + x] = TileCode.Wall;
      } else {
        tiles[y * W + x] = TileCode.Floor;
      }
    }
  }
  return {
    W,
    H,
    at(tx, ty) {
      if (tx < 0 || ty < 0 || tx >= W || ty >= H) return TileCode.Wall;
      return tiles[ty * W + tx] as TileCode;
    },
  };
}

function tileCentre(tx: number, ty: number): { x: number; y: number } {
  return {
    x: tx * SUBTILE_PER_TILE + SUBTILE_PER_TILE / 2,
    y: ty * SUBTILE_PER_TILE + SUBTILE_PER_TILE / 2,
  };
}

describe("PredictionBuffer basic", () => {
  test("empty ring → predicted() returns spawn state", () => {
    const c = tileCentre(10, 10);
    const init = makeInitialPlayer(c.x, c.y);
    const p = new PredictionBuffer(init);
    expect(p.size()).toBe(0);
    expect(p.predicted()).toEqual({ ...init });
  });

  test("push grows the ring up to capacity", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    for (let i = 0; i < 50; i++) {
      p.step(
        { dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i },
        maze,
        [],
      );
    }
    expect(p.size()).toBeLessThanOrEqual(32);
    expect(p.size()).toBeGreaterThanOrEqual(30);
  });

  test("predicted() after east-moves advances x", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    for (let i = 0; i < 5; i++) {
      p.step(
        { dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i },
        maze,
        [],
      );
    }
    const end = p.predicted();
    expect(end.x).toBeGreaterThan(c.x);
    expect(end.y).toBe(c.y);
  });
});

describe("reconcile-on-snapshot", () => {
  test("no divergence: replay count = 0", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    // Issue 5 inputs.
    for (let i = 0; i < 5; i++) {
      p.step({ dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i }, maze, []);
    }
    // Server confirms input #2 with the EXACT predicted state.
    const buffered = p["buf"][2]; // private peek for the test
    const r = p.reconcile(
      buffered.clientTick,
      buffered.state,
      maze,
      [],
    );
    expect(r.diverged).toBe(0);
    expect(r.replayed).toBe(0);
  });

  test("divergence within threshold: no replay", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    for (let i = 0; i < 5; i++) {
      p.step({ dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i }, maze, []);
    }
    const buffered = p["buf"][2];
    const r = p.reconcile(
      buffered.clientTick,
      { ...buffered.state, x: buffered.state.x + DIVERGE_THRESHOLD },
      maze,
      [],
    );
    expect(r.diverged).toBe(0);
  });

  test("divergence past threshold triggers replay", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    for (let i = 0; i < 5; i++) {
      p.step({ dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i }, maze, []);
    }
    const buffered = p["buf"][2];
    // Server reports tick 2's position diverged far on Y.
    const r = p.reconcile(
      buffered.clientTick,
      { ...buffered.state, y: buffered.state.y + DIVERGE_THRESHOLD * 10 },
      maze,
      [],
    );
    expect(r.diverged).toBe(1);
    expect(r.replayed).toBe(2); // ticks 3 and 4 replayed
    // After replay, ring entry 2's state matches server's y; entries
    // 3 and 4 carry the new lineage.
    const afterReplay = p["buf"][4];
    expect(afterReplay.state.y).toBe(buffered.state.y + DIVERGE_THRESHOLD * 10);
  });

  test("missing buffered tick → hard reset to server state", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    for (let i = 0; i < 5; i++) {
      p.step({ dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i }, maze, []);
    }
    // Reconcile against a tick we never buffered (-100 modular).
    const server = { x: 9999, y: 8888, vx: 0, vy: 0, halfExt: PLAYER_HALF_EXT };
    const r = p.reconcile(0xffff, server, maze, []);
    expect(r.replayed).toBe(0);
    expect(p.size()).toBe(0);
    expect(p.predicted()).toEqual(server);
  });

  test("PredictionConvergesUnderLatency (DoD #7)", () => {
    // Synthetic 100ms one-way delay: snapshots are emitted every 2
    // ticks (15 Hz) but the client lags 3 ticks behind the server.
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    // Phase A: prime the buffer with E movement; record the
    // authoritative state every 2 ticks.
    const snapshotQueue: Array<{ tick: number; state: any }> = [];
    let authoritative = makeInitialPlayer(c.x, c.y);
    for (let i = 0; i < 30; i++) {
      const inp = { dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: i };
      p.step(inp, maze, []);
      // Server applies the same input and updates authoritative.
      authoritative = stepPlayer(authoritative, inp, maze, []);
      if (i % 2 === 0) {
        snapshotQueue.push({ tick: i, state: { ...authoritative } });
      }
    }
    // Phase B: snapshots arrive 3 ticks late and trigger reconcile.
    let lastDiverge = 0;
    for (const snap of snapshotQueue) {
      const r = p.reconcile(snap.tick, snap.state, maze, []);
      if (r.diverged) lastDiverge++;
    }
    // No divergence: client predicted EXACTLY what the server did
    // (since the maze and inputs match deterministically). Replay
    // count should remain 0 across all snapshots.
    expect(lastDiverge).toBe(0);
    const final = p.predicted();
    // Final position is within DIVERGE_THRESHOLD of authoritative.
    expect(Math.abs(final.x - authoritative.x)).toBeLessThanOrEqual(DIVERGE_THRESHOLD);
    expect(Math.abs(final.y - authoritative.y)).toBeLessThanOrEqual(DIVERGE_THRESHOLD);
  });

  test("clientTick wraparound is handled by modular comparator", () => {
    const c = tileCentre(10, 10);
    const p = new PredictionBuffer(makeInitialPlayer(c.x, c.y));
    const maze = allFloorMaze();
    // Issue inputs straddling the 0xFFFF → 0 wrap.
    const starts = [0xfffd, 0xfffe, 0xffff, 0x0000, 0x0001];
    for (const ct of starts) {
      p.step({ dir: Dir.E, turbo: false, fireDir: Dir.Idle, clientTick: ct }, maze, []);
    }
    // Reconcile against tick 0xFFFE — both tick 0xFFFF and 0,1
    // should be considered "after" via modular16Diff.
    const buffered = p["buf"][1]; // index 1 == clientTick 0xfffe
    expect(buffered.clientTick).toBe(0xfffe);
    const r = p.reconcile(
      buffered.clientTick,
      { ...buffered.state, x: buffered.state.x + 999 },
      maze,
      [],
    );
    expect(r.diverged).toBe(1);
    // 3 entries after 0xfffe in the ring: 0xffff, 0x0000, 0x0001.
    expect(r.replayed).toBe(3);
  });
});

describe("stepPlayer deterministic mirror", () => {
  test("idle dir yields zero velocity and no movement", () => {
    const c = tileCentre(10, 10);
    const init = makeInitialPlayer(c.x, c.y);
    const out = stepPlayer(init, { dir: Dir.Idle, turbo: false }, allFloorMaze(), []);
    expect(out.x).toBe(init.x);
    expect(out.y).toBe(init.y);
    expect(out.vx).toBe(0);
    expect(out.vy).toBe(0);
  });

  test("east advance at non-turbo speed", () => {
    const c = tileCentre(10, 10);
    const init = makeInitialPlayer(c.x, c.y);
    const out = stepPlayer(init, { dir: Dir.E, turbo: false }, allFloorMaze(), []);
    expect(out.x).toBe(c.x + 16);
    expect(out.y).toBe(c.y);
  });

  test("turbo east doubles speed", () => {
    const c = tileCentre(10, 10);
    const init = makeInitialPlayer(c.x, c.y);
    const out = stepPlayer(init, { dir: Dir.E, turbo: true }, allFloorMaze(), []);
    expect(out.x).toBe(c.x + 32);
  });

  test("turbo locks direction (codex sweep #3)", () => {
    // Mirror Go's internal/sim/sim.go::TestTurboStrictLock contract.
    const c = tileCentre(10, 10);
    let s = { x: c.x, y: c.y, vx: 0, vy: 0, halfExt: PLAYER_HALF_EXT, lastDir: 0 as any };
    // Tick 1: turbo + E. Now locked to E.
    s = stepPlayer(s, { dir: Dir.E, turbo: true }, allFloorMaze(), []);
    expect(s.lastDir).toBe(Dir.E);
    const afterE = s.x;
    // Tick 2: turbo + W. Should STILL move east because lock holds.
    s = stepPlayer(s, { dir: Dir.W, turbo: true }, allFloorMaze(), []);
    expect(s.x).toBeGreaterThan(afterE);
    expect(s.lastDir).toBe(Dir.E);
    // Tick 3: release turbo + N. Lock cleared; moves N.
    const beforeN = s.y;
    s = stepPlayer(s, { dir: Dir.N, turbo: false }, allFloorMaze(), []);
    expect(s.y).toBeLessThan(beforeN);
    expect(s.lastDir).toBe(0);
    // Tick 4: turbo + Idle cancels the lock without moving.
    s = stepPlayer(s, { dir: Dir.Idle, turbo: true }, allFloorMaze(), []);
    expect(s.lastDir).toBe(0);
  });

  test("generator solid blocks east movement", () => {
    const c = tileCentre(10, 10);
    const init = makeInitialPlayer(c.x, c.y);
    const gx = c.x + 100; // very close
    const solids = [{ cx: gx, cy: c.y, halfExt: 112 }];
    const out = stepPlayer(init, { dir: Dir.E, turbo: false }, allFloorMaze(), solids);
    // Clamped to s.cx - s.halfExt - playerHalfExt - 1
    expect(out.x).toBe(gx - 112 - PLAYER_HALF_EXT - 1);
    expect(out.vx).toBe(0);
  });
});
