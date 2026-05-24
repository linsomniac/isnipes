// PHASE6.md §9 / §16.8 — browser entry point. Builds a minimal but real
// lobby + match-view DOM and wires it to the headless App / LobbyClient
// (whose logic is unit-tested in tests/lobby.test.ts). This file is the
// only browser-only module; it is bundled to dist/app.js and never
// imported by vitest, so DOM access here is safe.

import { App } from "./main.js";
import type { LobbyWS, MatchStarted, RoomDescriptor } from "./lobby.js";
import { computeSchemaChecksum } from "./proto.js";
import { NetClient, type WebSocketLike } from "./netClient.js";

const NICK_KEY = "nick";
const CLIENT_VERSION = "v0";

// getStoredNick returns localStorage.nick, generating + persisting a
// default when absent. §9.1 calls for a prompt; we auto-assign instead so
// the client never blocks on a modal (better for headless/e2e and for a
// first-load UX). A nick-edit field is a Phase 7 polish item.
function getStoredNick(): string {
  let n = "";
  try {
    n = localStorage.getItem(NICK_KEY) ?? "";
  } catch {
    n = "";
  }
  if (n === "") {
    n = "Player-" + Math.random().toString(36).slice(2, 7);
    try {
      localStorage.setItem(NICK_KEY, n);
    } catch {
      /* localStorage unavailable; in-memory nick is fine */
    }
  }
  return n;
}

function wsBase(): string {
  const scheme = location.protocol === "https:" ? "wss://" : "ws://";
  return scheme + location.host;
}

// BrowserLobbyWS adapts the browser WebSocket to the LobbyWS surface the
// LobbyClient expects (text frames, string callbacks).
class BrowserLobbyWS implements LobbyWS {
  private ws: WebSocket;
  onmessage: ((data: string) => void) | null = null;
  onclose: (() => void) | null = null;
  onopen: (() => void) | null = null;

  constructor(url: string) {
    this.ws = new WebSocket(url);
    this.ws.onopen = () => this.onopen?.();
    this.ws.onclose = () => this.onclose?.();
    this.ws.onmessage = (ev: MessageEvent) => {
      if (typeof ev.data === "string") this.onmessage?.(ev.data);
    };
  }

  send(data: string): void {
    this.ws.send(data);
  }

  close(): void {
    this.ws.close();
  }
}

// ---- DOM scaffold ----

interface UI {
  connecting: HTMLElement;
  lobby: HTMLElement;
  createBtn: HTMLButtonElement;
  myRoom: HTMLElement;
  myRoomId: HTMLElement;
  myRoomPlayers: HTMLElement;
  startBtn: HTMLButtonElement;
  roomList: HTMLElement;
  match: HTMLElement;
  status: HTMLElement;
}

function buildDOM(): UI {
  document.body.innerHTML = "";

  const status = el("p", { id: "status", "data-testid": "status" }, "");
  const connecting = el("section", { id: "connecting", "data-testid": "connecting" }, "Connecting…");

  const createBtn = el("button", { id: "create-room", "data-testid": "create-room" }, "Create Room") as HTMLButtonElement;

  const myRoomId = el("span", { "data-testid": "my-room-id" }, "");
  const myRoomPlayers = el("span", { "data-testid": "room-players" }, "");
  const startBtn = el("button", { id: "start-btn", "data-testid": "start-btn" }, "Start") as HTMLButtonElement;
  startBtn.disabled = true;
  const myRoom = el("div", { id: "my-room", "data-testid": "my-room", hidden: "true" }, "");
  myRoom.append(text("Your room: "), myRoomId, text(" ("), myRoomPlayers, text(")  "), startBtn);

  const roomList = el("ul", { id: "room-list", "data-testid": "room-list" }, "");

  const lobby = el("section", { id: "lobby", "data-testid": "lobby", hidden: "true" }, "");
  lobby.append(el("h1", {}, "Lobby"), createBtn, myRoom, el("h2", {}, "Rooms"), roomList);

  const canvas = el("canvas", { id: "game", width: "640", height: "480" }, "");
  const matchView = el("div", { "data-testid": "match-view" }, "");
  matchView.append(el("h1", {}, "In Match"), canvas);
  const match = el("section", { id: "match", hidden: "true" }, "");
  match.append(matchView);

  document.body.append(status, connecting, lobby, match);
  return { connecting, lobby, createBtn, myRoom, myRoomId, myRoomPlayers, startBtn, roomList, match, status };
}

