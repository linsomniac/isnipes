// PHASE7.md §15.6 — DoD #14/#16/#17 (decode/order, end dialog winner
// from cached Scoreboard, minimap). Tab toggle (#15) and chat overlay
// (#18) DOM behavior are exercised in browser-level tests; the pure
// model/decoder math lives here.

import { describe, expect, test } from "vitest";
import {
  decodeScoreboard, decodeMatchOver, orderRows, reasonText, winnerLabel,
  buildEndDialog, appendChat, plotMinimap, CHAT_RING_CAP, type ChatLine,
  emptyHudModel,
} from "../src/hud.js";

// Build a Scoreboard (0x0B) payload:
//   u32 server_tick, u8 entry_count,
//   [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]×n
function encScoreboard(tick: number, rows: { id: number; nick: string; lives: number; score: number }[]): Uint8Array {
  const enc = new TextEncoder();
  const nickBytes = rows.map((r) => enc.encode(r.nick));
  let len = 4 + 1;
  for (const nb of nickBytes) len += 4 + 1 + nb.length + 1 + 4;
  const buf = new Uint8Array(len);
  const dv = new DataView(buf.buffer);
  let off = 0;
  dv.setUint32(off, tick, true); off += 4;
  dv.setUint8(off, rows.length); off += 1;
  rows.forEach((r, i) => {
    dv.setUint32(off, r.id, true); off += 4;
    dv.setUint8(off, nickBytes[i].length); off += 1;
    buf.set(nickBytes[i], off); off += nickBytes[i].length;
    dv.setUint8(off, r.lives); off += 1;
    dv.setInt32(off, r.score, true); off += 4;
  });
  return buf;
}

// Build a MatchOver (0x08) payload:
//   u32 final_tick, u8 reason, u32 winner_id_or_0, u8 entry_count,
//   [u32 player_id, i32 score, u8 lives_remaining]×n
function encMatchOver(tick: number, reason: number, winner: number, entries: { id: number; score: number; lives: number }[]): Uint8Array {
  const buf = new Uint8Array(4 + 1 + 4 + 1 + entries.length * 9);
  const dv = new DataView(buf.buffer);
  let off = 0;
  dv.setUint32(off, tick, true); off += 4;
  dv.setUint8(off, reason); off += 1;
  dv.setUint32(off, winner, true); off += 4;
  dv.setUint8(off, entries.length); off += 1;
  for (const e of entries) {
    dv.setUint32(off, e.id, true); off += 4;
    dv.setInt32(off, e.score, true); off += 4;
    dv.setUint8(off, e.lives); off += 1;
  }
  return buf;
}

describe("hud scoreboard", () => {
  test("TestHUD_ScoreboardDecodeAndOrder desc score, ties asc id", () => {
    const payload = encScoreboard(42, [
      { id: 7, nick: "ann", lives: 2, score: 100 },
      { id: 3, nick: "bob", lives: 1, score: 250 },
      { id: 9, nick: "cy", lives: 3, score: 100 },
    ]);
    const rows = decodeScoreboard(payload);
    expect(rows.map((r) => r.id)).toEqual([3, 7, 9]); // 250, then 100 ties asc id
    expect(rows[0].nick).toBe("bob");
    expect(rows[1].score).toBe(100);
  });

  test("decodeScoreboard handles negative scores", () => {
    const rows = decodeScoreboard(encScoreboard(1, [{ id: 1, nick: "x", lives: 0, score: -5 }]));
    expect(rows[0].score).toBe(-5);
  });

  test("decodeScoreboard rejects truncated payload", () => {
    const good = encScoreboard(1, [{ id: 1, nick: "abc", lives: 1, score: 1 }]);
    expect(() => decodeScoreboard(good.subarray(0, good.length - 2))).toThrow();
  });

  test("decodeScoreboard rejects trailing bytes", () => {
    const good = encScoreboard(1, [{ id: 1, nick: "abc", lives: 1, score: 1 }]);
    const padded = new Uint8Array(good.length + 3);
    padded.set(good);
    expect(() => decodeScoreboard(padded)).toThrow(/trailing/);
  });

  test("orderRows is stable wrt input copy (no mutation)", () => {
    const input = [{ id: 2, nick: "a", lives: 1, score: 1 }, { id: 1, nick: "b", lives: 1, score: 1 }];
    const out = orderRows(input);
    expect(out.map((r) => r.id)).toEqual([1, 2]);
    expect(input.map((r) => r.id)).toEqual([2, 1]); // input untouched
  });
});

