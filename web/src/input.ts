// PHASE7.md §9 — keyboard input → movement/fire/turbo intent, with the
// Classic (default) and Modern presets (SPEC §3.10) and rebinding.
// Bindings are modeled Action→key so a collision is representable and
// the SPEC §3.10.3 "movement and shoot key sets must not overlap" rule
// is checkable (codex iter-0 finding #3).

import { Dir } from "./sim.js";

export type Action =
  | "moveN" | "moveE" | "moveS" | "moveW"
  | "fireN" | "fireE" | "fireS" | "fireW"
  | "turbo" | "chatOpen" | "menu";

export type Preset = "classic" | "modern";

// Bindings map each Action to a physical KeyboardEvent.code.
export type Bindings = Record<Action, string>;

export interface InputIntent {
  dir: Dir;
  turbo: boolean;
  fireDir: Dir;
}

const MOVE_ACTIONS: Action[] = ["moveN", "moveE", "moveS", "moveW"];
const FIRE_ACTIONS: Action[] = ["fireN", "fireE", "fireS", "fireW"];
export const ALL_ACTIONS: Action[] = [
  ...MOVE_ACTIONS, ...FIRE_ACTIONS, "turbo", "chatOpen", "menu",
];

export const PRESETS: Record<Preset, Bindings> = {
  // SPEC §3.10.1: arrows move, WASD fire, Space turbo.
  classic: {
    moveN: "ArrowUp", moveS: "ArrowDown", moveW: "ArrowLeft", moveE: "ArrowRight",
    fireN: "KeyW", fireS: "KeyS", fireW: "KeyA", fireE: "KeyD",
    turbo: "Space", chatOpen: "KeyT", menu: "Escape",
  },
  // SPEC §3.10.2: WASD move, arrows fire, Shift turbo.
  modern: {
    moveN: "KeyW", moveS: "KeyS", moveW: "KeyA", moveE: "KeyD",
    fireN: "ArrowUp", fireS: "ArrowDown", fireW: "ArrowLeft", fireE: "ArrowRight",
    turbo: "ShiftLeft", chatOpen: "KeyT", menu: "Escape",
  },
};

// combineDir maps held N/E/S/W booleans to a Dir8 (adjacent pair →
// diagonal; opposing pair → cancel; none → Idle).
function combineDir(up: boolean, down: boolean, left: boolean, right: boolean): Dir {
  const vy = up && !down ? -1 : down && !up ? 1 : 0;
  const vx = right && !left ? 1 : left && !right ? -1 : 0;
  if (vx === 0 && vy === 0) return Dir.Idle;
  if (vx === 0) return vy < 0 ? Dir.N : Dir.S;
  if (vy === 0) return vx > 0 ? Dir.E : Dir.W;
  if (vx > 0) return vy < 0 ? Dir.NE : Dir.SE;
  return vy < 0 ? Dir.NW : Dir.SW;
}

// validateBindings enforces SPEC §3.10.3: the move-key set and the
// fire-key set must be disjoint, and turbo/chat/menu must not collide
// with any move or fire key. Returns the colliding key, or null if ok.
export function validateBindings(b: Bindings): string | null {
  // Completeness: every action must map to a non-empty key string
  // (codex iter-3: corrupt persisted bindings like {turbo:null} or
  // {moveN:""} would silently disable an action).
  for (const a of ALL_ACTIONS) {
    if (typeof b[a] !== "string" || b[a] === "") return `missing:${a}`;
  }
  const moveKeys = MOVE_ACTIONS.map((a) => b[a]);
  const fireKeys = FIRE_ACTIONS.map((a) => b[a]);
  const moveSet = new Set(moveKeys);
  // duplicate within move or within fire is also a conflict.
  if (moveSet.size !== moveKeys.length) return findDup(moveKeys);
  const fireSet = new Set(fireKeys);
  if (fireSet.size !== fireKeys.length) return findDup(fireKeys);
  for (const k of fireKeys) if (moveSet.has(k)) return k; // move ∩ fire
  const combat = new Set([...moveKeys, ...fireKeys]);
  for (const a of ["turbo", "chatOpen", "menu"] as Action[]) {
    if (combat.has(b[a])) return b[a];
  }
  return null;
}

function findDup(keys: string[]): string {
  const seen = new Set<string>();
  for (const k of keys) {
    if (seen.has(k)) return k;
    seen.add(k);
  }
  return keys[0];
}

export class InputController {
  private bindings: Bindings;
  private held = new Set<string>();
  // keyForAction is the reverse index, rebuilt on setBindings.
  private actionKey: Bindings;

  constructor(bindings: Bindings = PRESETS.classic) {
    this.bindings = { ...bindings };
    this.actionKey = { ...bindings };
  }

  keyDown(code: string): void {
    this.held.add(code);
  }

  keyUp(code: string): void {
    this.held.delete(code);
  }

  clearHeld(): void {
    this.held.clear();
  }

  private isHeld(action: Action): boolean {
    return this.held.has(this.actionKey[action]);
  }

  intent(): InputIntent {
    const dir = combineDir(
      this.isHeld("moveN"), this.isHeld("moveS"), this.isHeld("moveW"), this.isHeld("moveE"),
    );
    const fireDir = combineDir(
      this.isHeld("fireN"), this.isHeld("fireS"), this.isHeld("fireW"), this.isHeld("fireE"),
    );
    return { dir, turbo: this.isHeld("turbo"), fireDir };
  }

  getBindings(): Bindings {
    return { ...this.bindings };
  }

  // setBindings validates and, on success, applies. On conflict it keeps
  // the prior map and returns the offending key (DoD #20).
  setBindings(b: Bindings): { ok: true } | { ok: false; conflict: string } {
    const conflict = validateBindings(b);
    if (conflict !== null) return { ok: false, conflict };
    this.bindings = { ...b };
    this.actionKey = { ...b };
    return { ok: true };
  }
}
