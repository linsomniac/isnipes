// PHASE7.md §8 — HUD model + pure decoders. The binary Scoreboard
// (0x0B) and MatchOver (0x08) frames are decoded here (proto.ts is
// frozen and does not decode them). The DOM/canvas overlay rendering is
// wired in browser.ts; this module holds the testable model + math.

import { ProtocolError } from "./proto.js";

export interface ScoreRow {
  id: number;
  nick: string;
  lives: number;
  score: number;
}

export interface ChatLine {
  fromId: number;
  fromNick: string;
  text: string;
}

export interface EndDialog {
  reason: number;
  winnerId: number;
  rows: ScoreRow[];
}

export interface HudModel {
  hp: number;
  lives: number;
  score: number;
  rows: ScoreRow[]; // ordered desc score, asc id
  nickById: Map<number, string>; // cached from the latest Scoreboard
  showScoreboard: boolean;
  chat: ChatLine[]; // ring buffer, newest last
  deadCam: boolean;
  endDialog: EndDialog | null;
}

export const CHAT_RING_CAP = 50;

export function emptyHudModel(): HudModel {
  return {
    hp: 0, lives: 0, score: 0,
    rows: [], nickById: new Map(),
    showScoreboard: false, chat: [], deadCam: false, endDialog: null,
  };
}

// orderRows sorts desc by score, ties asc by id (§8.2).
export function orderRows(rows: readonly ScoreRow[]): ScoreRow[] {
  return [...rows].sort((a, b) => (b.score - a.score) || (a.id - b.id));
}

// decodeScoreboard parses a Scoreboard (0x0B) payload:
//   u32 server_tick, u8 entry_count,
//   [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]×n
// Returns rows ordered desc score, asc id.
export function decodeScoreboard(payload: Uint8Array): ScoreRow[] {
  const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  if (payload.length < 5) throw new ProtocolError("scoreboard too short");
  let off = 4; // skip server_tick
  const count = dv.getUint8(off); off += 1;
  const rows: ScoreRow[] = [];
  const dec = new TextDecoder();
  for (let i = 0; i < count; i++) {
    if (off + 4 + 1 > payload.length) throw new ProtocolError("scoreboard truncated (id/len)");
    const id = dv.getUint32(off, true); off += 4;
    const nickLen = dv.getUint8(off); off += 1;
    if (off + nickLen + 1 + 4 > payload.length) throw new ProtocolError("scoreboard truncated (nick/score)");
    const nick = dec.decode(payload.subarray(off, off + nickLen)); off += nickLen;
    const lives = dv.getUint8(off); off += 1;
    const score = dv.getInt32(off, true); off += 4;
    rows.push({ id, nick, lives, score });
  }
  return orderRows(rows);
}

export interface MatchOverData {
  finalTick: number;
  reason: number;
  winnerId: number;
  entries: { id: number; score: number; lives: number }[];
}

// decodeMatchOver parses a MatchOver (0x08) payload:
//   u32 final_tick, u8 reason, u32 winner_id_or_0, u8 entry_count,
//   [u32 player_id, i32 score, u8 lives_remaining]×n
// Note: MatchOver carries NO nicks — the caller joins entries against
// the cached Scoreboard nickById map (§8.4).
export function decodeMatchOver(payload: Uint8Array): MatchOverData {
  const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  if (payload.length < 10) throw new ProtocolError("matchover too short");
  let off = 0;
  const finalTick = dv.getUint32(off, true); off += 4;
  const reason = dv.getUint8(off); off += 1;
  const winnerId = dv.getUint32(off, true); off += 4;
  const count = dv.getUint8(off); off += 1;
  const entries: { id: number; score: number; lives: number }[] = [];
  for (let i = 0; i < count; i++) {
    if (off + 9 > payload.length) throw new ProtocolError("matchover truncated");
    const id = dv.getUint32(off, true); off += 4;
    const score = dv.getInt32(off, true); off += 4;
    const lives = dv.getUint8(off); off += 1;
    entries.push({ id, score, lives });
  }
  return { finalTick, reason, winnerId, entries };
}

// §3.8.1 end-reason enum → prose.
const REASON_TEXT: Record<number, string> = {
  0: "Level cleared",
  1: "Last one standing",
  2: "All players eliminated",
  3: "Time expired",
  4: "Server error",
};

export function reasonText(reason: number): string {
  return REASON_TEXT[reason] ?? `Match ended (${reason})`;
}

// winnerLabel resolves the winner's display name from the cached
// Scoreboard nick map. winnerId 0 → no single winner (§8.4). Falls back
// to "Player <id>" when the nick was never cached.
export function winnerLabel(winnerId: number, nickById: Map<number, string>): string {
  if (winnerId === 0) return "No single winner";
  return nickById.get(winnerId) ?? `Player ${winnerId}`;
}

// buildEndDialog joins a decoded MatchOver against the cached nick map.
export function buildEndDialog(mo: MatchOverData, nickById: Map<number, string>): EndDialog {
  const rows: ScoreRow[] = mo.entries.map((e) => ({
    id: e.id,
    nick: nickById.get(e.id) ?? `Player ${e.id}`,
    lives: e.lives,
    score: e.score,
  }));
  return { reason: mo.reason, winnerId: mo.winnerId, rows: orderRows(rows) };
}

// appendChat pushes a line into the ring buffer, trimming to CHAT_RING_CAP.
export function appendChat(chat: ChatLine[], line: ChatLine): ChatLine[] {
  const next = [...chat, line];
  return next.length > CHAT_RING_CAP ? next.slice(next.length - CHAT_RING_CAP) : next;
}

// ---- minimap (§8.3) ----

export interface MinimapRect {
  x: number; y: number; w: number; h: number; // px in the canvas
}

export interface MinimapPoint {
  px: number; py: number; self: boolean;
}

// plotMinimap maps world-subtile entity coords into the minimap rect.
// worldW/worldH are in subtile units (maze.W*SUBTILE_PER_TILE, ...).
export function plotMinimap(
  rect: MinimapRect,
  worldW: number,
  worldH: number,
  selfX: number,
  selfY: number,
  entities: readonly { x: number; y: number }[],
): MinimapPoint[] {
  const sx = rect.w / worldW;
  const sy = rect.h / worldH;
  const pts: MinimapPoint[] = [];
  pts.push({ px: rect.x + selfX * sx, py: rect.y + selfY * sy, self: true });
  for (const e of entities) {
    pts.push({ px: rect.x + e.x * sx, py: rect.y + e.y * sy, self: false });
  }
  return pts;
}
