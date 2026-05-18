// PHASE4.md §14 — Go/TS physics-constants parity oracle.
// Both internal/sim/physics_test.go::TestPhysicsConstantsTextFile
// and this test read testdata/sim/physics_constants.txt and assert
// their in-language constants match. CI fails if either side drifts.

import { describe, expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  SUBTILE_PER_TILE,
  PLAYER_SPEED,
  PLAYER_TURBO_SPEED,
  PLAYER_HALF_EXT,
  FIRE_COOLDOWN_TICKS,
  PROJECTILE_SPEED,
  PROJECTILE_LIFETIME,
  GENERATOR_HALF_EXT,
  PROJECTILE_HALF_EXT,
} from "../src/sim.js";

const ORACLE = resolve(__dirname, "../../testdata/sim/physics_constants.txt");

function parseConstants(raw: string): Record<string, number> {
  const out: Record<string, number> = {};
  for (const line of raw.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || !trimmed.includes("=")) continue;
    const [k, v] = trimmed.split("=", 2);
    out[k.trim()] = parseInt(v.trim(), 10);
  }
  return out;
}

describe("PHASE4 §14 physics_constants.txt parity", () => {
  const raw = readFileSync(ORACLE, "utf8");
  const file = parseConstants(raw);

  test("starts with isnipes-physics/v1 header", () => {
    expect(raw.startsWith("isnipes-physics/v1")).toBe(true);
  });

  test.each([
    ["subtile_per_tile", SUBTILE_PER_TILE],
    ["player_speed", PLAYER_SPEED],
    ["player_turbo_speed", PLAYER_TURBO_SPEED],
    ["player_half_ext", PLAYER_HALF_EXT],
    ["fire_cooldown_ticks", FIRE_COOLDOWN_TICKS],
    ["projectile_speed", PROJECTILE_SPEED],
    ["projectile_lifetime", PROJECTILE_LIFETIME],
    ["generator_half_ext", GENERATOR_HALF_EXT],
    ["projectile_half_ext", PROJECTILE_HALF_EXT],
  ])("file constant %s matches TS", (key, tsValue) => {
    expect(file[key]).toBeDefined();
    expect(file[key]).toBe(tsValue);
  });
});
