// Keep the Playwright e2e specs (tests/e2e/**) out of the vitest run;
// they use the @playwright/test runner, not vitest.
import { defineConfig, configDefaults } from "vitest/config";

export default defineConfig({
  test: {
    exclude: [...configDefaults.exclude, "tests/e2e/**"],
  },
});
