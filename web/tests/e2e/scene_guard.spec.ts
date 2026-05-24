// PHASE7.md §12.2 / §15.12 — DoD #25. The ?scene= golden harness is
// gated behind the test-only __ISNIPES_TEST__ hook, which a production
// bundle never sets. A prod page at ?scene=... must therefore IGNORE the
// scene and boot the normal lobby flow (no fabricated state).

import { test, expect } from "@playwright/test";

test("TestScene_ProductionIgnoresSceneParam", async ({ page }) => {
  // No addInitScript: __ISNIPES_TEST__ is unset, i.e. a production load.
  await page.goto("/?scene=in-match-hud");
  // The normal flow connects to the lobby and reveals it.
  await expect(page.getByTestId("lobby")).toBeVisible({ timeout: 10_000 });
  // The scene harness must NOT have rendered.
  await expect(page.locator('[data-testid="scene-ready"]')).toHaveCount(0);
  await expect(page.getByTestId("match-view")).not.toHaveAttribute("data-scene-ready", "true");
});
