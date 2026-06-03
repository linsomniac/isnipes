// Wire-protocol mirror of internal/proto.
//
// IMPORTANT: the schemaDescriptor string below is **byte-identical** to
// internal/proto/checksum.go's `schemaDescriptor` constant. Any change
// to one MUST be matched in the other; the parity test in
// tests/proto.test.ts validates this against testdata/proto/checksum.txt.
//
// See PHASE2.md §5 (schema checksum) and §6.3 (binary messages).

export const schemaDescriptor =
  "isnipes-schema/v1\n" +
  "frame-header: [u8 type][u8 flags][u16 seq][u16 ack][u16 len][bytes payload]\n" +
  "dir8: 0,1,2,3,4,5,6,7,8\n" +
  "flag-bits: DEAD=0x01,SPAWN_INVULN=0x02,TURBO=0x04\n" +
  "entity: u32 id, u8 kind, u8 hp, u8 facing, u8 flags, i32 x, i32 y, i16 vx, i16 vy (20 bytes)\n" +
  "msg 0x00 MatchJoin C2S u32 schema_checksum, u8 token_len, bytes token\n" +
  "msg 0x01 Input C2S u16 client_tick, u8 dir, u8 turbo, u8 fire_dir\n" +
  "msg 0x02 Snapshot S2C u32 server_tick, u16 your_last_input_tick, u32 your_entity_id, u8 entity_count, [Entity]*n\n" +
  "msg 0x04 Event S2C u8 kind, u32 actor, u32 target, u8 reason\n" +
  "msg 0x06 Ping CS u32 ts_origin\n" +
  "msg 0x07 Pong CS u32 ts_origin, u32 ts_responder\n" +
  "msg 0x08 MatchOver S2C u32 final_tick, u8 reason, u32 winner_id_or_0, u8 entry_count, [u32 player_id, i32 score, u8 lives_remaining]*n\n" +
  "msg 0x09 MapInit S2C u32 seed, u16 width, u16 height, u8 packing, bytes packed_tiles\n" +
  "msg 0x0B Scoreboard S2C u32 server_tick, u8 entry_count, [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]*n\n" +
  "event-kinds: 0x01 entity_spawn, 0x02 entity_hit, 0x03 entity_kill, 0x04 generator_destroyed, 0x05 player_join, 0x06 player_leave, 0x07 player_dc, 0x08 player_rejoin, 0x09 match_starting, 0x0A match_started, 0x0B match_end, 0x0C chat_relay, 0x0D respawn_pending\n" +
  "close-codes: 1000 ok, 4001 AUTH, 4002 VERSION, 4003 MALFORMED, 4004 FULL, 4005 NOT_FOUND, 4006 SERVER_ERROR, 4007 IDLE\n";

// computeSchemaChecksum returns the u32 schemaChecksum (little-endian
// of SHA-256(schemaDescriptor)[0:4]).
//
// AIDEV-NOTE: crypto.subtle is a secure-context-only API — present on
// HTTPS and on http://localhost, but UNDEFINED on a plain-http page served
// to a LAN IP (e.g. http://192.168.x.x:8080). Without the sha256() fallback
// this line threw "Cannot read properties of undefined (reading 'digest')"
// during bootstrap, the lobby WS never opened, and the client hung on
// "Connecting…" forever for any non-localhost http visitor. The fallback
// yields the identical digest so the server-side checksum match is unchanged.
export async function computeSchemaChecksum(): Promise<number> {
  const bytes = new TextEncoder().encode(schemaDescriptor);
  const hash =
    typeof crypto !== "undefined" && crypto.subtle
      ? new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))
      : sha256(bytes);
  return (hash[0] | (hash[1] << 8) | (hash[2] << 16) | (hash[3] << 24)) >>> 0;
}

