// PHASE7.md §15.4 — DoD #11. Entity-kind resolution from snapshots,
// unknown-id fallback, retention of just-removed entities.

import { describe, expect, test } from "vitest";
import { EntityRegistry, EntityKind } from "../src/registry.js";
import type { Entity, Snapshot } from "../src/proto.js";

function ent(id: number, kind: number, x = 0, y = 0, facing = 0): Entity {
  return { id, kind, hp: 1, facing, flags: 0, x, y, vx: 0, vy: 0 };
}
function snap(tick: number, entities: Entity[]): Snapshot {
  return { serverTick: tick, yourLastInputTick: 0, yourEntityID: 1, entities };
}

describe("entity registry", () => {
  test("TestRegistry_ResolveEventEntityKinds", () => {
    const reg = new EntityRegistry();
    reg.update(snap(10, [
      ent(1, EntityKind.Player),
      ent(2, EntityKind.Snipe),
      ent(3, EntityKind.Projectile),
      ent(4, EntityKind.Generator),
    ]));
    expect(reg.kindOf(1)).toBe(EntityKind.Player);
    expect(reg.kindOf(2)).toBe(EntityKind.Snipe);
    expect(reg.kindOf(3)).toBe(EntityKind.Projectile);
    expect(reg.kindOf(4)).toBe(EntityKind.Generator);
  });

  test("TestRegistry_UnknownIdReturnsZero", () => {
    const reg = new EntityRegistry();
    reg.update(snap(1, [ent(1, EntityKind.Player)]));
    expect(reg.kindOf(999)).toBe(EntityKind.Unknown);
    expect(reg.kindOf(999)).toBe(0);
    expect(reg.get(999)).toBeNull();
  });

  test("TestRegistry_RebuildsEachSnapshot replaces; off-AOI/removed → Unknown", () => {
    const reg = new EntityRegistry();
    reg.update(snap(10, [ent(1, EntityKind.Player), ent(7, EntityKind.Player)]));
    expect(reg.size()).toBe(2);
    // §6.6: an entity absent from the next snapshot (off-AOI or removed)
    // is immediately Unknown — no stale-kind retention (would mis-fire
    // `scream` for off-AOI kills).
    reg.update(snap(11, [ent(1, EntityKind.Player)]));
    expect(reg.kindOf(7)).toBe(EntityKind.Unknown);
    expect(reg.kindOf(1)).toBe(EntityKind.Player);
    expect(reg.size()).toBe(1);
  });

  test("kind updates when an id changes kind across snapshots", () => {
    const reg = new EntityRegistry();
    reg.update(snap(1, [ent(5, EntityKind.Projectile, 100, 200, 3)]));
    expect(reg.get(5)?.x).toBe(100);
    reg.update(snap(2, [ent(5, EntityKind.Projectile, 150, 200, 3)]));
    expect(reg.get(5)?.x).toBe(150);
    expect(reg.get(5)?.lastTick).toBe(2);
  });
});
