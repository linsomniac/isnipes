// PHASE6.md §5.3 — top-level state machine. Handles boot,
// deep-link auto-join, and the LOBBY ↔ IN_MATCH transitions.

import { LobbyClient, type LobbyWS, parseDeepLinkRoom, type Welcome, type MatchStarted } from "./lobby.js";

export type AppState = "LOBBY" | "IN_MATCH" | "POST_MATCH";

export interface AppDeps {
  // Factory for the lobby WebSocket; injected in tests with a fake.
  openLobbyWS: () => LobbyWS;
  // Read window.location.search; injected in tests.
  locationSearch: () => string;
  // Read/write localStorage.nick (returns "" if absent).
  getStoredNick: () => string;
  // Schema checksum the client mirrors on connect.
  schemaChecksum: number;
  // ClientVersion identifier sent in hello.
  clientVersion: string;
}

export class App {
  private deps: AppDeps;
  state: AppState = "LOBBY";
  lobby: LobbyClient;

  // UI hooks. The browser view layer sets these; headless callers may
  // leave them null.
  onWelcome: ((w: Welcome) => void) | null = null;
  onMatchStarted: ((ms: MatchStarted) => void) | null = null;
  onStateChange: ((s: AppState) => void) | null = null;

  constructor(deps: AppDeps) {
    this.deps = deps;
    this.lobby = new LobbyClient();
  }

  // start opens the lobby WS, sends hello, and (if a deep-link is
  // present) auto-joins after welcome lands.
  start(): void {
    const ws = this.deps.openLobbyWS();
    const nick = this.deps.getStoredNick();
    const deepLinkRoom = parseDeepLinkRoom(this.deps.locationSearch());
    this.lobby.attach(ws, nick, this.deps.clientVersion, this.deps.schemaChecksum);
    this.lobby.onWelcome = (w) => {
      if (deepLinkRoom !== null) {
        // Auto-join immediately on welcome, before the UI hook runs so
        // the join frame is in flight as the lobby view appears.
        this.lobby.joinRoom(deepLinkRoom);
      }
      this.onWelcome?.(w);
    };
    // App owns the LOBBY→IN_MATCH transition; the view layer's match
    // connect logic runs via onMatchStarted (mirrors the onWelcome hook).
    this.lobby.onMatchStarted = (ms) => {
      this.toMatch(ms);
      this.onMatchStarted?.(ms);
    };
  }

  private setState(s: AppState): void {
    if (this.state === s) return;
    this.state = s;
    this.onStateChange?.(s);
  }

  // toMatch: LOBBY → IN_MATCH on matchStarted.
  toMatch(_ms?: MatchStarted): void {
    if (this.state === "LOBBY") this.setState("IN_MATCH");
  }

  // endMatch: IN_MATCH → POST_MATCH on MatchOver (called by the view
  // layer when it decodes the 0x08 frame off the match WS).
  endMatch(): void {
    if (this.state === "IN_MATCH") this.setState("POST_MATCH");
  }

  // backToLobby: POST_MATCH → LOBBY on the end-dialog back action.
  backToLobby(): void {
    this.setState("LOBBY");
  }
}
