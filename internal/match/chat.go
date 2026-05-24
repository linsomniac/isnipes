package match

// PHASE7.md §7 — in-match chat relay. The match WS already defines the
// Chat (0x05) frame but earlier phases ignored it; Phase 7 wires the
// relay through the match actor. This is the only server change and it
// touches no wire schema (Chat 0x05 and Event/ChatRelay 0x0C already
// exist), so schemaChecksum is unchanged.
//
// Wire (PHASE7.md §7.2 / open question §19.5 — RESOLVED, see
// .phase-loop-notes "Spec issues"): the relay is a SINGLE self-attributing
// Chat (0x05) frame. The two-frame {ChatRelay event + Chat text} pair the
// spec sketched cannot be made atomic on the wire because the slot's out
// channel has two producers (this actor + the reader's Pong echo), so a
// Pong can split the pair and orphan the attribution (codex iter-5). A
// single frame is atomic (one send, drop-or-deliver) and immune to
// interleaving/backpressure.
//
//   C→S Chat payload: [u8 len][utf8 text]              (client → server)
//   S→C Chat payload: [u32 sender][u8 len][utf8 text]  (server → client relay)
//
// schemaChecksum is unchanged (checksum.go descriptor untouched). Dead-cam
// senders may chat (§3.9).

import (
	"encoding/binary"
	"strings"
	"unicode/utf8"

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
	// Chat is specified as UTF-8 text; reject binary/garbage so the
	// server never broadcasts invalid protocol text (codex iter-5).
	if !utf8.ValidString(text) {
		return
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

// broadcastChat relays a single self-attributing Chat frame to every
// connected slot (incl. dead-cam). Best-effort, non-blocking per slot: a
// full out queue drops the message for that slot only (never closes it,
// unlike sendFrameTo). One frame ⇒ no orphan-attribution race.
func (m *Match) broadcastChat(sender sim.EntityID, text string) {
	payload := encodeRelayChat(sender, text)
	for _, s := range m.slots {
		if s.out == nil {
			continue
		}
		select {
		case s.out <- OutboundFrame{Type: proto.MsgChat, Payload: payload}:
		default:
			// queue full: drop for this slot only.
		}
	}
}

// encodeRelayChat builds the S→C relay Chat payload: [u32 sender][u8 len][text].
func encodeRelayChat(sender sim.EntityID, text string) []byte {
	b := make([]byte, 4+1+len(text))
	binary.LittleEndian.PutUint32(b[0:4], uint32(sender))
	b[4] = byte(len(text))
	copy(b[5:], text)
	return b
}

// DecodeChatText extracts the text from a C→S Chat payload: [u8 len][text].
// Used by the net read loop (inbound client chat) and tests.
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

// DecodeRelayChat extracts (sender, text) from an S→C relay Chat payload:
// [u32 sender][u8 len][text]. Used by tests (the TS client mirrors this).
func DecodeRelayChat(payload []byte) (sim.EntityID, string, bool) {
	if len(payload) < 5 {
		return 0, "", false
	}
	sender := binary.LittleEndian.Uint32(payload[0:4])
	n := int(payload[4])
	if len(payload) != 5+n {
		return 0, "", false
	}
	return sim.EntityID(sender), string(payload[5 : 5+n]), true
}
