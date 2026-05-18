package lobby

import "github.com/jafo/isnipes/internal/proto"

// broadcastRoomDelta is the Phase 6 §11 push variant: instead of
// resending the full roomList, emit a single-room delta to every
// connected session. The caller chooses the kind:
//   - LobbyRoomAdded — new room.
//   - LobbyRoomUpdated — state change, member change, etc.
//   - LobbyRoomRemoved — room is gone (uses roomID-only payload).
//
// Phase 6 keeps the Phase 2 full-list broadcast in place for the
// initial welcome handshake; only post-welcome change notifications
// flow through the delta path.
func (l *Lobby) broadcastRoomDelta(kind string, room *Room) {
	if kind == proto.LobbyRoomRemoved {
		payload := proto.LobbyRoomRemovedPayload{RoomID: room.ID}
		for _, s := range l.sessions {
			l.send(s, kind, payload)
		}
		return
	}
	payload := proto.LobbyRoomDeltaPayload{Room: room.describe()}
	for _, s := range l.sessions {
		l.send(s, kind, payload)
	}
}

// broadcastRoomRemoved emits a room_removed delta keyed on the room ID.
// Used when a room is GC'd or its match ends and the room is closed.
func (l *Lobby) broadcastRoomRemoved(roomID string) {
	payload := proto.LobbyRoomRemovedPayload{RoomID: roomID}
	for _, s := range l.sessions {
		l.send(s, proto.LobbyRoomRemoved, payload)
	}
}
