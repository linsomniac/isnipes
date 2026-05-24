// PHASE6.md §16.8 — DoD #16. Two browser contexts: A creates a room and
// the UI surfaces its ID; B opens ?room=<that ID> and auto-joins via the
// deep-link flow (§9); A starts the match; both contexts navigate to the
// in-match view, asserted via the match-view data-testid wrapper.

import { test, expect } from "@playwright/test";

test("TestLobby_DeepLinkPlaywright_E2E", async ({ browser }) => {
  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const a = await ctxA.newPage();
  const b = await ctxB.newPage();

  try {
    // A boots into the lobby (no deep-link) and creates a room.
    await a.goto("/");
    await expect(a.getByTestId("lobby")).toBeVisible();
    await a.getByTestId("create-room").click();

    await expect(a.getByTestId("my-room")).toBeVisible();
    const roomId = (await a.getByTestId("my-room-id").textContent())?.trim() ?? "";
    expect(roomId).toMatch(/^[A-Z0-9]{6}$/);

    // B deep-links straight into A's room.
    await b.goto(`/?room=${roomId}`);
    await expect(b.getByTestId("lobby")).toBeVisible();

    // Once B has joined, A's room shows 2 players and Start enables.
    await expect(a.getByTestId("room-players")).toHaveText("2/4");
    await expect(a.getByTestId("start-btn")).toBeEnabled();
    await a.getByTestId("start-btn").click();

    // Both contexts navigate to the in-match view AND authenticate to the
    // match WS (the data-match-connected marker is only set once a server
    // frame arrives, which a successful MatchJoin gates — a bad token is
    // closed with AUTH before any frame).
    await expect(a.getByTestId("match-view")).toBeVisible();
    await expect(b.getByTestId("match-view")).toBeVisible();
    await expect(a.getByTestId("match-view")).toHaveAttribute("data-match-connected", "true");
    await expect(b.getByTestId("match-view")).toHaveAttribute("data-match-connected", "true");
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});
