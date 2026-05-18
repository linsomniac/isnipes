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
export async function computeSchemaChecksum(): Promise<number> {
  const bytes = new TextEncoder().encode(schemaDescriptor);
  const hash = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
  return (hash[0] | (hash[1] << 8) | (hash[2] << 16) | (hash[3] << 24)) >>> 0;
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
