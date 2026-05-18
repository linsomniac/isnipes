package proto

import (
	"encoding/json"
	"errors"
)

// LobbyEnvelopeVersion is the §6.2 `v` field for Phase 2.
const LobbyEnvelopeVersion = 1

// Envelope is the lobby JSON wrapper:
//
//	{ "t": "<type>", "v": 1, "d": { ...payload... } }
type Envelope struct {
	T string          `json:"t"`
	V int             `json:"v"`
	D json.RawMessage `json:"d"`
}

// Lobby type tags (§6.2.1).
const (
	LobbyHello        = "hello"
	LobbyWelcome      = "welcome"
	LobbyRoomList     = "roomList"
	LobbyCreateRoom   = "createRoom"
	LobbyJoinRoom     = "joinRoom"
	LobbyLeaveRoom    = "leaveRoom"
	LobbyStartMatch   = "startMatch"
	LobbyMatchStarted = "matchStarted"
	LobbyTagError     = "error"
)

// Lobby error codes (§6.2.1).
const (
	LobbyErrVersion       = "VERSION"
	LobbyErrNickTaken     = "NICK_TAKEN"
	LobbyErrBadRequest    = "BAD_REQUEST"
	LobbyErrNotHost       = "NOT_HOST"
	LobbyErrNoRoom        = "NO_ROOM"
	LobbyErrRoomFull      = "ROOM_FULL"
	LobbyErrRoomGone      = "ROOM_GONE"
	LobbyErrAlreadyInRoom = "ALREADY_IN_ROOM"
	LobbyErrNoOpponent    = "NO_OPPONENT"
	LobbyErrBadLevel      = "BAD_LEVEL"     // Phase 6 §8.1
	LobbyErrNotInRoom     = "NOT_IN_ROOM"   // Phase 6 §6.2
	LobbyErrTextTooLong   = "TEXT_TOO_LONG" // Phase 6 §6.2
	LobbyErrRateLimited   = "RATE_LIMITED"  // Phase 6 §6.2
	LobbyErrKicked        = "KICKED"        // Phase 6 §7.1
)

// Hello payload (C→S).
type Hello struct {
	Nick           string `json:"nick"`
	ClientVersion  string `json:"clientVersion"`
	SchemaChecksum uint32 `json:"schemaChecksum"`
}

// Welcome payload (S→C).
type Welcome struct {
	PlayerID       string `json:"playerId"`
	Nick           string `json:"nick"`
	ServerVersion  string `json:"serverVersion"`
	SchemaChecksum uint32 `json:"schemaChecksum"`
	MOTD           string `json:"motd"`
}

// RoomDescriptor appears in RoomList entries (S→C).
type RoomDescriptor struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Players int    `json:"players"`
	Max     int    `json:"max"`
	Mode    string `json:"mode"`
	Level   Level  `json:"level"`
	State   string `json:"state"`
}

// Level is the (letter, number) pair from §6.2.1.
type Level struct {
	Letter string `json:"letter"`
	Number int    `json:"number"`
}

// RoomList payload (S→C).
type RoomList struct {
	Rooms []RoomDescriptor `json:"rooms"`
}

// CreateRoom payload (C→S).
type CreateRoom struct {
	Name  string `json:"name"`
	Max   int    `json:"max"`
	Level Level  `json:"level"`
}

// JoinRoom payload (C→S).
type JoinRoom struct {
	RoomID string `json:"roomId"`
}

// LeaveRoom payload (C→S, empty).
type LeaveRoom struct{}

// StartMatch payload (C→S).
type StartMatch struct {
	RoomID string `json:"roomId"`
}

// MatchStarted payload (S→C, per-recipient).
type MatchStarted struct {
	MatchID        string `json:"matchId"`
	GameSocketPath string `json:"gameSocketPath"`
	TickRate       int    `json:"tickRate"`
	MapSeed        uint32 `json:"mapSeed"`
	JoinToken      string `json:"joinToken"`
}

// LobbyError payload (S→C).
type LobbyError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EncodeLobbyMessage marshals a typed payload into a lobby envelope's
// JSON bytes. The caller passes the type tag (one of LobbyHello, etc.)
// and the payload value (one of the structs above).
func EncodeLobbyMessage(t string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{T: t, V: LobbyEnvelopeVersion, D: raw})
}

// DecodeLobbyEnvelope parses the wrapper. The caller then dispatches
// on env.T and calls json.Unmarshal(env.D, &payload) for the typed
// payload.
func DecodeLobbyEnvelope(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, err
	}
	if e.V != LobbyEnvelopeVersion {
		return Envelope{}, ErrLobbyVersion
	}
	return e, nil
}

// ErrLobbyVersion is returned when the envelope's `v` is unsupported.
// The lobby handler maps this to error{code: VERSION} and close 1003.
var ErrLobbyVersion = errors.New("isnipes/proto: lobby envelope version unsupported")
