// PHASE6.md §5.3 — top-level state machine. Handles boot,
// deep-link auto-join, and the LOBBY ↔ IN_MATCH transitions.

import { LobbyClient, type LobbyWS, parseDeepLinkRoom } from "./lobby.js";

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
    if (deepLinkRoom !== null) {
      this.lobby.onWelcome = () => {
        // Auto-join immediately on welcome.
        this.lobby.joinRoom(deepLinkRoom);
      };
    }
  }
}
