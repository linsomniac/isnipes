// PHASE7.md §12.4 / §15.12 — DoD #27. Real-WS integration: two browser
// contexts run the LIVE flow (no ?scene=), reach IN_MATCH, the canvas
// paints non-empty after MapInit+Snapshot, and a keyboard action drives
// the local player (a server-authoritative position change), proving the
// render + input path end-to-end (the golden harness bypasses the live
// path).

import { test, expect, type Page } from "@playwright/test";

// canvasNonEmpty samples the game canvas and returns true if any pixel
// differs from the background (i.e. the maze/entities painted).
async function canvasNonEmpty(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const c = document.getElementById("game") as HTMLCanvasElement | null;
    if (!c) return false;
    const ctx = c.getContext("2d");
    if (!ctx) return false;
    const { data } = ctx.getImageData(0, 0, c.width, c.height);
    const r0 = data[0], g0 = data[1], b0 = data[2];
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] !== r0 || data[i + 1] !== g0 || data[i + 2] !== b0) return true;
    }
    return false;
  });
}

// readSelf returns the test-gated self position the client publishes each
// rendered frame (only when __ISNIPES_TEST__ is set).
async function readSelfX(page: Page): Promise<number | null> {
  return page.evaluate(() => {
    const w = window as unknown as { __isnipesSelf?: { x: number; y: number } };
    return w.__isnipesSelf ? w.__isnipesSelf.x : null;
  });
}

test("TestPlay_LiveRenderAndInput", async ({ browser }) => {
  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const a = await ctxA.newPage();
  const b = await ctxB.newPage();
  try {
    // Enable the test-gated self-position hook (no ?scene=, so the live
    // flow is unaffected).
    for (const p of [a, b]) {
      await p.addInitScript(() => {
        (window as unknown as { __ISNIPES_TEST__: boolean }).__ISNIPES_TEST__ = true;
      });
    }
    await a.goto("/");
    await expect(a.getByTestId("lobby")).toBeVisible();
    await a.getByTestId("create-room").click();
    await expect(a.getByTestId("my-room")).toBeVisible();
    const roomId = (await a.getByTestId("my-room-id").textContent())?.trim() ?? "";
    expect(roomId).toMatch(/^[A-Z0-9]{6}$/);

    await b.goto(`/?room=${roomId}`);
    await expect(a.getByTestId("room-players")).toHaveText("2/4");
    await a.getByTestId("start-btn").click();

    // Both reach the in-match view and authenticate the match WS.
    for (const p of [a, b]) {
      await expect(p.getByTestId("match-view")).toBeVisible();
      await expect(p.getByTestId("match-view")).toHaveAttribute("data-match-connected", "true");
    }

    // The canvas paints once MapInit + a Snapshot have arrived.
    await expect.poll(() => canvasNonEmpty(a), { timeout: 10_000 }).toBe(true);

    // The self position is published once a snapshot lands.
    await expect.poll(() => readSelfX(a), { timeout: 10_000 }).not.toBeNull();
    const startX = (await readSelfX(a))!;

    // Hold ArrowRight: the client sends Input frames, the server moves the
    // entity east, and the next snapshots report a larger x. This proves
    // the keyboard → Input → sim → snapshot → render path end-to-end.
    await a.locator("body").focus();
    await a.keyboard.down("ArrowRight");
    await expect.poll(async () => (await readSelfX(a))! - startX, { timeout: 10_000 }).toBeGreaterThan(0);
    await a.keyboard.up("ArrowRight");
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});
