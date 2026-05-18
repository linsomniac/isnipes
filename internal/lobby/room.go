package lobby

import (
	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// RoomState tracks lifecycle per §7.2.
type RoomState uint8

const (
	RoomOpen RoomState = iota
	RoomStarting
	RoomInMatch
	RoomClosed
)

func (rs RoomState) String() string {
	switch rs {
	case RoomOpen:
		return "OPEN"
	case RoomStarting:
		return "STARTING"
	case RoomInMatch:
		return "IN_MATCH"
	case RoomClosed:
		return "CLOSED"
	}
	return "?"
}

// Room is the lobby-side room record.
type Room struct {
	ID      string
	Name    string
	Host    string // playerId
	Max     int
	Level   proto.Level
	State   RoomState
	Members []string // playerIds in join order

	// Set when the host calls startMatch.
	MatchID string
	// Pending player→EntityID assignments for the match.
	Slots []sim.EntityID
}

// describe returns the wire RoomDescriptor.
func (r *Room) describe() proto.RoomDescriptor {
	return proto.RoomDescriptor{
		ID:      r.ID,
		Name:    r.Name,
		Players: len(r.Members),
		Max:     r.Max,
		Mode:    "ffa",
		Level:   r.Level,
		State:   r.State.String(),
	}
}
