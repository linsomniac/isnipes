package lobby

import (
	"strings"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// Phase 6 §6.2 — chat validation/rate-limit constants.
const (
	chatMaxLen     = 1024 // bytes after TrimSpace
	chatBucketCap  = 4    // burst
	chatRefillMs   = 500  // 1 token per 500 ms
	chatBucketFull = 4    // initial bucket = full
)

// chatBucket is the §6.2 per-session token bucket.
type chatBucket struct {
	tokens     int
	lastRefill time.Time
}

// allow decrements one token if available; refills based on elapsed
// time first. Returns true if the call is permitted.
func (b *chatBucket) allow(now time.Time) bool {
	if b.tokens < 0 {
		b.tokens = 0
	}
	// Refill.
	if !b.lastRefill.IsZero() {
		elapsed := now.Sub(b.lastRefill)
		refill := int(elapsed / (chatRefillMs * time.Millisecond))
		if refill > 0 {
			b.tokens += refill
			if b.tokens > chatBucketCap {
				b.tokens = chatBucketCap
			}
			b.lastRefill = b.lastRefill.Add(time.Duration(refill) * chatRefillMs * time.Millisecond)
		}
	} else {
		b.tokens = chatBucketFull
		b.lastRefill = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// handleChat processes a LobbyChat envelope: validate, rate-limit,
// broadcast `chat_relay` to every room member (including the sender,
// per §6.3 echo rule). Errors yield single-session error envelopes.
func (l *Lobby) handleChat(s *Session, p proto.LobbyChatPayload) {
	if s.Nick == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "hello first")
		return
	}
	text := strings.TrimSpace(p.Text)
	if text == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "text required")
		return
	}
	if len(text) > chatMaxLen {
		l.sendError(s, proto.LobbyErrTextTooLong, "text > 1024 bytes")
		return
	}
	if s.roomID == "" || s.roomID != p.RoomID {
		l.sendError(s, proto.LobbyErrNotInRoom, "not in that room")
		return
	}
	room, ok := l.rooms[p.RoomID]
	if !ok {
		l.sendError(s, proto.LobbyErrRoomGone, "room gone")
		return
	}
	// Rate-limit.
	if s.chat == nil {
		s.chat = &chatBucket{}
	}
	if !s.chat.allow(l.clock()) {
		l.sendError(s, proto.LobbyErrRateLimited, "chat rate exceeded")
		return
	}
	// Broadcast.
	relay := proto.LobbyChatRelayPayload{
		RoomID:   room.ID,
		FromNick: s.Nick,
		Text:     text,
		Ts:       l.clock().UnixMilli(),
	}
	for _, mid := range room.Members {
		ms, ok := l.sessions[SessionID(mid)]
		if !ok {
			continue
		}
		l.send(ms, proto.LobbyChatRelay, relay)
	}
}
