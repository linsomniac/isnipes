// PHASE4.md §18.9 — interpolation buffer tests.

import { describe, expect, test } from "vitest";
import { InterpBuffer, type EntityState } from "../src/interp.js";

function ent(id: number, x: number, y: number): EntityState {
  return { id, kind: 1, hp: 1, facing: 1, flags: 0, x, y, vx: 0, vy: 0 };
}

describe("InterpBuffer", () => {
  test("empty → sample returns []", () => {
    const b = new InterpBuffer();
    expect(b.sample(1000)).toEqual([]);
  });

  test("single snapshot → returns it verbatim", () => {
    const b = new InterpBuffer();
    b.push(0, 100, [ent(1, 100, 200)]);
    const s = b.sample(200);
    expect(s).toHaveLength(1);
    expect(s[0].x).toBe(100);
    expect(s[0].y).toBe(200);
  });

  test("lerp between two snapshots at midpoint", () => {
    const b = new InterpBuffer(/* lag */ 100);
    b.push(0, 100, [ent(1, 100, 100)]);
    b.push(1, 200, [ent(1, 200, 100)]);
    // nowWallMs - 100 = 150 → halfway between t=100 and t=200.
    const s = b.sample(250);
    expect(s[0].x).toBe(150);
  });

  test("lerp at boundary alpha = 0 → uses before", () => {
    const b = new InterpBuffer(100);
    b.push(0, 100, [ent(1, 100, 100)]);
    b.push(1, 200, [ent(1, 200, 100)]);
    const s = b.sample(200); // tRender=100, exactly at sBefore
    expect(s[0].x).toBe(100);
  });

  test("entity disappears in next snapshot → carried forward", () => {
    const b = new InterpBuffer(100);
    b.push(0, 100, [ent(1, 100, 100), ent(2, 50, 50)]);
    b.push(1, 200, [ent(1, 200, 100)]);
    const s = b.sample(250);
    const e2 = s.find((e) => e.id === 2);
    expect(e2).toBeDefined();
    expect(e2!.x).toBe(50);
  });

  test("new entity appears in second snapshot → present in result", () => {
    const b = new InterpBuffer(100);
    b.push(0, 100, [ent(1, 100, 100)]);
    b.push(1, 200, [ent(1, 200, 100), ent(3, 300, 300)]);
    const s = b.sample(250);
    const e3 = s.find((e) => e.id === 3);
    expect(e3).toBeDefined();
    expect(e3!.x).toBe(300);
  });

  test("stale snapshots pruned past horizon", () => {
    const b = new InterpBuffer(100);
    b.push(0, 0, [ent(1, 0, 0)]);
    b.push(1, 100, [ent(1, 100, 0)]);
    b.push(2, 200, [ent(1, 200, 0)]);
    // Push something 2 seconds later — stale pruning kicks in.
    b.push(3, 2200, [ent(1, 300, 0)]);
    // Buffer should be ≤ 2 entries (stale horizon = 1000 ms).
    expect(b.size()).toBeLessThanOrEqual(2);
  });

  test("clear() empties the buffer", () => {
    const b = new InterpBuffer(100);
    b.push(0, 100, [ent(1, 100, 100)]);
    b.clear();
    expect(b.size()).toBe(0);
    expect(b.sample(200)).toEqual([]);
  });
});
