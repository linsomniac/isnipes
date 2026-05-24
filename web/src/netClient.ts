// PHASE4.md §12 — minimal WebSocket client + 2 Hz Ping ticker +
// client-side OWT EWMA estimator. The client OWT is HUD-only; the
// server uses its own estimator (internal/match/owt.go) for lag-comp.

import {
  decodeFrame,
  encodeFrame,
  encodeInput,
  encodeMatchJoin,
  MsgType,
} from "./proto.js";

export const PING_INTERVAL_MS = 500;
const ALPHA_NUM = 1;
const ALPHA_DEN = 5; // α = 0.2 mirror of internal/match/owt.go
const MAX_OBSERVED_RTT_MS = 10000;
const IDLE_TIMEOUT_MS = 5000;

export interface ClientInputFrame {
  clientTick: number;
  dir: number; // Dir enum
  turbo: 0 | 1;
  fireDir: number;
}

// NetClientEvents is the set of callbacks the consumer subscribes
// to. The WS message-decode happens inside NetClient; consumers
// receive already-parsed payloads.
export interface NetClientEvents {
  onOpen?: () => void;
  onClose?: (code: number, reason: string) => void;
  // Raw decoded frames; the proto.ts layer surfaces these via
  // decoded-msg helpers that the consumer wires per-type.
  onFrame?: (type: number, payload: Uint8Array) => void;
}

// WebSocketLike is the minimal surface NetClient needs. The
// browser's `WebSocket` satisfies this naturally; tests provide a
// stub.
export interface WebSocketLike {
  readyState: number;
  send(data: ArrayBuffer | Uint8Array): void;
  close(code?: number, reason?: string): void;
  onopen: ((this: WebSocketLike, ev: any) => any) | null;
  onclose: ((this: WebSocketLike, ev: any) => any) | null;
  onerror: ((this: WebSocketLike, ev: any) => any) | null;
  onmessage: ((this: WebSocketLike, ev: any) => any) | null;
}

export interface ClockLike {
  nowMs(): number;
  setInterval(fn: () => void, ms: number): any;
  clearInterval(id: any): void;
}

// Default clock uses the browser's wall clock + timers.
export const defaultClock: ClockLike = {
  nowMs: () => Date.now(),
  setInterval: (fn, ms) => setInterval(fn, ms),
  clearInterval: (id) => clearInterval(id),
};

export class NetClient {
  private ws: WebSocketLike;
  private readonly clock: ClockLike;
  private events: NetClientEvents = {};
  private smoothedMs: number = 0;
  private smoothedSet: boolean = false;
  private pingTimer: any = null;
  private idleTimer: any = null;
  private lastFrameAt: number;
  private outboundSeq: number = 0;

  constructor(ws: WebSocketLike, clock: ClockLike = defaultClock) {
    this.ws = ws;
    this.clock = clock;
    this.lastFrameAt = clock.nowMs();
  }

  setEvents(events: NetClientEvents): void {
    this.events = events;
  }

  // sendMatchJoin: required first frame after WS upgrade. Returns
  // the encoded payload bytes for inspection by tests.
  sendMatchJoin(schemaChecksum: number, token: string): Uint8Array {
    // The wire token is the raw bytes of the lobby-issued joinToken
    // string (the Go server compares against []byte(joinToken)). Encode
    // as UTF-8; passing the string straight to encodeMatchJoin would
    // coerce each char to 0 via Uint8Array#set and fail server AUTH.
    const payload = encodeMatchJoin({ schemaChecksum, token: new TextEncoder().encode(token) });
    const buf = encodeFrame(
      { type: MsgType.MatchJoin, flags: 0, seq: this.outboundSeq++, ack: 0xffff, len: payload.length },
      payload,
    );
    this.ws.send(buf);
    return buf;
  }

  // sendInput: posts one tick's input.
  sendInput(input: ClientInputFrame): Uint8Array {
    const payload = encodeInput(input);
    const buf = encodeFrame(
      { type: MsgType.Input, flags: 0, seq: this.outboundSeq++, ack: 0xffff, len: payload.length },
      payload,
    );
    this.ws.send(buf);
    return buf;
  }

