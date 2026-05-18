package lobby

import "github.com/jafo/isnipes/internal/proto"

// handleKick processes a LobbyKick envelope. Authorization (§7.2):
// sender must be the room's host; target must be a different session
// in the same room. On success, the evictee gets a `kicked` envelope
// and is removed from the room.
func (l *Lobby) handleKick(s *Session, p proto.LobbyKickPayload) {
	if s.Nick == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "hello first")
		return
	}
	room, ok := l.rooms[p.RoomID]
	if !ok {
		l.sendError(s, proto.LobbyErrRoomGone, "room gone")
		return
	}
	if room.Host != string(s.ID) {
		l.sendError(s, proto.LobbyErrNotHost, "only host can kick")
		return
	}
	if p.SessionID == string(s.ID) {
		l.sendError(s, proto.LobbyErrBadRequest, "host cannot kick self")
		return
	}
	target, ok := l.sessions[SessionID(p.SessionID)]
	if !ok || target.roomID != room.ID {
		l.sendError(s, proto.LobbyErrNotInRoom, "target not in room")
		return
	}
	// Notify the evictee.
	l.send(target, proto.LobbyKicked, proto.LobbyKickedPayload{
		RoomID: room.ID,
		Reason: "kicked-by-host",
	})
	// Remove from room.
	l.removeFromRoom(target, room)
	// §7.3: invalidate any join token allocated for this player.
	// Subsequent MatchJoin with that token returns Close{4001 AUTH}.
	for tok, pending := range l.tokens {
		if pending != nil && pending.SessionID == target.ID {
			delete(l.tokens, tok)
		}
	}
}
