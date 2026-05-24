package match

// PHASE7.md §7 — in-match chat relay. The match WS already defines the
// Chat (0x05) frame but earlier phases ignored it; Phase 7 wires the
// relay through the match actor. This is the only server change and it
// touches no wire schema (Chat 0x05 and Event/ChatRelay 0x0C already
// exist), so schemaChecksum is unchanged.
//
// Wire (PHASE7.md §7.2 / open question §19.5): a chat message is relayed
// as a pair — an Event{Kind: ChatRelay, Actor: senderEntityID, Reason: 1
// (match scope)} immediately followed by the Chat (0x05) text frame — so
// the client can attribute the message (Event carries no text; Chat
// carries no sender). Dead-cam senders may chat (§3.9).

import (
	"strings"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

const (
	// chatMaxLen is the Chat frame's u8-len ceiling.
	chatMaxLen = 255
	// Rate limit: 4 messages / 2 s (burst 4, refill 1 per 0.5 s). At the
	// 30 Hz sim tick, 0.5 s = 15 ticks (mirrors the lobby limit, P6 §6.2).
	chatBurst       = 4
	chatRefillTicks = 15
	// chatScopeMatch is SPEC §541's scope=1 (match) for ChatRelay.
	chatScopeMatch = 1
)

type chatBucket struct {
	tokens   float64
	lastTick uint32
}

type ctlChat struct {
	PlayerID sim.EntityID
	Text     string
}

func (ctlChat) controlTag() {}

// SubmitChat posts an in-match chat message from a player's WS to the
// actor. Best-effort: a full inbox drops the message.
func (m *Match) SubmitChat(playerID sim.EntityID, text string) {
	select {
	case m.in <- ctlChat{PlayerID: playerID, Text: text}:
	default:
	}
}

// handleChat validates, rate-limits, and relays one chat message. Runs
// in the actor goroutine (no locking on slots/chatBuckets).
func (m *Match) handleChat(v ctlChat) {
	slot, ok := m.slots[v.PlayerID]
	if !ok {
		return
	}
	text := strings.TrimSpace(v.Text)
	if text == "" {
		return // §7.2: drop empty (no Error frame on the match WS, §4.3.5)
	}
	if len(text) > chatMaxLen {
		text = text[:chatMaxLen]
	}
	if !m.chatAllow(slot.PlayerID) {
		return // rate-limited: silently dropped
	}
	m.broadcastChat(slot.PlayerID, text)
}

// chatAllow applies the per-player token bucket. Returns false when the
// sender is over their rate limit.
func (m *Match) chatAllow(pid sim.EntityID) bool {
	now := m.ServerTick()
	b := m.chatBuckets[pid]
	if b == nil {
		b = &chatBucket{tokens: chatBurst, lastTick: now}
		m.chatBuckets[pid] = b
	}
	if now > b.lastTick {
		b.tokens += float64(now-b.lastTick) / float64(chatRefillTicks)
		if b.tokens > chatBurst {
			b.tokens = chatBurst
		}
		b.lastTick = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens -= 1
	return true
}

// broadcastChat relays the ChatRelay event + Chat text frame to every
// connected slot (incl. dead-cam). Best-effort per slot: a full out
// queue drops the message for that slot only (does NOT close the slot,
// unlike sendFrameTo).
func (m *Match) broadcastChat(sender sim.EntityID, text string) {
	ev := proto.Event{
		Kind:   uint8(proto.EventChatRelay),
		Actor:  uint32(sender),
		Target: 0,
		Reason: chatScopeMatch,
	}
	evPayload, err := ev.Encode(nil)
	if err != nil {
		m.abort("chat event encode: " + err.Error())
		return
	}
	chatPayload := encodeChatPayload(text)
	for _, s := range m.slots {
		if s.out == nil {
			continue
		}
		// The relay event and its text frame must arrive consecutively;
		// only enqueue the text if the event made it into the queue.
		select {
		case s.out <- OutboundFrame{Type: proto.MsgEvent, Payload: evPayload}:
		default:
			continue
		}
		select {
		case s.out <- OutboundFrame{Type: proto.MsgChat, Payload: chatPayload}:
		default:
		}
	}
}

// encodeChatPayload builds the Chat (0x05) payload: u8 len, utf8 text.
// (proto.go has no Chat codec and is kept frozen; this is the only
// caller, so the trivial encode lives here.)
func encodeChatPayload(text string) []byte {
	b := make([]byte, 1+len(text))
	b[0] = byte(len(text))
	copy(b[1:], text)
	return b
}

// DecodeChatText extracts the text from a Chat (0x05) payload. Used by
// the net read loop and tests.
func DecodeChatText(payload []byte) (string, bool) {
	if len(payload) < 1 {
		return "", false
	}
	n := int(payload[0])
	if len(payload) != 1+n {
		return "", false
	}
	return string(payload[1 : 1+n]), true
}
