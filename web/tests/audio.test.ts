// PHASE7.md §15.5 — DoD #12/#13. Event→cue mapping (via registry) and
// master-volume gating, using a fake sink (no audio hardware).

import { describe, expect, test } from "vitest";
import { AudioEngine, type AudioSink, type Cue } from "../src/audio.js";
import { EntityRegistry, EntityKind } from "../src/registry.js";
import { EventKind } from "../src/proto.js";
import type { Entity, Snapshot } from "../src/proto.js";

class FakeSink implements AudioSink {
  calls: { cue: Cue; gain: number }[] = [];
  play(cue: Cue, gain: number): void {
    this.calls.push({ cue, gain });
  }
}

function ent(id: number, kind: number): Entity {
  return { id, kind, hp: 1, facing: 0, flags: 0, x: 0, y: 0, vx: 0, vy: 0 };
}
function snap(entities: Entity[]): Snapshot {
  return { serverTick: 1, yourLastInputTick: 0, yourEntityID: 1, entities };
}

function setup(vol = 1) {
  const sink = new FakeSink();
  const reg = new EntityRegistry();
  reg.update(snap([
    ent(1, EntityKind.Player),
    ent(2, EntityKind.Snipe),
    ent(3, EntityKind.Projectile),
    ent(4, EntityKind.Generator),
  ]));
  const eng = new AudioEngine(sink, reg, () => vol);
  return { sink, reg, eng };
}

describe("audio cue mapping", () => {
  test("TestAudio_EventCueMapping", () => {
    const { sink, eng } = setup();
    eng.onEvent(EventKind.EntitySpawn, 1, 3, 0); // projectile spawn → shoot
    eng.onEvent(EventKind.EntitySpawn, 4, 2, 0); // snipe spawn → silent
    eng.onEvent(EventKind.EntityHit, 1, 2, 0); // → hit
    eng.onEvent(EventKind.EntityKill, 0, 1, 0); // player killed → scream
    eng.onEvent(EventKind.EntityKill, 0, 2, 0); // snipe killed → hit
    eng.onEvent(EventKind.GeneratorDestroyed, 0, 4, 0); // → generator
    eng.onEvent(EventKind.PlayerJoin, 1, 0, 0); // → spawn
    eng.onEvent(EventKind.PlayerRejoin, 1, 0, 0); // → spawn
    eng.onEvent(EventKind.ChatRelay, 1, 0, 0); // → silent
    eng.onEvent(EventKind.PlayerLeave, 1, 0, 0); // → silent
    expect(sink.calls.map((c) => c.cue)).toEqual([
      "shoot", "hit", "scream", "hit", "generator", "spawn", "spawn",
    ]);
  });

  test("TestAudio_UnknownTargetPlaysHitNotScream", () => {
    const { sink, eng } = setup();
    // A kill whose target is outside AOI / already evicted → conservative
    // "hit", never "scream".
    eng.onEvent(EventKind.EntityKill, 0, 99999, 0);
    expect(sink.calls).toHaveLength(1);
    expect(sink.calls[0].cue).toBe("hit");
  });

  test("TestAudio_MatchOverVictoryVsSilent", () => {
    const { sink, eng } = setup();
    eng.onMatchOver(0); // PVE_COMPLETE → victory
    eng.onMatchOver(1); // LAST_STANDING → victory
    eng.onMatchOver(3); // TIMER → victory
    eng.onMatchOver(2); // ALL_ELIMINATED → silent
    eng.onMatchOver(4); // SERVER_ERROR → silent
    expect(sink.calls.map((c) => c.cue)).toEqual(["victory", "victory", "victory"]);
  });
});

describe("audio volume gating", () => {
  test("TestAudio_MasterVolumeGatesOutput", () => {
    // Volume 0 → no sink calls at all.
    const z = setup(0);
    z.eng.onEvent(EventKind.EntityHit, 1, 2, 0);
    z.eng.onMatchOver(1);
    expect(z.sink.calls).toHaveLength(0);

    // Volume 0.5 → gain passed through (clamped).
    const h = setup(0.5);
    h.eng.onEvent(EventKind.EntityHit, 1, 2, 0);
    expect(h.sink.calls).toHaveLength(1);
    expect(h.sink.calls[0].gain).toBe(0.5);
  });

  test("volume is read live (not cached at construction)", () => {
    const sink = new FakeSink();
    const reg = new EntityRegistry();
    reg.update(snap([ent(2, EntityKind.Snipe)]));
    let vol = 0;
    const eng = new AudioEngine(sink, reg, () => vol);
    eng.onEvent(EventKind.EntityHit, 1, 2, 0);
    expect(sink.calls).toHaveLength(0); // muted
    vol = 1;
    eng.onEvent(EventKind.EntityHit, 1, 2, 0);
    expect(sink.calls).toHaveLength(1); // now audible
  });

  test("out-of-range volume is clamped", () => {
    const hi = setup(5);
    hi.eng.onEvent(EventKind.EntityHit, 1, 2, 0);
    expect(hi.sink.calls[0].gain).toBe(1);
  });
});