  // start arms the ping ticker + idle-timeout watchdog and hooks the
  // WS event handlers.
  start(): void {
    this.ws.onopen = () => {
      this.events.onOpen?.();
    };
    this.ws.onmessage = (ev: any) => {
      this.lastFrameAt = this.clock.nowMs();
      this.handleMessage(ev.data);
    };
    this.ws.onclose = (ev: any) => {
      this.shutdown();
      this.events.onClose?.(ev.code ?? 0, ev.reason ?? "");
    };
    this.ws.onerror = () => {
      // The follow-up onclose handles the lifecycle; we don't
      // duplicate that here.
    };
    this.pingTimer = this.clock.setInterval(() => this.sendPing(), PING_INTERVAL_MS);
    this.idleTimer = this.clock.setInterval(() => this.checkIdle(), 1000);
  }

  private shutdown(): void {
    if (this.pingTimer !== null) {
      this.clock.clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    if (this.idleTimer !== null) {
      this.clock.clearInterval(this.idleTimer);
      this.idleTimer = null;
    }
  }

  close(code: number = 1000, reason: string = ""): void {
    this.shutdown();
    this.ws.close(code, reason);
  }

  private sendPing(): void {
    const now = this.clock.nowMs();
    // Client-originated Ping with ts_origin in client clock.
    const payload = new Uint8Array(4);
    const dv = new DataView(payload.buffer);
    dv.setUint32(0, now >>> 0, true);
    const buf = encodeFrame(
      { type: MsgType.Ping, flags: 0, seq: this.outboundSeq++, ack: 0xffff, len: payload.length },
      payload,
    );
    this.ws.send(buf);
  }

  private checkIdle(): void {
    if (this.clock.nowMs() - this.lastFrameAt > IDLE_TIMEOUT_MS) {
      this.close(4007, "IDLE");
    }
  }

  // observePong consumes a server-originated Pong (response to our
  // Ping) and updates the OWT EWMA. ts_origin is in CLIENT clock
  // domain (the client originated the ping).
  observePong(tsOrigin: number, _tsResponder: number): void {
    const now = this.clock.nowMs();
    let rtt = (now - tsOrigin) | 0;
    if (rtt < 0) rtt = 0;
    if (rtt > MAX_OBSERVED_RTT_MS) rtt = MAX_OBSERVED_RTT_MS;
    const owt = (rtt / 2) | 0;
    if (!this.smoothedSet) {
      this.smoothedMs = owt;
      this.smoothedSet = true;
      return;
    }
    // α = 1/5 → new = old*4/5 + sample/5.
    this.smoothedMs = Math.floor(
      (this.smoothedMs * (ALPHA_DEN - ALPHA_NUM) + owt * ALPHA_NUM) / ALPHA_DEN,
    );
  }

  owtMs(): number {
    return this.smoothedMs;
  }

  private handleMessage(data: ArrayBuffer | Uint8Array | string): void {
    let bytes: Uint8Array;
    if (typeof data === "string") {
      // Lobby JSON; not in match-WS scope.
      return;
    } else if (data instanceof Uint8Array) {
      bytes = data;
    } else {
      bytes = new Uint8Array(data);
    }
    const decoded = decodeFrame(bytes);
    if (!decoded) return;
    this.events.onFrame?.(decoded.header.type, decoded.payload);
    // Auto-handle Pong → OWT update.
    if (decoded.header.type === MsgType.Pong && decoded.payload.length >= 8) {
      const dv = new DataView(decoded.payload.buffer, decoded.payload.byteOffset, decoded.payload.byteLength);
      const tsOrigin = dv.getUint32(0, true);
      const tsResponder = dv.getUint32(4, true);
      this.observePong(tsOrigin, tsResponder);
    }
    // Auto-respond to inbound Ping with Pong (server-initiated path).
    if (decoded.header.type === MsgType.Ping && decoded.payload.length >= 4) {
      const dv = new DataView(decoded.payload.buffer, decoded.payload.byteOffset, decoded.payload.byteLength);
      const tsOrigin = dv.getUint32(0, true);
      const tsResponder = this.clock.nowMs() >>> 0;
      const pong = new Uint8Array(8);
      const out = new DataView(pong.buffer);
      out.setUint32(0, tsOrigin, true);
      out.setUint32(4, tsResponder, true);
      const buf = encodeFrame(
        { type: MsgType.Pong, flags: 0, seq: this.outboundSeq++, ack: 0xffff, len: pong.length },
        pong,
      );
      this.ws.send(buf);
    }
  }
}
