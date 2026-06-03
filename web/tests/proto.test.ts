// PHASE2.md §13.4: cross-runtime schemaChecksum parity + byte
// fixture round-trip.

import { describe, expect, test } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  computeSchemaChecksum,
  sha256,
  decodeMapInit,
  decodeSnapshot,
  decodeFrame,
  encodeFrame,
  encodeInput,
  encodeMatchJoin,
  schemaDescriptor,
  ProtocolError,
  FlagBits,
  MaxFrameLen,
} from "../src/proto.js";

const TESTDATA = resolve(__dirname, "../../testdata/proto");

describe("schema checksum parity", () => {
  test("descriptor pinned", () => {
    expect(schemaDescriptor.length).toBeGreaterThan(800);
    expect(schemaDescriptor.startsWith("isnipes-schema/v1\n")).toBe(true);
  });

  test("checksum matches testdata/proto/checksum.txt", async () => {
    const want = readFileSync(resolve(TESTDATA, "checksum.txt"), "utf8").trim();
    const got = await computeSchemaChecksum();
    const hex = "0x" + got.toString(16).toUpperCase().padStart(8, "0");
    expect(hex).toBe(want);
  });

  // sha256 fallback exists because crypto.subtle is undefined on plain-HTTP
  // LAN pages (it's secure-context-only); without a fallback the client
  // bootstrap throws before opening the lobby WS and hangs on "Connecting…".
  test("sha256 fallback matches known vector", () => {
    // SHA-256("abc") = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
    const got = sha256(new TextEncoder().encode("abc"));
    const hex = [...got].map((b) => b.toString(16).padStart(2, "0")).join("");
    expect(hex).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
  });

  test("checksum is identical when crypto.subtle is unavailable", async () => {
    const want = readFileSync(resolve(TESTDATA, "checksum.txt"), "utf8").trim();
    const real = Object.getOwnPropertyDescriptor(globalThis, "crypto");
    // Simulate an insecure context: crypto present but no subtle.
    Object.defineProperty(globalThis, "crypto", {
      value: {},
      configurable: true,
      writable: true,
    });
    try {
      const got = await computeSchemaChecksum();
      const hex = "0x" + got.toString(16).toUpperCase().padStart(8, "0");
      expect(hex).toBe(want);
    } finally {
      if (real) Object.defineProperty(globalThis, "crypto", real);
    }
  });
});

describe("byte fixtures", () => {
  test("mapinit_baseline.bin decodes", () => {
    const raw = readFileSync(resolve(TESTDATA, "mapinit_baseline.bin"));
    const bytes = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
    const mi = decodeMapInit(bytes);
    expect(mi.seed >>> 0).toBe(0xdeadbeef);
    expect(mi.width).toBe(60);
    expect(mi.height).toBe(40);
    expect(mi.packing).toBe(1);
    expect(mi.packedTiles.length).toBe(600); // 60×40×2 bits = 600 bytes
  });

  test("snapshot_baseline.bin decodes", () => {
    const raw = readFileSync(resolve(TESTDATA, "snapshot_baseline.bin"));
    const bytes = new Uint8Array(raw.buffer, raw.byteOffset, raw.byteLength);
    const s = decodeSnapshot(bytes);
    expect(s.serverTick).toBe(100);
    expect(s.yourLastInputTick).toBe(50);
    expect(s.yourEntityID).toBe(1);
    expect(s.entities.length).toBe(3);
    expect(s.entities[0].id).toBe(1);
    expect(s.entities[1].id).toBe(2);
    expect(s.entities[2].id).toBe(9);
    // Entity 2 is turboing per the fixture.
    expect(s.entities[1].flags & FlagBits.Turbo).toBe(FlagBits.Turbo);
  });
});

describe("frame header encode/decode", () => {
  test("round-trip", () => {
    const payload = new Uint8Array([1, 2, 3, 4, 5]);
    const buf = encodeFrame({ type: 0x01, flags: 0, seq: 42, ack: 0xffff, len: 5 }, payload);
    const { header, payload: gotPayload } = decodeFrame(buf);
    expect(header.type).toBe(0x01);
    expect(header.seq).toBe(42);
    expect(gotPayload).toEqual(payload);
  });

  test("rejects trailing bytes", () => {
    const payload = new Uint8Array([1, 2, 3, 4, 5]);
    const buf = encodeFrame({ type: 0x01, flags: 0, seq: 0, ack: 0, len: 5 }, payload);
    const tampered = new Uint8Array(buf.length + 1);
    tampered.set(buf);
    tampered[buf.length] = 0xff;
    expect(() => decodeFrame(tampered)).toThrow(ProtocolError);
  });
});

describe("encoders match Go behavior", () => {
  test("encodeInput exact bytes", () => {
    const got = encodeInput({ clientTick: 12345, dir: 2, turbo: 1, fireDir: 5 });
    expect([...got]).toEqual([0x39, 0x30, 2, 1, 5]); // 12345 = 0x3039 LE
  });

  test("encodeInput rejects out-of-range Dir", () => {
    expect(() => encodeInput({ clientTick: 0, dir: 9, turbo: 0, fireDir: 0 })).toThrow(ProtocolError);
  });

  test("encodeMatchJoin exact bytes", () => {
    const tok = new TextEncoder().encode("hi");
    const got = encodeMatchJoin({ schemaChecksum: 0xdeadbeef, token: tok });
    expect([...got]).toEqual([0xef, 0xbe, 0xad, 0xde, 2, 0x68, 0x69]);
  });
});

describe("MaxFrameLen / ProtocolError surface", () => {
  test("MaxFrameLen is 65535", () => {
    expect(MaxFrameLen).toBe(65535);
  });
});
