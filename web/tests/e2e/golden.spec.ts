// PHASE7.md §12.1-12.3 / §15.12 — DoD #26. Golden screenshot diffs for
// the five reference scenes, rendered deterministically via the
// test-gated ?scene= harness (no live WS). Chromium ≤ 2% per-pixel
// (configured globally as maxDiffPixelRatio: 0.02). Baselines are
// committed under golden.spec.ts-snapshots/ (regenerate with
// `npm run test:e2e -- --update-snapshots`).

import { test, expect } from "@playwright/test";

const SCENES = ["empty-lobby", "full-lobby", "in-match-hud", "scoreboard", "end-screen"] as const;

for (const scene of SCENES) {
  test(`TestGolden_${scene}`, async ({ page }) => {
    // Enable the test-only scene harness before the bundle boots.
    await page.addInitScript(() => {
      (window as unknown as { __ISNIPES_TEST__: boolean }).__ISNIPES_TEST__ = true;
    });
    await page.goto(`/?scene=${scene}`);
    await expect(page.locator('[data-testid="scene-ready"]')).toBeVisible();
    await expect(page).toHaveScreenshot(`${scene}.png`);
  });
}
