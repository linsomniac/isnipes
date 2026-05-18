// PHASE6.md §16.7 — vitest coverage for the TS lobby client.

import { describe, expect, test } from "vitest";
import {
  LobbyClient,
  parseDeepLinkRoom,
  type LobbyWS,
} from "../src/lobby.js";
import { App } from "../src/main.js";

// FakeWS records sent frames and exposes a push() to simulate inbound.
class FakeWS implements LobbyWS {
  sent: string[] = [];
  onmessage: ((d: string) => void) | null = null;
  onclose: (() => void) | null = null;
  onopen: (() => void) | null = null;
  send(d: string): void {
    this.sent.push(d);
  }
  close(): void {
    if (this.onclose) this.onclose();
  }
  push(envelope: { t: string; v?: number; d: unknown }): void {
    if (this.onmessage)
      this.onmessage(JSON.stringify({ v: 1, ...envelope }));
  }
}

describe("parseDeepLinkRoom", () => {
  test("accepts 6-char base32 codes case-insensitively", () => {
    expect(parseDeepLinkRoom("?room=ABCD23")).toBe("ABCD23");
    expect(parseDeepLinkRoom("?room=abcd23")).toBe("ABCD23");
    expect(parseDeepLinkRoom("?room=Z9MN44")).toBe("Z9MN44");
  });
  test("rejects malformed or absent codes", () => {
    expect(parseDeepLinkRoom("")).toBe(null);
    expect(parseDeepLinkRoom("?foo=bar")).toBe(null);
    expect(parseDeepLinkRoom("?room=")).toBe(null);
    expect(parseDeepLinkRoom("?room=AB")).toBe(null); // too short
    expect(parseDeepLinkRoom("?room=ABCDEFG")).toBe(null); // too long
    expect(parseDeepLinkRoom("?room=!!!!!!")).toBe(null);
    expect(parseDeepLinkRoom("?room=<script>")).toBe(null); // XSS guard
  });
});

describe("LobbyClient envelope dispatch", () => {
  test("welcome event fires onWelcome", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    let got: any = null;
    c.onWelcome = (w) => (got = w);
    c.attach(ws, "Alice", "v0", 0x42607394);
    ws.push({ t: "welcome", d: { playerId: "p1", nick: "Alice", serverVersion: "v0", schemaChecksum: 0x42607394, motd: "" } });
    expect(got?.nick).toBe("Alice");
  });

  test("roomList replaces the local map; subsequent deltas mutate", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    let last: any = null;
    c.onRoomListChange = (rs) => (last = rs);
    c.attach(ws, "Alice", "v0", 0);

    ws.push({ t: "roomList", d: { rooms: [{ id: "R1", name: "Room1", players: 1, max: 4, mode: "ffa", level: { letter: "A", number: 1 }, state: "OPEN" }] } });
    expect(c.rooms()).toHaveLength(1);
    expect(last).toHaveLength(1);

    ws.push({ t: "room_added", d: { room: { id: "R2", name: "Room2", players: 2, max: 4, mode: "ffa", level: { letter: "B", number: 3 }, state: "OPEN" } } });
    expect(c.rooms()).toHaveLength(2);

    ws.push({ t: "room_updated", d: { room: { id: "R1", name: "Room1", players: 3, max: 4, mode: "ffa", level: { letter: "A", number: 1 }, state: "OPEN" } } });
    expect(c.rooms().find((r) => r.id === "R1")?.players).toBe(3);

    ws.push({ t: "room_removed", d: { roomId: "R2" } });
    expect(c.rooms()).toHaveLength(1);
    expect(c.rooms().find((r) => r.id === "R2")).toBeUndefined();
  });

  test("chat_relay → onChatRelay", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    let got: any = null;
    c.onChatRelay = (m) => (got = m);
    c.attach(ws, "Alice", "v0", 0);
    ws.push({ t: "chat_relay", d: { roomId: "R1", fromNick: "Bob", text: "hi", ts: 1 } });
    expect(got?.text).toBe("hi");
  });

  test("matchStarted → onMatchStarted", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    let got: any = null;
    c.onMatchStarted = (m) => (got = m);
    c.attach(ws, "Alice", "v0", 0);
    ws.push({ t: "matchStarted", d: { matchId: "M1", gameSocketPath: "/ws/match/M1", tickRate: 30, mapSeed: 1, joinToken: "tokA" } });
    expect(got?.matchId).toBe("M1");
  });

  test("error envelope → onError", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    let got: any = null;
    c.onError = (e) => (got = e);
    c.attach(ws, "Alice", "v0", 0);
    ws.push({ t: "error", d: { code: "BAD_LEVEL", message: "bad" } });
    expect(got?.code).toBe("BAD_LEVEL");
  });
});

describe("LobbyClient outbound", () => {
  test("createRoom encodes the v=1 envelope", () => {
    const c = new LobbyClient();
    const ws = new FakeWS();
    c.attach(ws, "Alice", "v0", 0);
    c.createRoom({ name: "R", max: 4, level: { letter: "A", number: 1 } });
    expect(ws.sent.length).toBe(1);
    const env = JSON.parse(ws.sent[0]);
    expect(env.t).toBe("createRoom");
    expect(env.v).toBe(1);
    expect(env.d.name).toBe("R");
  });
});

describe("App deep-link auto-join (DoD #15)", () => {
  test("TestLobbyClient_DeepLinkAutoJoin", () => {
    const ws = new FakeWS();
    const app = new App({
      openLobbyWS: () => ws,
      locationSearch: () => "?room=ABCD23",
      getStoredNick: () => "Alice",
      schemaChecksum: 0x42607394,
      clientVersion: "v0",
    });
    app.start();
    // Server sends welcome; deep-link triggers auto joinRoom.
    ws.push({ t: "welcome", d: { playerId: "p1", nick: "Alice", serverVersion: "v0", schemaChecksum: 0x42607394, motd: "" } });
    // The client should have emitted exactly one joinRoom frame with
    // the upper-cased room code.
    const joinSent = ws.sent.find((s) => JSON.parse(s).t === "joinRoom");
    expect(joinSent).toBeDefined();
    const env = JSON.parse(joinSent!);
    expect(env.d.roomId).toBe("ABCD23");
  });

  test("no auto-join when ?room is absent", () => {
    const ws = new FakeWS();
    const app = new App({
      openLobbyWS: () => ws,
      locationSearch: () => "",
      getStoredNick: () => "Alice",
      schemaChecksum: 0,
      clientVersion: "v0",
    });
    app.start();
    ws.push({ t: "welcome", d: { playerId: "p1", nick: "Alice", serverVersion: "v0", schemaChecksum: 0, motd: "" } });
    const join = ws.sent.find((s) => JSON.parse(s).t === "joinRoom");
    expect(join).toBeUndefined();
  });

  test("malformed ?room is ignored", () => {
    const ws = new FakeWS();
    const app = new App({
      openLobbyWS: () => ws,
      locationSearch: () => "?room=BAD!!!",
      getStoredNick: () => "Alice",
      schemaChecksum: 0,
      clientVersion: "v0",
    });
    app.start();
    ws.push({ t: "welcome", d: { playerId: "p1", nick: "Alice", serverVersion: "v0", schemaChecksum: 0, motd: "" } });
    const join = ws.sent.find((s) => JSON.parse(s).t === "joinRoom");
    expect(join).toBeUndefined();
  });
});