function el(tag: string, attrs: Record<string, string>, txt: string): HTMLElement {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "hidden") {
      e.hidden = true;
    } else {
      e.setAttribute(k, v);
    }
  }
  if (txt) e.textContent = txt;
  return e;
}

function text(s: string): Text {
  return document.createTextNode(s);
}

// ---- match-WS (best-effort; the view does not depend on it) ----

function connectMatch(ms: MatchStarted, schemaChecksum: number): void {
  try {
    const ws = new WebSocket(wsBase() + ms.gameSocketPath);
    ws.binaryType = "arraybuffer";
    const nc = new NetClient(ws as unknown as WebSocketLike);
    nc.setEvents({ onOpen: () => nc.sendMatchJoin(schemaChecksum, ms.joinToken) });
    nc.start();
  } catch {
    /* the match-view wrapper is already shown; rendering comes in Phase 7 */
  }
}

// ---- boot ----

async function boot(): Promise<void> {
  const ui = buildDOM();
  const schemaChecksum = await computeSchemaChecksum();

  const app = new App({
    openLobbyWS: () => new BrowserLobbyWS(wsBase() + "/ws/lobby"),
    locationSearch: () => location.search,
    getStoredNick,
    schemaChecksum,
    clientVersion: CLIENT_VERSION,
  });

  let myRoomId: string | null = null;
  let pendingCreateName: string | null = null;
  let preCreateIds = new Set<string>();

  ui.createBtn.onclick = () => {
    const name = "Room-" + Math.random().toString(36).slice(2, 6);
    pendingCreateName = name;
    preCreateIds = new Set(app.lobby.rooms().map((r) => r.id));
    app.lobby.createRoom({ name, max: 4, level: { letter: "A", number: 1 } });
  };

  ui.startBtn.onclick = () => {
    if (myRoomId !== null) app.lobby.startMatch(myRoomId);
  };

  app.onWelcome = () => {
    ui.connecting.hidden = true;
    ui.lobby.hidden = false;
  };

  app.lobby.onRoomListChange = (rooms: RoomDescriptor[]) => {
    if (myRoomId === null && pendingCreateName !== null) {
      const mine = rooms.find((r) => r.name === pendingCreateName && !preCreateIds.has(r.id));
      if (mine) {
        myRoomId = mine.id;
        pendingCreateName = null;
        ui.myRoomId.textContent = mine.id;
        ui.myRoom.hidden = false;
      }
    }
    renderRooms(ui.roomList, rooms);
    if (myRoomId !== null) {
      const mine = rooms.find((r) => r.id === myRoomId);
      if (mine) {
        ui.myRoomPlayers.textContent = `${mine.players}/${mine.max}`;
        // §6: a match needs ≥2 players (NO_OPPONENT otherwise).
        ui.startBtn.disabled = mine.players < 2;
      }
    }
  };

  app.lobby.onMatchStarted = (ms: MatchStarted) => {
    ui.lobby.hidden = true;
    ui.match.hidden = false;
    connectMatch(ms, schemaChecksum);
  };

  app.lobby.onError = (e) => {
    ui.status.textContent = `error: ${e.code} ${e.message}`;
  };

  app.start();
}

function renderRooms(listEl: HTMLElement, rooms: RoomDescriptor[]): void {
  listEl.innerHTML = "";
  for (const r of rooms) {
    const li = el("li", { "data-testid": "room-row", "data-room-id": r.id }, "");
    li.textContent = `${r.id} — ${r.name} (${r.players}/${r.max}) [${r.state}]`;
    listEl.append(li);
  }
}

void boot();
