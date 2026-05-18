// PHASE4.md §9 — client-side input ring buffer + reconcile-on-
// snapshot machinery. The ring keeps the last 30 inputs the client
// has issued for its own player along with the predicted kinematic
// state AFTER applying each input. When a snapshot arrives carrying
// `your_last_input_tick`, the buffered prediction for that tick is
// compared to the authoritative state; on divergence (> 4 subtile
// units) all later inputs are replayed against the server state.

import {
  type ClientInputIntent,
  type MazeView,
  type PlayerKinematic,
  type SolidAABB,
  PLAYER_HALF_EXT,
  stepPlayer,
} from "./sim.js";

export interface ClientInput extends ClientInputIntent {
  fireDir: number; // Dir enum value, 0 = no fire
  clientTick: number; // u16 modular
}

export interface PredEntry {
  clientTick: number;
  input: ClientInput;
  state: PlayerKinematic;
}

// DIVERGE_THRESHOLD per PHASE4.md §9.4 / §4.4 — "e.g. > 4 subtile
// units"; we hard-code 4.
export const DIVERGE_THRESHOLD = 4;

// RING_CAPACITY ≥ 30 valid entries; we use 32 (power of two for
// fast modulo) and treat indices into the ring as `i & 0x1F`.
const RING_CAPACITY = 32;

// modular16Diff returns ((a - b) mod 2^16) interpreted as a signed
// i16, per §4.3.6. Distance between two u16 client ticks.
function modular16Diff(a: number, b: number): number {
  const d = (a - b) & 0xffff;
  return d > 0x7fff ? d - 0x10000 : d;
}

export class PredictionBuffer {
  private buf: PredEntry[] = [];
  private spawnState: PlayerKinematic;

  constructor(initialState: PlayerKinematic) {
    this.spawnState = { ...initialState };
  }

  // Number of valid entries currently held.
  size(): number {
    return this.buf.length;
  }

  // Returns the most-recent predicted state, or the spawn state if
  // empty.
  predicted(): PlayerKinematic {
    if (this.buf.length === 0) return { ...this.spawnState };
    return { ...this.buf[this.buf.length - 1].state };
  }

  // push appends a new (input, predicted-state-after-input) entry.
  // The caller is responsible for computing `state` via stepPlayer.
  push(entry: PredEntry): void {
    this.buf.push(entry);
    if (this.buf.length > RING_CAPACITY) {
      // Drop the oldest. Maintaining ≤ RING_CAPACITY entries.
      this.buf.shift();
    }
  }

  // step advances the local player by applying input. Returns the
  // resulting kinematic. The entry is appended to the ring.
  step(
    input: ClientInput,
    maze: MazeView,
    solids: ReadonlyArray<SolidAABB>,
  ): PlayerKinematic {
    const prev = this.predicted();
    const next = stepPlayer(prev, input, maze, solids);
    this.push({ clientTick: input.clientTick, input, state: next });
    return next;
  }

  // reconcile is called on each Snapshot. Returns { replayed, diverged }
  // where `replayed` is the number of ring entries re-stepped and
  // `diverged` is 1 if the threshold fired.
  reconcile(
    yourLastInputTick: number,
    serverState: PlayerKinematic,
    maze: MazeView,
    solids: ReadonlyArray<SolidAABB>,
  ): { replayed: number; diverged: number } {
    if (this.buf.length === 0) {
      // Cold-start; set spawn to server's state.
      this.spawnState = { ...serverState };
      return { replayed: 0, diverged: 0 };
    }
    // Find the entry whose clientTick == yourLastInputTick (modular).
    let idx = -1;
    for (let i = 0; i < this.buf.length; i++) {
      if (this.buf[i].clientTick === yourLastInputTick) {
        idx = i;
        break;
      }
    }
    if (idx < 0) {
      // Server consumed an input we no longer have buffered (ring
      // wrap, first snapshot, or out-of-order). Hard-reset to server
      // state; drop all buffered entries strictly older.
      this.spawnState = { ...serverState };
      this.buf = [];
      return { replayed: 0, diverged: 0 };
    }
    const buffered = this.buf[idx];
    const dx = Math.abs(serverState.x - buffered.state.x);
    const dy = Math.abs(serverState.y - buffered.state.y);
    if (dx <= DIVERGE_THRESHOLD && dy <= DIVERGE_THRESHOLD) {
      return { replayed: 0, diverged: 0 };
    }
    // Diverged: replay inputs strictly after yourLastInputTick.
    let cur: PlayerKinematic = { ...serverState };
    // Update the matched entry's state to the server's authoritative.
    this.buf[idx] = { ...buffered, state: { ...cur } };
    // Replay forward.
    let replayed = 0;
    for (let i = idx + 1; i < this.buf.length; i++) {
      const e = this.buf[i];
      // Modular check: e.clientTick must be after yourLastInputTick.
      if (modular16Diff(e.clientTick, yourLastInputTick) <= 0) continue;
      cur = stepPlayer(cur, e.input, maze, solids);
      this.buf[i] = { ...e, state: { ...cur } };
      replayed++;
    }
    return { replayed, diverged: 1 };
  }

  // reset clears the ring and re-seeds the spawn state.
  reset(state: PlayerKinematic): void {
    this.buf = [];
    this.spawnState = { ...state };
  }
}

// makeInitialPlayer constructs the spawn kinematic given subtile
// position. halfExt defaults to PLAYER_HALF_EXT.
export function makeInitialPlayer(x: number, y: number): PlayerKinematic {
  return { x, y, vx: 0, vy: 0, halfExt: PLAYER_HALF_EXT, lastDir: 0 };
}
