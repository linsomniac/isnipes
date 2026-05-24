// PHASE6.md §16.8 — Playwright config for the deep-link e2e (DoD #16).
// The webServer spawns the single Go binary serving the freshly built
// client from ./web/dist (via the --web-dist flag). Chromium-only is the
// default CI gate per SPEC §8 Phase 7; we use the system Chrome channel
// so no browser download is required.

import { defineConfig, devices } from "@playwright/test";

const PORT = 8123;

export default defineConfig({
  testDir: "./tests/e2e",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "off",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], channel: "chrome" },
    },
  ],
  webServer: {
    command: `go run ./cmd/isnipes --addr=:${PORT} --web-dist=./web/dist`,
    cwd: "..",
    url: `http://127.0.0.1:${PORT}/healthz`,
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
