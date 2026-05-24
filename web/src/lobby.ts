// PHASE6.md §5.3 — minimal TypeScript lobby client. Handles the
// envelope decode/dispatch + per-event handlers; deep-link auto-join
// is driven by main.ts which calls connect/joinRoom in sequence.

// ---- protocol types (mirror internal/proto/lobby.go) ----

export interface Envelope {
  t: string;
  v: number;
  d: unknown;
}

export interface Hello {
  nick: string;
  clientVersion: string;
  schemaChecksum: number;
}

export interface Welcome {
  playerId: string;
  nick: string;
  serverVersion: string;
  schemaChecksum: number;
  motd: string;
}

export interface Level {
  letter: string;
  number: number;
}

export interface RoomDescriptor {
  id: string;
  name: string;
  players: number;
  max: number;
  mode: string;
  level: Level;
  state: string;
}

export interface RoomList {
  rooms: RoomDescriptor[];
}

export interface CreateRoom {
  name: string;
  max: number;
  level: Level;
}

export interface JoinRoom {
  roomId: string;
}

export interface StartMatch {
  roomId: string;
}

export interface MatchStarted {
  matchId: string;
  gameSocketPath: string;
  tickRate: number;
  mapSeed: number;
  joinToken: string;
}

export interface ChatPayload {
  roomId: string;
  text: string;
}

export interface ChatRelay {
  roomId: string;
  fromNick: string;
  text: string;
  ts: number;
}

export interface LobbyError {
  code: string;
  message: string;
}

export interface RoomDelta {
  room: RoomDescriptor;
}

export interface RoomRemoved {
  roomId: string;
}

// LevelPreset — PHASE6 §8.2 preview metadata (PHASE7 §10.4 picker). The
// wire `letter` is a Go byte → a JSON number (65='A'); we expose it as a
// single-char string.
export interface LevelPreset {
  letter: string;
  number: number;
  difficulty: string;
  playerLives: number;
  generators: number;
  maxSnipes: number;
  description: string;
}

interface LevelPresetWire {
  letter: number;
  number: number;
  difficulty: string;
  playerLives: number;
  generators: number;
  maxSnipes: number;
  description: string;
}

// parseLevelPresets converts a level_presets envelope's `d` into
// LevelPreset[], mapping the numeric letter code to a character.
export function parseLevelPresets(d: unknown): LevelPreset[] {
  const presets = (d as { presets?: LevelPresetWire[] })?.presets;
  if (!Array.isArray(presets)) return [];
  return presets.map((w) => ({
    letter: String.fromCharCode(w.letter),
    number: w.number,
    difficulty: w.difficulty,
    playerLives: w.playerLives,
    generators: w.generators,
    maxSnipes: w.maxSnipes,
    description: w.description,
  }));
}

// ---- WebSocket-like abstraction (mirrors netClient.ts pattern) ----

export interface LobbyWS {
  send(data: string): void;
  close(): void;
  onmessage: ((data: string) => void) | null;
  onclose: (() => void) | null;
  onopen: (() => void) | null;
}

// ---- client ----

export type LobbyEventHandler<T> = (payload: T) => void;

export class LobbyClient {
  private ws: LobbyWS | null = null;
  private nick = "";
  private roomList: Map<string, RoomDescriptor> = new Map();

  // event handlers
  onWelcome: LobbyEventHandler<Welcome> | null = null;
  onRoomListChange: LobbyEventHandler<RoomDescriptor[]> | null = null;
  onChatRelay: LobbyEventHandler<ChatRelay> | null = null;
  onMatchStarted: LobbyEventHandler<MatchStarted> | null = null;
  onLevelPresets: LobbyEventHandler<LevelPreset[]> | null = null;
  onError: LobbyEventHandler<LobbyError> | null = null;

  // attach binds the client to a WebSocket-like and sends hello.
  attach(ws: LobbyWS, nick: string, clientVersion: string, schemaChecksum: number): void {
    this.ws = ws;
    this.nick = nick;
    ws.onmessage = (data: string) => this.handle(data);
    ws.onopen = () => this.send("hello", { nick, clientVersion, schemaChecksum });
    // If the socket is already open at attach time, fire the hello now.
    // The test harness simulates this by calling attach AFTER setting
    // ws.onopen synchronously.
  }

  // sendHello is exposed for tests that don't drive onopen.
  sendHello(clientVersion: string, schemaChecksum: number): void {
    this.send("hello", { nick: this.nick, clientVersion, schemaChecksum });
  }

  createRoom(c: CreateRoom): void {
    this.send("createRoom", c);
  }

  joinRoom(roomId: string): void {
    this.send("joinRoom", { roomId });
  }

  leaveRoom(): void {
    this.send("leaveRoom", {});
  }

  startMatch(roomId: string): void {
    this.send("startMatch", { roomId });
  }

  sendChat(roomId: string, text: string): void {
    this.send("chat", { roomId, text });
  }

  kick(roomId: string, sessionId: string): void {
    this.send("kick", { roomId, sessionId });
  }

  rooms(): RoomDescriptor[] {
    return Array.from(this.roomList.values());
  }

  private send(t: string, d: unknown): void {
    if (!this.ws) return;
    const env: Envelope = { t, v: 1, d };
    this.ws.send(JSON.stringify(env));
  }

  private handle(raw: string): void {
    let env: Envelope;
    try {
      env = JSON.parse(raw) as Envelope;
    } catch {
      return;
    }
    switch (env.t) {
      case "welcome":
        if (this.onWelcome) this.onWelcome(env.d as Welcome);
        break;
      case "roomList": {
        const rl = env.d as RoomList;
        this.roomList = new Map(rl.rooms.map((r) => [r.id, r]));
        this.fireRoomListChange();
        break;
      }
      case "room_added":
      case "room_updated": {
        const delta = env.d as RoomDelta;
        this.roomList.set(delta.room.id, delta.room);
        this.fireRoomListChange();
        break;
      }
      case "room_removed": {
        const rr = env.d as RoomRemoved;
        this.roomList.delete(rr.roomId);
        this.fireRoomListChange();
        break;
      }
      case "chat_relay":
        if (this.onChatRelay) this.onChatRelay(env.d as ChatRelay);
        break;
      case "matchStarted":
        if (this.onMatchStarted) this.onMatchStarted(env.d as MatchStarted);
        break;
      case "level_presets":
        if (this.onLevelPresets) this.onLevelPresets(parseLevelPresets(env.d));
        break;
      case "error":
        if (this.onError) this.onError(env.d as LobbyError);
        break;
      // Unknown types are ignored per §11.5.
    }
  }

  private fireRoomListChange(): void {
    if (this.onRoomListChange) this.onRoomListChange(this.rooms());
  }
}

// ---- deep-link auto-join helper (PHASE6.md §9) ----

// parseDeepLinkRoom parses a query string for `room=XXXXXX`. Returns
// the upper-cased 6-char room code or null if absent/invalid.
export function parseDeepLinkRoom(search: string): string | null {
  const params = new URLSearchParams(search);
  const raw = params.get("room");
  if (!raw) return null;
  const code = raw.toUpperCase();
  if (!/^[A-Z0-9]{6}$/.test(code)) return null;
  return code;
}
