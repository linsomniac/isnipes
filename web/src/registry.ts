// PHASE7.md §6.6 — presentation-side entity registry. `Event` (0x04)
// frames carry only {kind, actor, target, reason} with NO entity kind,
// so animation (§6.3) and audio (§7.3) resolve an event's actor/target
// *kind* through this map, which reflects the LATEST Snapshot only.
//
// §6.6: an id outside AOI OR already removed resolves to Unknown. We
// therefore REPLACE on each snapshot rather than retain — a stale kind
// would make an off-AOI kill play `scream` instead of the required
// conservative `hit` (codex iter-3). This still resolves a freshly
// killed player: killEntity() sets FlagDead and the entity stays in the
// snapshot for the death tick (GC removes only eliminated/snipe slots),
// so the kill event's target is still present as a Player.

import type { Snapshot } from "./proto.js";

// EntityKind mirrors internal/sim/config.go. 0 = unknown (not in the
// latest snapshot: outside AOI or removed).
export const EntityKind = {
  Unknown: 0,
  Player: 1,
  Generator: 2,
  Projectile: 3,
  Snipe: 4,
} as const;

export interface RegEntry {
  kind: number;
  x: number;
  y: number;
  facing: number;
  lastTick: number;
}

export class EntityRegistry {
  private m = new Map<number, RegEntry>();

  // update rebuilds the map from the snapshot's entities (replace, not
  // merge). Anything absent from this snapshot becomes Unknown.
  update(s: Snapshot): void {
    this.m.clear();
    for (const e of s.entities) {
      this.m.set(e.id, { kind: e.kind, x: e.x, y: e.y, facing: e.facing, lastTick: s.serverTick });
    }
  }

  // kindOf returns the entity's kind, or EntityKind.Unknown (0) when the
  // id is not in the latest snapshot (the §6.6 graceful fallback).
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
