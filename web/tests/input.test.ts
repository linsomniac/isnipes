// PHASE7.md §15.7 — DoD #19/#20. Classic + Modern presets, diagonal
// combos, and the move∩fire overlap rejection (SPEC §3.10.3).

import { describe, expect, test } from "vitest";
import { InputController, PRESETS, validateBindings, type Bindings } from "../src/input.js";
import { Dir } from "../src/sim.js";

describe("input presets", () => {
  test("TestInput_ClassicAndModernPresets resolve to the right intent", () => {
    const classic = new InputController(PRESETS.classic);
    classic.keyDown("ArrowUp");
    expect(classic.intent().dir).toBe(Dir.N);
    classic.keyDown("KeyD"); // fire east
    expect(classic.intent().fireDir).toBe(Dir.E);
    classic.keyDown("Space");
    expect(classic.intent().turbo).toBe(true);

    const modern = new InputController(PRESETS.modern);
    modern.keyDown("KeyW"); // move north
    expect(modern.intent().dir).toBe(Dir.N);
    modern.keyDown("ArrowRight"); // fire east
    expect(modern.intent().fireDir).toBe(Dir.E);
    modern.keyDown("ShiftLeft");
    expect(modern.intent().turbo).toBe(true);
  });

  test("TestInput_DiagonalCombos: adjacent pair → diagonal, opposing cancel", () => {
    const c = new InputController(PRESETS.classic);
    c.keyDown("ArrowUp");
    c.keyDown("ArrowRight");
    expect(c.intent().dir).toBe(Dir.NE);
    c.keyDown("ArrowDown"); // up+down cancel vertically → only right → E
    expect(c.intent().dir).toBe(Dir.E);
    c.keyUp("ArrowRight"); // up+down only → Idle
    expect(c.intent().dir).toBe(Dir.Idle);

    // fire diagonal via WASD (classic): W+D → NE.
    const f = new InputController(PRESETS.classic);
    f.keyDown("KeyW");
    f.keyDown("KeyD");
    expect(f.intent().fireDir).toBe(Dir.NE);
  });

  test("vi keys h/j/k/l move (in addition to the bound move keys)", () => {
    const c = new InputController(PRESETS.classic);
    c.keyDown("KeyK"); // up
    expect(c.intent().dir).toBe(Dir.N);
    c.keyUp("KeyK");
    c.keyDown("KeyJ"); // down
    expect(c.intent().dir).toBe(Dir.S);
    c.keyUp("KeyJ");
    c.keyDown("KeyL"); // right
    expect(c.intent().dir).toBe(Dir.E);
    c.keyDown("KeyK"); // right + up → NE diagonal
    expect(c.intent().dir).toBe(Dir.NE);
    // vi keys move, never fire.
    expect(c.intent().fireDir).toBe(Dir.Idle);

    // vi aliases compose with the bound arrows too (modern preset: WASD move,
    // arrows fire — h/j/k/l still move, not fire).
    const m = new InputController(PRESETS.modern);
    m.keyDown("KeyL");
    expect(m.intent().dir).toBe(Dir.E);
    expect(m.intent().fireDir).toBe(Dir.Idle);
  });

  test("keyUp clears held; clearHeld resets", () => {
    const c = new InputController(PRESETS.classic);
    c.keyDown("ArrowUp");
    expect(c.intent().dir).toBe(Dir.N);
    c.keyUp("ArrowUp");
    expect(c.intent().dir).toBe(Dir.Idle);
    c.keyDown("Space");
    c.clearHeld();
    expect(c.intent().turbo).toBe(false);
  });
});

describe("input rebinding", () => {
  test("default presets are conflict-free", () => {
    expect(validateBindings(PRESETS.classic)).toBeNull();
    expect(validateBindings(PRESETS.modern)).toBeNull();
  });

  test("TestInput_RebindRejectsOverlap (move ∩ fire) keeps prior map", () => {
    const c = new InputController(PRESETS.classic);
    const bad: Bindings = { ...PRESETS.classic, fireN: "ArrowUp" }; // ArrowUp is moveN
    const res = c.setBindings(bad);
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.conflict).toBe("ArrowUp");
    // prior (classic) map retained: fireN still KeyW.
    expect(c.getBindings().fireN).toBe("KeyW");
  });

  test("turbo/chat/menu may not collide with move or fire", () => {
    const c = new InputController(PRESETS.classic);
    expect(c.setBindings({ ...PRESETS.classic, turbo: "ArrowUp" }).ok).toBe(false);
    expect(c.setBindings({ ...PRESETS.classic, chatOpen: "KeyW" }).ok).toBe(false);
  });

  test("rejects incomplete/corrupt bindings (missing or empty action key)", () => {
    const c = new InputController(PRESETS.classic);
    // missing turbo key.
    const missing = { ...PRESETS.classic } as Record<string, string>;
    delete missing.turbo;
    expect(c.setBindings(missing as never).ok).toBe(false);
    // empty-string key.
    expect(c.setBindings({ ...PRESETS.classic, menu: "" }).ok).toBe(false);
    // null key (corrupt persisted value).
    expect(c.setBindings({ ...PRESETS.classic, moveN: null } as never).ok).toBe(false);
    // prior map intact.
    expect(c.getBindings().turbo).toBe("Space");
  });

  test("a valid rebind applies", () => {
    const c = new InputController(PRESETS.classic);
    const ok = c.setBindings({ ...PRESETS.classic, turbo: "KeyQ" });
    expect(ok.ok).toBe(true);
    c.keyDown("KeyQ");
    expect(c.intent().turbo).toBe(true);
  });
});
