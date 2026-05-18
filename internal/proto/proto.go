package proto

import "errors"

// MsgType identifies a binary frame's payload kind. Values match
// PHASE2.md §1.1 / SPEC §4.3.2.
type MsgType uint8

const (
	MsgMatchJoin   MsgType = 0x00
	MsgInput       MsgType = 0x01
	MsgSnapshot    MsgType = 0x02
	MsgEntityDelta MsgType = 0x03 // deferred to v1.1
	MsgEvent       MsgType = 0x04
	MsgChat        MsgType = 0x05 // deferred to Phase 6
	MsgPing        MsgType = 0x06
	MsgPong        MsgType = 0x07
	MsgMatchOver   MsgType = 0x08
	MsgMapInit     MsgType = 0x09
	MsgResync      MsgType = 0x0A // deferred to Phase 5
	MsgScoreboard  MsgType = 0x0B
)

// EventKind matches SPEC §4.3.2.
type EventKind uint8

const (
	EventEntitySpawn        EventKind = 0x01
	EventEntityHit          EventKind = 0x02
	EventEntityKill         EventKind = 0x03
	EventGeneratorDestroyed EventKind = 0x04
	EventPlayerJoin         EventKind = 0x05
	EventPlayerLeave        EventKind = 0x06
	EventPlayerDC           EventKind = 0x07
	EventPlayerRejoin       EventKind = 0x08
	EventMatchStarting      EventKind = 0x09
	EventMatchStarted       EventKind = 0x0A
	EventMatchEnd           EventKind = 0x0B
	EventChatRelay          EventKind = 0x0C
	EventRespawnPending     EventKind = 0x0D
)

// Flag bits on Entity.Flags. Mirrors internal/sim's Flag* constants
// (PHASE1.md §5) but kept here so internal/proto remains independent
// of internal/sim for testing isolation.
const (
	FlagDead        uint8 = 1 << 0
	FlagSpawnInvuln uint8 = 1 << 1
	FlagTurbo       uint8 = 1 << 2
)

// MatchOver reason values from SPEC §3.8.1.
const (
	EndPVEComplete   uint8 = 0
	EndLastStanding  uint8 = 1
	EndAllEliminated uint8 = 2
	EndTimer         uint8 = 3
	EndServerError   uint8 = 4
)

// MaxFrameLen is the maximum payload length, per PHASE2.md §6.1.
const MaxFrameLen = 65535

// FrameHeaderLen is the fixed-size frame header per PHASE2.md §6.1.
const FrameHeaderLen = 8

// MaxEntitiesPerSnapshot is the §6.3.3 cap (matches the u8 entity_count
// field's representable maximum).
const MaxEntitiesPerSnapshot = 64

// Errors returned by decoders. Any of these → Close{4003 MALFORMED}.
var (
	ErrMalformed = errors.New("isnipes/proto: malformed frame")
	ErrTruncated = errors.New("isnipes/proto: truncated frame")
	ErrTooLong   = errors.New("isnipes/proto: payload exceeds MaxFrameLen")
)

// Entity is the canonical wire entity (PHASE2.md §6.3.3). It mirrors
// internal/sim.Entity's wire-relevant fields. Kept here to keep proto
// independent of sim.
type Entity struct {
	ID     uint32
	Kind   uint8
	HP     uint8
	Facing uint8
	Flags  uint8
	X, Y   int32
	VX, VY int16
}

// EntityWireLen is the exact byte size of one Entity on the wire.
const EntityWireLen = 4 + 1 + 1 + 1 + 1 + 4 + 4 + 2 + 2 // = 20
