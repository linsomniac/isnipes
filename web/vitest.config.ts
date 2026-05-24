// Keep the Playwright e2e specs (tests/e2e/**) out of the vitest run;
// they use the @playwright/test runner, not vitest.
import { defineConfig, configDefaults } from "vitest/config";

// PHASE7.md §15 / DoD #28 — per-file coverage gate for the new pure-logic
// modules. browser.ts/scenes.ts/main.ts/lobby.ts are integration/DOM
// layers verified by the Playwright e2e, not by this unit gate.
const COVERED = [
  "src/render.ts",
  "src/maze.ts",
  "src/anim.ts",
  "src/registry.ts",
  "src/audio.ts",
  "src/hud.ts",
  "src/input.ts",
  "src/settings.ts",
  "src/palette.ts",
];

export default defineConfig({
  test: {
    exclude: [...configDefaults.exclude, "tests/e2e/**"],
    coverage: {
      provider: "v8",
      include: COVERED,
      thresholds: {
        perFile: true,
        statements: 70,
        lines: 70,
      },
    },
  },
});