// sha256 is a dependency-free SHA-256 (FIPS 180-4) returning the 32-byte
// big-endian digest, byte-identical to crypto.subtle.digest("SHA-256", …).
// Used only as the insecure-context fallback above; small inputs only.
export function sha256(data: Uint8Array): Uint8Array {
  const K = new Uint32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
    0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
    0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
    0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
    0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
    0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
  ]);
  let h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a;
  let h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;

  const len = data.length;
  const withOne = len + 1;
  const pad = (56 - (withOne % 64) + 64) % 64;
  const total = withOne + pad + 8;
  const msg = new Uint8Array(total);
  msg.set(data);
  msg[len] = 0x80;
  const dv = new DataView(msg.buffer);
  const bitLen = len * 8;
  dv.setUint32(total - 8, Math.floor(bitLen / 0x100000000));
  dv.setUint32(total - 4, bitLen >>> 0);

  const w = new Uint32Array(64);
  const rotr = (x: number, n: number): number => (x >>> n) | (x << (32 - n));

  for (let off = 0; off < total; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = dv.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const x = w[i - 15], y = w[i - 2];
      const s0 = rotr(x, 7) ^ rotr(x, 18) ^ (x >>> 3);
      const s1 = rotr(y, 17) ^ rotr(y, 19) ^ (y >>> 10);
      w[i] = (w[i - 16] + s0 + w[i - 7] + s1) >>> 0;
    }
    let a = h0, b = h1, c = h2, d = h3, e = h4, f = h5, g = h6, h = h7;
    for (let i = 0; i < 64; i++) {
      const S1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
      const ch = (e & f) ^ (~e & g);
      const t1 = (h + S1 + ch + K[i] + w[i]) >>> 0;
      const S0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) >>> 0;
      h = g; g = f; f = e; e = (d + t1) >>> 0;
      d = c; c = b; b = a; a = (t1 + t2) >>> 0;
    }
    h0 = (h0 + a) >>> 0; h1 = (h1 + b) >>> 0; h2 = (h2 + c) >>> 0;
    h3 = (h3 + d) >>> 0; h4 = (h4 + e) >>> 0; h5 = (h5 + f) >>> 0;
    h6 = (h6 + g) >>> 0; h7 = (h7 + h) >>> 0;
  }
  const out = new Uint8Array(32);
  const odv = new DataView(out.buffer);
  [h0, h1, h2, h3, h4, h5, h6, h7].forEach((hh, i) => odv.setUint32(i * 4, hh >>> 0));
  return out;
}

// ----- message-type / event-kind / flag enums (mirror internal/proto) -----

export const MsgType = {
  MatchJoin: 0x00,
  Input: 0x01,
  Snapshot: 0x02,
  EntityDelta: 0x03,
  Event: 0x04,
  Chat: 0x05,
  Ping: 0x06,
  Pong: 0x07,
  MatchOver: 0x08,
  MapInit: 0x09,
  Resync: 0x0a,
  Scoreboard: 0x0b,
} as const;

export const EventKind = {
  EntitySpawn: 0x01,
  EntityHit: 0x02,
  EntityKill: 0x03,
  GeneratorDestroyed: 0x04,
  PlayerJoin: 0x05,
  PlayerLeave: 0x06,
  PlayerDC: 0x07,
  PlayerRejoin: 0x08,
  MatchStarting: 0x09,
  MatchStarted: 0x0a,
  MatchEnd: 0x0b,
  ChatRelay: 0x0c,
  RespawnPending: 0x0d,
} as const;

export const FlagBits = {
  Dead: 0x01,
  SpawnInvuln: 0x02,
  Turbo: 0x04,
} as const;

export const CloseCode = {
  OK: 1000,
  Unsupported: 1003,
  Auth: 4001,
  Version: 4002,
  Malformed: 4003,
  Full: 4004,
  NotFound: 4005,
  ServerError: 4006,
  Idle: 4007,
} as const;

// ----- frame header + binary helpers -----

export const FrameHeaderLen = 8;
export const AckNone = 0xffff;
export const MaxFrameLen = 65535;
export const EntityWireLen = 20;
export const MaxEntitiesPerSnapshot = 64;

export interface FrameHeader {
  type: number;
  flags: number;
  seq: number;
  ack: number;
  len: number;
}

export interface Entity {
  id: number;
  kind: number;
  hp: number;
  facing: number;
  flags: number;
  x: number;
  y: number;
  vx: number;
  vy: number;
}

export interface Snapshot {
  serverTick: number;
  yourLastInputTick: number;
  yourEntityID: number;
  entities: Entity[];
}

