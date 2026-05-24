// PHASE4.md §18.10 — NetClient WS scaffolding + Ping/Pong/OWT
// behaviour. Uses a fake WS + fake clock so timers are deterministic.

import { describe, expect, test } from "vitest";
import {
  NetClient,
  PING_INTERVAL_MS,
  type ClockLike,
  type WebSocketLike,
} from "../src/netClient.js";
import { encodeFrame, MsgType } from "../src/proto.js";

class FakeClock implements ClockLike {
  now = 0;
  intervals: Array<{ id: number; fn: () => void; ms: number }> = [];
  nextID = 1;
  nowMs(): number {
    return this.now;
  }
  setInterval(fn: () => void, ms: number): any {
    const id = this.nextID++;
    this.intervals.push({ id, fn, ms });
    return id;
  }
  clearInterval(id: any): void {
    this.intervals = this.intervals.filter((i) => i.id !== id);
  }
  advance(ms: number): void {
    const end = this.now + ms;
    // Repeatedly fire ticks until we've consumed the window.
    while (this.now < end) {
      let next = end;
      for (const iv of this.intervals) {
        const fireAt = this.now + iv.ms;
        if (fireAt < next) next = fireAt;
      }
      this.now = next;
      // Fire ALL intervals whose period divides the elapsed time.
      // Approximation: fire any interval whose ms <= remaining-step.
      for (const iv of this.intervals) {
        if (this.now % iv.ms === 0) iv.fn();
      }
    }
  }
}

class FakeWS implements WebSocketLike {
  readyState = 1;
  sent: Uint8Array[] = [];
  onopen: any = null;
  onclose: any = null;
  onerror: any = null;
  onmessage: any = null;
  send(data: ArrayBuffer | Uint8Array): void {
    if (data instanceof Uint8Array) {
      this.sent.push(new Uint8Array(data));
    } else {
      this.sent.push(new Uint8Array(data));
    }
  }
  close(_code?: number, _reason?: string): void {
    this.readyState = 3;
    if (this.onclose) this.onclose({ code: _code ?? 1000, reason: _reason ?? "" });
  }
  deliver(payload: Uint8Array): void {
    if (this.onmessage) this.onmessage({ data: payload });
  }
}

describe("NetClient", () => {
  test("sendMatchJoin emits a MatchJoin frame", () => {
    const ws = new FakeWS();
    const clk = new FakeClock();
    const c = new NetClient(ws, clk);
    c.sendMatchJoin(0x42607394, "tok123");
    expect(ws.sent).toHaveLength(1);
    // First byte = frame.type = MsgType.MatchJoin (0x00).
    expect(ws.sent[0][0]).toBe(MsgType.MatchJoin);
    // The token must be the UTF-8 bytes of "tok123" — not a zeroed
    // buffer (which a string→Uint8Array#set coercion would produce and
    // which the server would reject with AUTH). Frame layout: 8-byte
    // header, then MatchJoin payload {u32 schema, u8 token_len, token}.
    const HDR = 8;
    const frame = ws.sent[0];
    const tokenLen = frame[HDR + 4];
    expect(tokenLen).toBe(6);
    const token = frame.slice(HDR + 5, HDR + 5 + tokenLen);
    expect(Array.from(token)).toEqual(Array.from(new TextEncoder().encode("tok123")));
  });

  test("Ping ticker emits at 500ms intervals", () => {
    const ws = new FakeWS();
    const clk = new FakeClock();
    const c = new NetClient(ws, clk);
    c.start();
    // Advance 2 seconds — expect 4 Ping frames.
    clk.advance(2000);
    const pings = ws.sent.filter((f) => f[0] === MsgType.Ping);
    expect(pings.length).toBeGreaterThanOrEqual(3);
    expect(pings.length).toBeLessThanOrEqual(5);
  });

  test("observePong drives OWT EWMA", () => {
    const ws = new FakeWS();
    const clk = new FakeClock();
    clk.now = 1000;
    const c = new NetClient(ws, clk);
    c.observePong(900, 950); // RTT = 100, OWT = 50
    expect(c.owtMs()).toBe(50);
    // Subsequent ObservePong moves EWMA by α=0.2.
    c.observePong(900, 950); // same sample → stays at 50
    expect(c.owtMs()).toBe(50);
  });

  test("server-initiated Ping is echoed as Pong", () => {
    const ws = new FakeWS();
    const clk = new FakeClock();
    clk.now = 2000;
    const c = new NetClient(ws, clk);
    c.start();
    // Server-originated Ping frame with ts_origin = 1234.
    const payload = new Uint8Array(4);
    new DataView(payload.buffer).setUint32(0, 1234, true);
    const frame = encodeFrame(
      { type: MsgType.Ping, flags: 0, seq: 0, ack: 0xffff, len: 4 },
      payload,
    );
    ws.deliver(frame);
    // The auto-Pong response is in ws.sent.
    const pongs = ws.sent.filter((f) => f[0] === MsgType.Pong);
    expect(pongs.length).toBeGreaterThanOrEqual(1);
    // Pong payload echoes ts_origin = 1234.
    const pong = pongs[0];
    const dv = new DataView(pong.buffer, pong.byteOffset + 8, pong.length - 8);
    expect(dv.getUint32(0, true)).toBe(1234);
  });

  test("inbound Pong updates OWT", () => {
    const ws = new FakeWS();
    const clk = new FakeClock();
    clk.now = 1000;
    const c = new NetClient(ws, clk);
    c.start();
    // Server sends Pong{ts_origin=900, ts_responder=950}; RTT=100,
    // OWT=50 in client clock domain.
    const payload = new Uint8Array(8);
    const dv = new DataView(payload.buffer);
    dv.setUint32(0, 900, true);
    dv.setUint32(4, 950, true);
    const frame = encodeFrame(
      { type: MsgType.Pong, flags: 0, seq: 0, ack: 0xffff, len: 8 },
      payload,
    );
    ws.deliver(frame);
    expect(c.owtMs()).toBe(50);
  });
});
