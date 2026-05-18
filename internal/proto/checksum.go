package proto

import "crypto/sha256"

// schemaDescriptor is the canonical schema source-of-truth.
// IMPORTANT: this string is duplicated verbatim in web/src/proto.ts.
// Any change here MUST be matched in the TypeScript mirror; the
// cross-runtime tests in §13.4 enforce parity.
//
// The schemaChecksum (u32) is bytes [0:4] of SHA-256(schemaDescriptor),
// interpreted little-endian. See PHASE2.md §5.1.
const schemaDescriptor = "" +
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
	"close-codes: 1000 ok, 4001 AUTH, 4002 VERSION, 4003 MALFORMED, 4004 FULL, 4005 NOT_FOUND, 4006 SERVER_ERROR, 4007 IDLE\n"

// schemaChecksumValue is the u32 schemaChecksum value, computed once
// at init. Equals little-endian decode of SHA-256(schemaDescriptor)[0:4].
// Held in an unexported var so callers cannot mutate it; read via
// SchemaChecksum() below.
var schemaChecksumValue uint32 = computeSchemaChecksum()

func computeSchemaChecksum() uint32 {
	sum := sha256.Sum256([]byte(schemaDescriptor))
	return uint32(sum[0]) | uint32(sum[1])<<8 | uint32(sum[2])<<16 | uint32(sum[3])<<24
}

// SchemaChecksum returns the canonical schema checksum (see §5.1).
// The value is read-only; the package guarantees it is stable for
// the process lifetime.
func SchemaChecksum() uint32 { return schemaChecksumValue }

// SchemaDescriptor returns the canonical source string. Exposed so
// tests (and the TS mirror) can verify parity.
func SchemaDescriptor() string { return schemaDescriptor }
