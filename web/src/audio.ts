// PHASE7.md §7.3 — audio cue router + procedural WebAudio sink. Cues are
// driven by Event(0x04) frames and MatchOver(0x08), resolving entity
// kinds via the registry (§6.6). A swappable sink lets vitest assert the
// event→cue mapping without audio hardware. No binary assets (§19.2).

import { EventKind } from "./proto.js";
import { EntityKind, type EntityRegistry } from "./registry.js";

export type Cue = "shoot" | "hit" | "scream" | "generator" | "spawn" | "victory";

export interface AudioSink {
  play(cue: Cue, gain: number): void;
}

// MatchOver §3.8.1 reasons that represent a *result* (play victory).
// ALL_ELIMINATED(2) and SERVER_ERROR(4) are silent.
const VICTORY_REASONS = new Set([0 /*PVE_COMPLETE*/, 1 /*LAST_STANDING*/, 3 /*TIMER*/]);

export class AudioEngine {
  private sink: AudioSink;
  private reg: EntityRegistry;
  private getMasterVolume: () => number;

  constructor(sink: AudioSink, reg: EntityRegistry, getMasterVolume: () => number) {
    this.sink = sink;
    this.reg = reg;
    this.getMasterVolume = getMasterVolume;
  }

  onEvent(kind: number, _actor: number, target: number, _reason: number): void {
    const cue = this.cueForEvent(kind, target);
    if (cue) this.emit(cue);
  }

  onMatchOver(reason: number): void {
    if (VICTORY_REASONS.has(reason)) this.emit("victory");
  }

  // cueForEvent maps a resolved event to a cue (or null = silent). §7.3.
  private cueForEvent(kind: number, target: number): Cue | null {
    switch (kind) {
      case EventKind.EntitySpawn:
        return this.reg.kindOf(target) === EntityKind.Projectile ? "shoot" : null;
      case EventKind.EntityHit:
        return "hit";
      case EventKind.EntityKill:
        // scream only for a confirmed player target; snipe/unknown → hit.
        return this.reg.kindOf(target) === EntityKind.Player ? "scream" : "hit";
      case EventKind.GeneratorDestroyed:
        return "generator";
      case EventKind.PlayerJoin:
      case EventKind.PlayerRejoin:
        return "spawn";
      default:
        return null;
    }
  }

  // emit gates on master volume: 0 short-circuits before any sink call
  // (and, for the WebAudio sink, before any node is created). §7.3.
  private emit(cue: Cue): void {
    const v = clamp01(this.getMasterVolume());
    if (v <= 0) return;
    this.sink.play(cue, v);
  }
}

function clamp01(v: number): number {
  if (!Number.isFinite(v)) return 0;
  return v < 0 ? 0 : v > 1 ? 1 : v;
}

// ---- procedural WebAudio sink (browser only) ----

// Per-cue synth recipe: a short oscillator/noise burst with an
// exponential decay envelope. Tuned to be distinct, not pretty (§19.2).
interface Recipe {
  type: OscillatorType | "noise";
  freq: number;
  durMs: number;
  peak: number; // relative amplitude before master gain
}

const RECIPES: Record<Cue, Recipe> = {
  shoot: { type: "square", freq: 880, durMs: 70, peak: 0.25 },
  hit: { type: "triangle", freq: 220, durMs: 90, peak: 0.3 },
  scream: { type: "sawtooth", freq: 440, durMs: 260, peak: 0.35 },
  generator: { type: "noise", freq: 0, durMs: 400, peak: 0.4 },
  spawn: { type: "sine", freq: 660, durMs: 120, peak: 0.2 },
  victory: { type: "sine", freq: 523, durMs: 500, peak: 0.3 },
};

// WebAudioSink lazily creates a single AudioContext on the first play
// (browser autoplay policy requires a user gesture before this runs).
export class WebAudioSink implements AudioSink {
  private ctx: AudioContext | null = null;

  private ensureCtx(): AudioContext | null {
    if (this.ctx) return this.ctx;
    const Ctor: typeof AudioContext | undefined =
      (globalThis as { AudioContext?: typeof AudioContext }).AudioContext;
    if (!Ctor) return null; // no WebAudio (e.g. headless without it)
    this.ctx = new Ctor();
    return this.ctx;
  }

  // unlock resumes a suspended AudioContext. Call from a user-gesture
  // handler (click/keydown) so cues are audible under browser autoplay
  // policy (codex iter-3). Creates the context if absent.
  unlock(): void {
    const ctx = this.ensureCtx();
    if (ctx && ctx.state === "suspended") void ctx.resume();
  }

  play(cue: Cue, gain: number): void {
    const ctx = this.ensureCtx();
    if (!ctx) return;
    // A server-driven cue may arrive before any gesture; best-effort
    // resume so the cue isn't silently dropped once a gesture lands.
    if (ctx.state === "suspended") void ctx.resume();
    const r = RECIPES[cue];
    const now = ctx.currentTime;
    const dur = r.durMs / 1000;
    const g = ctx.createGain();
    g.gain.setValueAtTime(r.peak * gain, now);
    g.gain.exponentialRampToValueAtTime(0.0001, now + dur);
    g.connect(ctx.destination);
    if (r.type === "noise") {
      const buf = ctx.createBuffer(1, Math.ceil(ctx.sampleRate * dur), ctx.sampleRate);
      const data = buf.getChannelData(0);
      for (let i = 0; i < data.length; i++) data[i] = Math.random() * 2 - 1;
      const src = ctx.createBufferSource();
      src.buffer = buf;
      src.connect(g);
      src.start(now);
      src.stop(now + dur);
    } else {
      const osc = ctx.createOscillator();
      osc.type = r.type;
      osc.frequency.setValueAtTime(r.freq, now);
      osc.connect(g);
      osc.start(now);
      osc.stop(now + dur);
    }
  }
}
