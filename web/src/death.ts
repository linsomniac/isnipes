// Death → respawn presentation sequencer (client-only). Pure: no DOM, no
// canvas, no clock read — the caller passes performance.now() as nowMs so the
// curves are display-rate independent and unit-testable.
//
// Eliminates the camera origin-jump on death: holds the camera at the death
// spot, plays a red sting, fades to dark with a RESPAWNING countdown, then
// fades back in at the server-chosen spawn (hiding the teleport).
//
// Spec: docs/superpowers/specs/2026-06-03-death-respawn-camera-design.md.

export interface DeathFxInput {
  selfPresent: boolean;
  selfPos: { x: number; y: number } | null;
  livesRemaining: number;
  nowMs: number;
}

export interface DeathFxOutput {
  cameraOverride: { x: number; y: number } | null;
  redAlpha: number; // 0..1
  dimAlpha: number; // 0..1
  countdown: number | null; // integer seconds, or null when not shown
}

// Timing (ms) / intensity (alpha) constants. Tunable.
export const RED_PEAK = 0.55;
export const RED_MS = 400;
export const HOLD_MS = 1000;
export const DIM_RAMP_MS = 400;
export const DIM_MAX = 0.85;
export const RESPAWN_MS = 3000; // mirrors the server's 90-tick @ 30Hz timer
export const FADEIN_MS = 400;
export const SAFETY_MS = 5000; // bail-out if no respawn arrives (disconnect)

// redStingAlpha: peak at t=0, linear decay to 0 by RED_MS.
export function redStingAlpha(elapsedMs: number): number {
  if (elapsedMs <= 0) return RED_PEAK;
  if (elapsedMs >= RED_MS) return 0;
  return RED_PEAK * (1 - elapsedMs / RED_MS);
}

// dimRampAlpha: 0 until HOLD_MS, then ramps to DIM_MAX over DIM_RAMP_MS and holds.
export function dimRampAlpha(elapsedMs: number): number {
  if (elapsedMs <= HOLD_MS) return 0;
  const t = elapsedMs - HOLD_MS;
  if (t >= DIM_RAMP_MS) return DIM_MAX;
  return DIM_MAX * (t / DIM_RAMP_MS);
}

// respawnCountdown: null until the dim begins, then integer seconds left (>= 1).
export function respawnCountdown(elapsedMs: number): number | null {
  if (elapsedMs <= HOLD_MS) return null;
  return Math.max(1, Math.ceil((RESPAWN_MS - elapsedMs) / 1000));
}

type Phase = "alive" | "dead" | "fadein";

function idle(): DeathFxOutput {
  return { cameraOverride: null, redAlpha: 0, dimAlpha: 0, countdown: null };
}

// AIDEV-NOTE: RespawnSequencer is a pure state machine — no side effects.
// The caller (render loop) passes nowMs (performance.now()) each frame so
// tests can inject synthetic timestamps without touching the clock.
export class RespawnSequencer {
  private phase: Phase = "alive";
  private deathPos = { x: 0, y: 0 };
  private deathAtMs = 0;
  private fadeInAtMs = 0;
  private eliminated = false;
  private lastSelfPos: { x: number; y: number } | null = null;

  update(input: DeathFxInput): DeathFxOutput {
    const { selfPresent, selfPos, livesRemaining, nowMs } = input;

    if (selfPresent && selfPos) this.lastSelfPos = { x: selfPos.x, y: selfPos.y };

    switch (this.phase) {
      case "alive": {
        if (!selfPresent && this.lastSelfPos) {
          this.phase = "dead";
          this.deathPos = { ...this.lastSelfPos };
          this.deathAtMs = nowMs;
          this.eliminated = livesRemaining <= 0;
          // Fall through to the dead branch immediately so the first dead
          // frame already carries cameraOverride / redAlpha.
          return this.update(input);
        }
        return idle();
      }

      case "dead": {
        if (this.eliminated) {
          if (selfPresent) { this.phase = "alive"; return idle(); }
          return {
            cameraOverride: null,
            redAlpha: redStingAlpha(nowMs - this.deathAtMs),
            dimAlpha: 0,
            countdown: null,
          };
        }
        if (selfPresent) {
          // Marine reappeared at the new spawn → fade back in this frame.
          this.phase = "fadein";
          this.fadeInAtMs = nowMs;
          return this.update(input);
        }
        const elapsed = nowMs - this.deathAtMs;
        if (elapsed > SAFETY_MS) { this.phase = "alive"; return idle(); }
        return {
          cameraOverride: { ...this.deathPos },
          redAlpha: redStingAlpha(elapsed),
          dimAlpha: dimRampAlpha(elapsed),
          countdown: respawnCountdown(elapsed),
        };
      }

      case "fadein": {
        if (!selfPresent) {
          // Vanished again mid-fade — restart the dead handling.
          this.phase = "dead";
          this.deathAtMs = nowMs;
          if (this.lastSelfPos) this.deathPos = { ...this.lastSelfPos };
          this.eliminated = livesRemaining <= 0;
          return this.update(input);
        }
        const t = nowMs - this.fadeInAtMs;
        if (t >= FADEIN_MS) { this.phase = "alive"; return idle(); }
        return {
          cameraOverride: null,
          redAlpha: 0,
          dimAlpha: DIM_MAX * (1 - t / FADEIN_MS),
          countdown: null,
        };
      }
    }
  }
}
