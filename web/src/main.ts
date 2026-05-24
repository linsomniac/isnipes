// PHASE6.md §5.3 — top-level state machine. Handles boot,
// deep-link auto-join, and the LOBBY ↔ IN_MATCH transitions.

import { LobbyClient, type LobbyWS, parseDeepLinkRoom, type Welcome } from "./lobby.js";

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

  // UI hook fired on welcome (after any deep-link auto-join is queued).
  // The browser view layer sets this; headless callers may leave it null.
  onWelcome: ((w: Welcome) => void) | null = null;

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
  }
}
