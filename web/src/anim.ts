// PHASE7.md §6.3 — deterministic animation phase. Every function here is
// pure in (entityId, renderTick) — never wall-clock — so a frozen
// renderTick yields identical pixels (golden-test requirement, §12).
// Overlays (muzzle flash, death poof) are armed from registry-resolved
// events (§6.6) and purged when their fixed duration elapses.

import { EventKind } from "./proto.js";
import { EntityKind, type EntityRegistry } from "./registry.js";

export const WALK_FRAME_TICKS = 4; // render ticks per walk frame
export const MUZZLE_FLASH_TICKS = 6;
export const DEATH_POOF_TICKS = 18;

export type WalkFrame = 0 | 1 | 2 | 3;
export type Dir8Index = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7;

// walkFrame returns the 4-frame walk index. Idle → frame 0. Advances one
// frame every WALK_FRAME_TICKS; a per-id offset desynchronizes entities.
export function walkFrame(entityId: number, renderTick: number, moving: boolean): WalkFrame {
  if (!moving) return 0;
  const base = Math.floor(renderTick / WALK_FRAME_TICKS);
  return (((base + entityId) % 4) + 4) % 4 as WalkFrame;
}

// dir8FromFacing maps the wire Dir8 enum (0=Idle,1=N,2=NE,3=E,4=SE,5=S,
// 6=SW,7=W,8=NW; see sim.ts Dir) to a 0..7 sprite-row index (0=N..7=NW).
// Idle defaults to S (facing the camera).
export function dir8FromFacing(facing: number): Dir8Index {
  switch (facing) {
    case 1: return 0; // N
    case 2: return 1; // NE
    case 3: return 2; // E
    case 4: return 3; // SE
    case 5: return 4; // S
    case 6: return 5; // SW
    case 7: return 6; // W
    case 8: return 7; // NW
    default: return 4; // Idle → S
  }
}

export type OverlayKind = "muzzle" | "poof";

export interface Overlay {
  kind: OverlayKind;
  anchorId: number; // entity the overlay is drawn on
  armedAtTick: number;
}

function durationOf(kind: OverlayKind): number {
  return kind === "muzzle" ? MUZZLE_FLASH_TICKS : DEATH_POOF_TICKS;
}

// OverlayManager tracks transient combat overlays. Arming is driven by
// registry-resolved events; rendering reads active(tick).
export class OverlayManager {
  private list: Overlay[] = [];

  // onEvent arms overlays from an Event(0x04) frame. Per §6.6:
  //  - EntitySpawn of a projectile → muzzle flash on the firing ACTOR.
  //  - EntityKill → death poof on the TARGET.
  // If the anchor entity is unknown (outside AOI / evicted), the overlay
  // is skipped (no entity to anchor the draw to). Audio still plays via
  // AudioEngine, which does not need an on-screen anchor.
  onEvent(reg: EntityRegistry, kind: number, actor: number, target: number, renderTick: number): void {
    if (kind === EventKind.EntitySpawn && reg.kindOf(target) === EntityKind.Projectile) {
      if (reg.kindOf(actor) !== EntityKind.Unknown) this.arm("muzzle", actor, renderTick);
    } else if (kind === EventKind.EntityKill) {
      if (reg.kindOf(target) !== EntityKind.Unknown) this.arm("poof", target, renderTick);
    }
  }

  arm(kind: OverlayKind, anchorId: number, renderTick: number): void {
    this.list.push({ kind, anchorId, armedAtTick: renderTick });
  }

  // active returns the overlays still within their duration at renderTick
  // and purges expired ones.
  active(renderTick: number): Overlay[] {
    this.list = this.list.filter((o) => renderTick - o.armedAtTick < durationOf(o.kind));
    return this.list;
  }

  count(): number {
    return this.list.length;
  }
}