export interface MapInit {
  seed: number;
  width: number;
  height: number;
  packing: number;
  packedTiles: Uint8Array;
}

export function encodeFrame(hdr: FrameHeader, payload: Uint8Array): Uint8Array {
  const buf = new Uint8Array(FrameHeaderLen + payload.length);
  const dv = new DataView(buf.buffer);
  buf[0] = hdr.type;
  buf[1] = hdr.flags;
  dv.setUint16(2, hdr.seq, true);
  dv.setUint16(4, hdr.ack, true);
  dv.setUint16(6, payload.length, true);
  buf.set(payload, FrameHeaderLen);
  return buf;
}

export class ProtocolError extends Error {}

export function decodeFrame(src: Uint8Array): { header: FrameHeader; payload: Uint8Array } {
  if (src.length < FrameHeaderLen) {
    throw new ProtocolError("truncated");
  }
  const dv = new DataView(src.buffer, src.byteOffset, src.byteLength);
  const header: FrameHeader = {
    type: src[0],
    flags: src[1],
    seq: dv.getUint16(2, true),
    ack: dv.getUint16(4, true),
    len: dv.getUint16(6, true),
  };
  const end = FrameHeaderLen + header.len;
  if (src.length < end) throw new ProtocolError("truncated");
  if (src.length > end) throw new ProtocolError("trailing bytes");
  return { header, payload: src.subarray(FrameHeaderLen, end) };
}

export function decodeSnapshot(payload: Uint8Array): Snapshot {
  if (payload.length < 11) throw new ProtocolError("snapshot too short");
  const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  const serverTick = dv.getUint32(0, true);
  const yourLastInputTick = dv.getUint16(4, true);
  const yourEntityID = dv.getUint32(6, true);
  const n = payload[10];
  if (n > MaxEntitiesPerSnapshot) throw new ProtocolError("entity_count out of range");
  if (payload.length !== 11 + n * EntityWireLen) throw new ProtocolError("snapshot length mismatch");
  const entities: Entity[] = [];
  let off = 11;
  for (let i = 0; i < n; i++) {
    entities.push({
      id: dv.getUint32(off + 0, true),
      kind: payload[off + 4],
      hp: payload[off + 5],
      facing: payload[off + 6],
      flags: payload[off + 7],
      x: dv.getInt32(off + 8, true),
      y: dv.getInt32(off + 12, true),
      vx: dv.getInt16(off + 16, true),
      vy: dv.getInt16(off + 18, true),
    });
    off += EntityWireLen;
  }
  return { serverTick, yourLastInputTick, yourEntityID, entities };
}

export function decodeMapInit(payload: Uint8Array): MapInit {
  if (payload.length < 9) throw new ProtocolError("mapinit too short");
  const dv = new DataView(payload.buffer, payload.byteOffset, payload.byteLength);
  return {
    seed: dv.getUint32(0, true),
    width: dv.getUint16(4, true),
    height: dv.getUint16(6, true),
    packing: payload[8],
    packedTiles: payload.subarray(9),
  };
}

export interface Input {
  clientTick: number;
  dir: number;
  turbo: number;
  fireDir: number;
}

export function encodeInput(in0: Input): Uint8Array {
  if (in0.dir > 8 || in0.fireDir > 8 || in0.turbo > 1) {
    throw new ProtocolError("malformed Input");
  }
  const buf = new Uint8Array(5);
  const dv = new DataView(buf.buffer);
  dv.setUint16(0, in0.clientTick, true);
  buf[2] = in0.dir;
  buf[3] = in0.turbo;
  buf[4] = in0.fireDir;
  return buf;
}

export interface MatchJoin {
  schemaChecksum: number;
  token: Uint8Array;
}

export function encodeMatchJoin(m: MatchJoin): Uint8Array {
  if (m.token.length === 0 || m.token.length > 32) throw new ProtocolError("token length");
  const buf = new Uint8Array(5 + m.token.length);
  const dv = new DataView(buf.buffer);
  dv.setUint32(0, m.schemaChecksum, true);
  buf[4] = m.token.length;
  buf.set(m.token, 5);
  return buf;
}
