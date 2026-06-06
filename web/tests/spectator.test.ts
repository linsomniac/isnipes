// Spectator camera (web/src/spectator.ts). Pure unit tests: synthetic frame
// deltas + entity lists drive the camera so there is no clock/display
// dependence. Spec: docs/superpowers/specs/2026-06-05-spectator-scroll-design.md.

import { describe, expect, test } from "vitest";
import { Dir, velocityFor, PLAYER_TURBO_SPEED } from "../src/sim.js";
import {
  SpectatorCamera, PAN_SPEED, PAN_TURBO_MULT, MAX_DT_MS,
  type SpectatorInput,
} from "../src/spectator.js";

// A world big enough that pan tests never hit the clamp.
const BIG = { worldW: 1_000_000, worldH: 1_000_000 };

// inp() builds a SpectatorInput with sensible defaults; override per test.
function inp(over: Partial<SpectatorInput> = {}): SpectatorInput {
  return {
    dtMs: 16, panDir: Dir.Idle, turbo: false, cycleEdge: false,
    players: [], bounds: BIG, ...over,
  };
}

describe("PAN_SPEED", () => {
  test("is player-turbo speed scaled to per-second (30Hz)", () => {
    expect(PAN_SPEED).toBe(PLAYER_TURBO_SPEED * 30); // 6720 subtiles/s
  });
});

describe("free-pan", () => {
  test("start() seeds free mode at the death spot", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 42, y: 84 });
    const out = cam.update(inp({ dtMs: 0 }));
    expect(out.mode).toBe("free");
    expect(out.center).toEqual({ x: 42, y: 84 });
    expect(out.followId).toBeNull();
  });

  test("integrates position by dt in the pan direction (East)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.E }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * 0.1); // 1672
    expect(out.center.y).toBeCloseTo(1000);
    expect(out.mode).toBe("free");
  });

  test("diagonals use velocityFor (normalized)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 5000, y: 5000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.NE }));
    const [vx, vy] = velocityFor(Dir.NE, PAN_SPEED);
    expect(out.center.x).toBeCloseTo(5000 + vx * 0.1);
    expect(out.center.y).toBeCloseTo(5000 + vy * 0.1);
  });

  test("turbo multiplies pan speed by PAN_TURBO_MULT", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.E, turbo: true }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * PAN_TURBO_MULT * 0.1);
  });

  test("clamps dt at MAX_DT_MS so a tab-out can't fling the camera", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 1000, y: 1000 });
    const out = cam.update(inp({ dtMs: 100_000, panDir: Dir.E }));
    expect(out.center.x).toBeCloseTo(1000 + PAN_SPEED * (MAX_DT_MS / 1000));
  });

  test("clamps centre to [0, world] on the left/top edge", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 10, y: 10 });
    const out = cam.update(inp({ dtMs: 100, panDir: Dir.NW, turbo: true }));
    expect(out.center.x).toBe(0);
    expect(out.center.y).toBe(0);
  });

  test("clamps centre to [0, world] on the right/bottom edge", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 90, y: 90 });
    const out = cam.update(inp({
      dtMs: 100, panDir: Dir.SE, turbo: true, bounds: { worldW: 100, worldH: 100 },
    }));
    expect(out.center.x).toBe(100);
    expect(out.center.y).toBe(100);
  });
});

describe("follow cycling", () => {
  const players = [
    { id: 5, x: 1, y: 1 },
    { id: 2, x: 2, y: 2 },
    { id: 9, x: 3, y: 3 },
  ];

  test("cycleEdge follows the next player id-ascending, wrapping", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    let out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.mode).toBe("follow");
    expect(out.followId).toBe(2);
    expect(out.center).toEqual({ x: 2, y: 2 });
    out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.followId).toBe(5);
    expect(out.center).toEqual({ x: 1, y: 1 });
    out = cam.update(inp({ cycleEdge: true, players }));
    expect(out.followId).toBe(9);
    out = cam.update(inp({ cycleEdge: true, players })); // wrap
    expect(out.followId).toBe(2);
  });

  test("cycleEdge on an empty player list is a no-op (stays free)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 7, y: 7 });
    const out = cam.update(inp({ cycleEdge: true, players: [] }));
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 7, y: 7 });
  });

  test("a same-frame Tab + movement key nets to free (move-release wins)", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    // cycleEdge engages follow (step 1), but the same-frame held movement key
    // releases it back to free (step 2). Documented intentional ordering: Tab
    // only engages follow when no movement key is held. dtMs:0 → no pan drift.
    const out = cam.update(inp({ dtMs: 0, cycleEdge: true, panDir: Dir.E, players: [{ id: 1, x: 9, y: 9 }] }));
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 0, y: 0 });
  });
});

describe("follow tracking", () => {
  test("follow tracks a moving target across frames", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 100, y: 100 }] }));
    const out = cam.update(inp({ players: [{ id: 1, x: 150, y: 160 }] }));
    expect(out.mode).toBe("follow");
    expect(out.center).toEqual({ x: 150, y: 160 });
  });

  test("auto-advances when the followed target leaves the list", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 10, y: 10 }, { id: 3, x: 30, y: 30 }] }));
    const out = cam.update(inp({ players: [{ id: 3, x: 35, y: 35 }] })); // id 1 gone
    expect(out.mode).toBe("follow");
    expect(out.followId).toBe(3);
    expect(out.center).toEqual({ x: 35, y: 35 });
  });

  test("drops to free (holding centre) when no players remain", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 35, y: 35 }] }));
    const out = cam.update(inp({ players: [] })); // none left
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 35, y: 35 }); // last centre held
  });

  test("a non-idle panDir releases follow → free at the current centre", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 500, y: 500 }] }));
    // dtMs:0 so the released frame does not also pan — assert it released in place.
    const out = cam.update(inp({ dtMs: 0, panDir: Dir.E, players: [{ id: 1, x: 500, y: 500 }] }));
    expect(out.mode).toBe("free");
    expect(out.followId).toBeNull();
    expect(out.center).toEqual({ x: 500, y: 500 });
  });

  test("a reappearing original target does not re-grab follow", () => {
    const cam = new SpectatorCamera();
    cam.start({ x: 0, y: 0 });
    cam.update(inp({ cycleEdge: true, players: [{ id: 1, x: 10, y: 10 }, { id: 3, x: 30, y: 30 }] })); // follow id 1
    cam.update(inp({ players: [{ id: 3, x: 35, y: 35 }] })); // id 1 gone → auto-advance to id 3
    const out = cam.update(inp({ players: [{ id: 1, x: 12, y: 12 }, { id: 3, x: 40, y: 40 }] })); // id 1 back
    expect(out.followId).toBe(3); // stays on the advanced-to target
    expect(out.center).toEqual({ x: 40, y: 40 });
  });
});
