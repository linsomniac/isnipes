// PHASE7.md §6.6 — presentation-side entity registry. `Event` (0x04)
// frames carry only {kind, actor, target, reason} with NO entity kind,
// so animation (§6.3) and audio (§7.3) resolve an event's actor/target
// *kind* through this map, rebuilt from every Snapshot. A short
// retention window keeps a just-removed entity (e.g. a player killed
// this tick) resolvable for the kill event that references it.

import type { Snapshot } from "./proto.js";

// EntityKind mirrors internal/sim/config.go. 0 = unknown (not in AOI or
// already removed past the retention window).
export const EntityKind = {
  Unknown: 0,
  Player: 1,
  Generator: 2,
  Projectile: 3,
  Snipe: 4,
} as const;

// How long (in server ticks) a no-longer-seen entity stays resolvable.
// ~3 snapshots at 15 Hz snapshot cadence covers the kill-event race.
export const RETENTION_TICKS = 6;

export interface RegEntry {
  kind: number;
  x: number;
  y: number;
  facing: number;
  lastTick: number;
}

export class EntityRegistry {
  private m = new Map<number, RegEntry>();

  // update merges the snapshot's entities (does not clear), stamping
  // each with the snapshot tick. Call prune() to evict stale entries.
  update(s: Snapshot): void {
    for (const e of s.entities) {
      this.m.set(e.id, { kind: e.kind, x: e.x, y: e.y, facing: e.facing, lastTick: s.serverTick });
    }
    this.prune(s.serverTick);
  }

  prune(currentTick: number, retentionTicks: number = RETENTION_TICKS): void {
    for (const [id, e] of this.m) {
      if (currentTick - e.lastTick > retentionTicks) this.m.delete(id);
    }
  }

  // kindOf returns the entity's kind, or EntityKind.Unknown (0) when the
  // id is outside AOI / already evicted (the §6.6 graceful fallback).
  kindOf(id: number): number {
    return this.m.get(id)?.kind ?? EntityKind.Unknown;
  }

  get(id: number): RegEntry | null {
    return this.m.get(id) ?? null;
  }

  size(): number {
    return this.m.size;
  }
}