describe("hud end-of-match", () => {
  test("TestHUD_ReasonTextEnum covers all 5 reasons", () => {
    expect(reasonText(0)).toMatch(/cleared/i);
    expect(reasonText(1)).toMatch(/standing/i);
    expect(reasonText(2)).toMatch(/eliminated/i);
    expect(reasonText(3)).toMatch(/time/i);
    expect(reasonText(4)).toMatch(/error/i);
    expect(reasonText(99)).toMatch(/99/);
  });

  test("TestHUD_EndDialogFromMatchOver resolves winner nick from cached map", () => {
    // MatchOver carries NO nicks — winner nick must come from the cached
    // Scoreboard id→nick map.
    const nickById = new Map<number, string>();
    for (const r of decodeScoreboard(encScoreboard(10, [
      { id: 3, nick: "bob", lives: 1, score: 250 },
      { id: 7, nick: "ann", lives: 0, score: 100 },
    ]))) nickById.set(r.id, r.nick);

    const mo = decodeMatchOver(encMatchOver(99, 1 /* LAST_STANDING */, 3, [
      { id: 3, score: 250, lives: 1 },
      { id: 7, score: 100, lives: 0 },
    ]));
    expect(mo.winnerId).toBe(3);
    expect(winnerLabel(mo.winnerId, nickById)).toBe("bob");
    const dlg = buildEndDialog(mo, nickById);
    expect(dlg.rows.map((r) => r.nick)).toEqual(["bob", "ann"]);
    expect(dlg.reason).toBe(1);
  });

  test("TestHUD_WinnerLabelFallback for uncached id and no-winner", () => {
    expect(winnerLabel(0, new Map())).toBe("No single winner");
    expect(winnerLabel(5, new Map())).toBe("Player 5");
  });

  test("decodeMatchOver rejects trailing bytes (incl. zero-entry)", () => {
    const good = encMatchOver(1, 2, 0, []); // zero entries
    const padded = new Uint8Array(good.length + 4);
    padded.set(good);
    expect(() => decodeMatchOver(padded)).toThrow(/trailing/);
    // a well-formed zero-entry frame still decodes.
    expect(decodeMatchOver(good).entries).toHaveLength(0);
  });
});

describe("hud chat ring", () => {
  test("appendChat trims to CHAT_RING_CAP keeping newest", () => {
    let chat: ChatLine[] = [];
    for (let i = 0; i < CHAT_RING_CAP + 10; i++) {
      chat = appendChat(chat, { fromId: 1, fromNick: "x", text: `m${i}` });
    }
    expect(chat.length).toBe(CHAT_RING_CAP);
    expect(chat[chat.length - 1].text).toBe(`m${CHAT_RING_CAP + 9}`);
  });
});

describe("hud minimap", () => {
  test("TestHUD_MinimapPlotsEntities within rect, self flagged", () => {
    const rect = { x: 500, y: 380, w: 100, h: 80 };
    const worldW = 1000, worldH = 800;
    const pts = plotMinimap(rect, worldW, worldH, 500, 400, [{ x: 0, y: 0 }, { x: 1000, y: 800 }]);
    expect(pts[0].self).toBe(true);
    expect(pts[0].px).toBeCloseTo(550, 5); // 500 + 500/1000*100
    expect(pts[0].py).toBeCloseTo(420, 5); // 380 + 400/800*80
    // corner entity at world origin maps to rect origin.
    expect(pts[1].px).toBeCloseTo(500, 5);
    expect(pts[1].py).toBeCloseTo(380, 5);
    // all points within the rect bounds.
    for (const p of pts) {
      expect(p.px).toBeGreaterThanOrEqual(rect.x);
      expect(p.px).toBeLessThanOrEqual(rect.x + rect.w);
      expect(p.py).toBeGreaterThanOrEqual(rect.y);
      expect(p.py).toBeLessThanOrEqual(rect.y + rect.h);
    }
  });
});

test("emptyHudModel has a null respawnCountdown", () => {
  expect(emptyHudModel().respawnCountdown).toBeNull();
});
