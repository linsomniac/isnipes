// PHASE7.md §15.10 — DoD #21. App state transitions
// LOBBY → IN_MATCH → POST_MATCH → LOBBY.

import { describe, expect, test } from "vitest";
import { App, type AppDeps, type AppState } from "../src/main.js";
import type { LobbyWS } from "../src/lobby.js";

class FakeLobbyWS implements LobbyWS {
  sent: string[] = [];
  onmessage: ((data: string) => void) | null = null;
  onclose: (() => void) | null = null;
  onopen: (() => void) | null = null;
  send(data: string): void { this.sent.push(data); }
  close(): void {}
  deliver(t: string, d: unknown): void { this.onmessage?.(JSON.stringify({ t, v: 1, d })); }
}

function makeApp(): { app: App; ws: FakeLobbyWS; states: AppState[] } {
  const ws = new FakeLobbyWS();
  const deps: AppDeps = {
    openLobbyWS: () => ws,
    locationSearch: () => "",
    getStoredNick: () => "tester",
    schemaChecksum: 0x42607394,
    clientVersion: "v0",
  };
  const app = new App(deps);
  const states: AppState[] = [];
  app.onStateChange = (s) => states.push(s);
  return { app, ws, states };
}

describe("App state machine", () => {
  test("TestApp_MatchStateTransitions full cycle", () => {
    const { app, ws, states } = makeApp();
    app.start();
    expect(app.state).toBe("LOBBY");

    // matchStarted (from the lobby WS) → IN_MATCH.
    ws.deliver("matchStarted", {
      matchId: "M1", gameSocketPath: "/ws/match/M1", tickRate: 30, mapSeed: 1, joinToken: "tok",
    });
    expect(app.state).toBe("IN_MATCH");

    // MatchOver decoded off the match WS → the view layer calls endMatch.
    app.endMatch();
    expect(app.state).toBe("POST_MATCH");

    // Back-to-lobby action.
    app.backToLobby();
    expect(app.state).toBe("LOBBY");

    expect(states).toEqual(["IN_MATCH", "POST_MATCH", "LOBBY"]);
  });

  test("transitions are guarded (no skips / no-ops)", () => {
    const { app } = makeApp();
    app.start();
    // endMatch from LOBBY is a no-op.
    app.endMatch();
    expect(app.state).toBe("LOBBY");
    // backToLobby from LOBBY is a no-op (no duplicate event).
    let changes = 0;
    app.onStateChange = () => changes++;
    app.backToLobby();
    expect(changes).toBe(0);
  });

  test("onMatchStarted view hook still fires alongside the transition", () => {
    const { app, ws } = makeApp();
    let gotPath = "";
    app.onMatchStarted = (ms) => { gotPath = ms.gameSocketPath; };
    app.start();
    ws.deliver("matchStarted", {
      matchId: "M2", gameSocketPath: "/ws/match/M2", tickRate: 30, mapSeed: 2, joinToken: "t2",
    });
    expect(app.state).toBe("IN_MATCH");
    expect(gotPath).toBe("/ws/match/M2");
  });
});
