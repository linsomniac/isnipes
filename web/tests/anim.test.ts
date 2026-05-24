// PHASE7.md §15.3 — DoD #9/#10. Walk-cycle determinism, muzzle flash on
// fire, death poof on kill, overlay decay. All phase math is pure in
// (entityId, renderTick).

import { describe, expect, test } from "vitest";
import {
  walkFrame, dir8FromFacing, OverlayManager,
  WALK_FRAME_TICKS, MUZZLE_FLASH_TICKS, DEATH_POOF_TICKS,
} from "../src/anim.js";
import { EntityRegistry, EntityKind } from "../src/registry.js";
import { EventKind } from "../src/proto.js";
import type { Entity, Snapshot } from "../src/proto.js";

function ent(id: number, kind: number): Entity {
  return { id, kind, hp: 1, facing: 0, flags: 0, x: 0, y: 0, vx: 0, vy: 0 };
}
function snap(tick: number, entities: Entity[]): Snapshot {
  return { serverTick: tick, yourLastInputTick: 0, yourEntityID: 1, entities };
}

describe("walk cycle", () => {
  test("TestAnim_WalkCyclePhaseDeterministic", () => {
    // Pure: same inputs → same output.
    expect(walkFrame(3, 12, true)).toBe(walkFrame(3, 12, true));
    // Idle → frame 0.
    expect(walkFrame(3, 12, false)).toBe(0);
    // Advances exactly one frame every WALK_FRAME_TICKS.
    const f0 = walkFrame(0, 0, true);
    const f1 = walkFrame(0, WALK_FRAME_TICKS, true);
    const f2 = walkFrame(0, 2 * WALK_FRAME_TICKS, true);
    expect((f0 + 1) % 4).toBe(f1);
    expect((f1 + 1) % 4).toBe(f2);
    // Within a frame window, the frame is stable.
    expect(walkFrame(0, 1, true)).toBe(f0);
    expect(walkFrame(0, WALK_FRAME_TICKS - 1, true)).toBe(f0);
    // Per-id offset desynchronizes entities at the same tick.
    expect(walkFrame(0, 0, true)).not.toBe(walkFrame(1, 0, true));
  });

  test("dir8FromFacing maps Dir8 enum to sprite rows; idle → S", () => {
    expect(dir8FromFacing(1)).toBe(0); // N
    expect(dir8FromFacing(3)).toBe(2); // E
    expect(dir8FromFacing(5)).toBe(4); // S
    expect(dir8FromFacing(8)).toBe(7); // NW
    expect(dir8FromFacing(0)).toBe(4); // Idle → S
  });
});

describe("combat overlays", () => {
  test("TestAnim_MuzzleFlashOnFire arms on the firing actor", () => {
    const reg = new EntityRegistry();
    // actor 1 = player, target 9 = the spawned projectile.
    reg.update(snap(5, [ent(1, EntityKind.Player), ent(9, EntityKind.Projectile)]));
    const ov = new OverlayManager();
    ov.onEvent(reg, EventKind.EntitySpawn, /*actor*/ 1, /*target*/ 9, /*tick*/ 5);
    const active = ov.active(5);
    expect(active).toHaveLength(1);
    expect(active[0]).toMatchObject({ kind: "muzzle", anchorId: 1 });
  });

  test("EntitySpawn of a non-projectile (snipe) arms no muzzle flash", () => {
    const reg = new EntityRegistry();
    reg.update(snap(5, [ent(2, EntityKind.Generator), ent(8, EntityKind.Snipe)]));
    const ov = new OverlayManager();
    ov.onEvent(reg, EventKind.EntitySpawn, 2, 8, 5);
    expect(ov.active(5)).toHaveLength(0);
  });

  test("TestAnim_DeathPoofOnKill arms on a target still in the snapshot", () => {
    const reg = new EntityRegistry();
    // killEntity sets FlagDead but the player stays in the death-tick
    // snapshot, so the kill event's target resolves to Player.
    reg.update(snap(11, [ent(7, EntityKind.Player)]));
    const ov = new OverlayManager();
    ov.onEvent(reg, EventKind.EntityKill, /*actor*/ 0, /*target*/ 7, /*tick*/ 11);
    const active = ov.active(11);
    expect(active).toHaveLength(1);
    expect(active[0]).toMatchObject({ kind: "poof", anchorId: 7 });
  });

  test("kill of an already-removed target arms no poof (no anchor)", () => {
    const reg = new EntityRegistry();
    reg.update(snap(12, [])); // target gone (eliminated/GC'd)
    const ov = new OverlayManager();
    ov.onEvent(reg, EventKind.EntityKill, 0, 7, 12);
    expect(ov.active(12)).toHaveLength(0);
  });

  test("kill with unknown target arms no overlay (no anchor)", () => {
    const reg = new EntityRegistry();
    const ov = new OverlayManager();
    ov.onEvent(reg, EventKind.EntityKill, 0, 12345, 1);
    expect(ov.active(1)).toHaveLength(0);
  });

  test("TestAnim_OverlaysDecayAndPurge", () => {
    const reg = new EntityRegistry();
    reg.update(snap(0, [ent(1, EntityKind.Player), ent(9, EntityKind.Projectile)]));
    const ov = new OverlayManager();
    ov.arm("muzzle", 1, 0);
    ov.arm("poof", 1, 0);
    // Muzzle expires first.
    expect(ov.active(MUZZLE_FLASH_TICKS - 1).some((o) => o.kind === "muzzle")).toBe(true);
    expect(ov.active(MUZZLE_FLASH_TICKS).some((o) => o.kind === "muzzle")).toBe(false);
    // Poof still alive until its longer duration.
    expect(ov.active(DEATH_POOF_TICKS - 1).some((o) => o.kind === "poof")).toBe(true);
    expect(ov.active(DEATH_POOF_TICKS).some((o) => o.kind === "poof")).toBe(false);
    expect(ov.count()).toBe(0);
  });
});
