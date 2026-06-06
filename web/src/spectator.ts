// Spectator camera for an eliminated player (out of lives, match still
// running). Pure: no DOM, no canvas, no clock read — the caller passes the
// frame delta (dtMs) and the current living-player list each frame, so the
// curves are display-rate independent and unit-testable. Mirrors the
// RespawnSequencer pattern in web/src/death.ts.
//
// Spec: docs/superpowers/specs/2026-06-05-spectator-scroll-design.md.

import { Dir, velocityFor, PLAYER_TURBO_SPEED } from "./sim.js";

export type SpectatorMode = "free" | "follow";

export interface SpectatorPlayer {
  id: number;
  x: number;
  y: number;
}

export interface SpectatorInput {
  dtMs: number; // frame delta (performance.now() diff); clamped to MAX_DT_MS
  panDir: Dir; // movement-key Dir8 (Dir.Idle = no pan)
  turbo: boolean; // turbo key held → fast pan
  cycleEdge: boolean; // true only on the frame Tab was pressed
  players: SpectatorPlayer[]; // living players this frame (kind===Player)
  bounds: { worldW: number; worldH: number }; // subtile maze extents
}

export interface SpectatorOutput {
  center: { x: number; y: number }; // camera centre → cameraOverride
  mode: SpectatorMode;
  followId: number | null; // followed player id (follow mode), else null
}

// The sim runs at 30 ticks/sec; sim.ts speeds are per-tick subtiles, so a
// per-second pan speed is the per-tick turbo speed × the tick rate.
const TICKS_PER_SEC = 30;

// PAN_SPEED: a brisk free-look, ≈ player turbo speed (6720 subtiles/s).
export const PAN_SPEED = PLAYER_TURBO_SPEED * TICKS_PER_SEC;
// PAN_TURBO_MULT: holding turbo doubles the pan speed.
export const PAN_TURBO_MULT = 2;
// MAX_DT_MS: clamp the frame delta so a tab-out / GC pause can't fling the
// camera across the map in one frame.
export const MAX_DT_MS = 100;

// AIDEV-NOTE: SpectatorCamera is a pure state machine — no side effects, no
// clock. The caller (drawFrame) passes dtMs + the snapshot's living players;
// it returns a world centre the existing cameraOverride path consumes. The
// only mutable state is the centre, mode, and followId.
export class SpectatorCamera {
  private center = { x: 0, y: 0 };
  private mode: SpectatorMode = "free";
  private followId: number | null = null;

  // start seeds free-pan at the death spot (called once on the first
  // spectating frame).
  start(pos: { x: number; y: number }): void {
    this.center = { x: pos.x, y: pos.y };
    this.mode = "free";
    this.followId = null;
  }

  update(input: SpectatorInput): SpectatorOutput {
    const { dtMs, panDir, turbo, cycleEdge, players, bounds } = input;
    const dtSec = Math.min(dtMs, MAX_DT_MS) / 1000;

    // 1. Tab edge → advance to the next living player.
    if (cycleEdge) {
      const next = this.nextFollowId(players);
      if (next !== null) {
        this.followId = next;
        this.mode = "follow";
      }
      // none → stay in the current mode (no-op)
    }

    // 2. A movement key releases follow back to free-pan in place.
    if (panDir !== Dir.Idle && this.mode === "follow") {
      this.mode = "free";
      this.followId = null;
    }

    // 3. Apply the active mode.
    if (this.mode === "follow") {
      const target = players.find((p) => p.id === this.followId);
      if (target) {
        this.center = { x: target.x, y: target.y };
      } else {
        // Followed player left the list (eliminated, or culled by the
        // server's 64-entity dead-cam cap) → auto-advance, or drop to free.
        const next = this.nextFollowId(players);
        if (next === null) {
          this.mode = "free";
          this.followId = null;
          // hold the last centre
        } else {
          this.followId = next;
          const t = players.find((p) => p.id === next)!;
          this.center = { x: t.x, y: t.y };
        }
      }
    } else if (panDir !== Dir.Idle) {
      const speed = PAN_SPEED * (turbo ? PAN_TURBO_MULT : 1);
      const [vx, vy] = velocityFor(panDir, speed);
      this.center.x += vx * dtSec;
      this.center.y += vy * dtSec;
    }

    // 4. Clamp the centre to the world so free/follow stay coherent and the
    // centre never drifts off-map. The renderer's computeCamera does the
    // final viewport-edge clamp on top of this.
    this.center.x = clamp(this.center.x, 0, bounds.worldW);
    this.center.y = clamp(this.center.y, 0, bounds.worldH);

    return {
      center: { x: this.center.x, y: this.center.y },
      mode: this.mode,
      followId: this.mode === "follow" ? this.followId : null,
    };
  }

  // nextFollowId returns the living player id immediately after the current
  // followId in id-ascending order, wrapping to the first; the first id when
  // not yet following; or null when there are no players.
  private nextFollowId(players: SpectatorPlayer[]): number | null {
    if (players.length === 0) return null;
    const ids = players.map((p) => p.id).sort((a, b) => a - b);
    if (this.followId === null) return ids[0];
    for (const id of ids) if (id > this.followId) return id;
    return ids[0]; // wrap
  }
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.max(lo, Math.min(v, hi));
}
