package match

// PHASE7.md §15.11 — in-match chat relay. These are synchronous unit
// tests of handleChat (no actor goroutine), so slot state — including
// DeadCam — can be set directly without racing the actor (the live
// actor owns m.slots). The relay emits a ChatRelay event + Chat text
// frame to every connected slot (incl. dead-cam), rate-limited per
// sender; empty messages are dropped (§7.2 / §3.9).

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// chatSetup builds a Match with two joined slots wired to out channels,
// WITHOUT starting the actor goroutine.
func chatSetup(t *testing.T) (*Match, chan OutboundFrame, chan OutboundFrame) {
	t.Helper()
	fc := &fakeClock{now: time.Unix(0, 0)}
	cfg := MatchConfig{
		MatchID:   "M1",
		MapSeed:   0xCAFEBABE,
		MapWidth:  60,
		MapHeight: 40,
		PlayerSlots: []PendingJoin{
			{MatchID: "M1", Token: "tokA", PlayerID: 1, Nick: "Alice", IssuedAt: fc.Now()},
			{MatchID: "M1", Token: "tokB", PlayerID: 2, Nick: "Bob", IssuedAt: fc.Now()},
		},
		Ticker: newFakeTicker(),
		Clock:  fc.Now,
	}
	m, err := NewMatch(cfg)
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	outA := make(chan OutboundFrame, 64)
	outB := make(chan OutboundFrame, 64)
	m.slots[1] = &Slot{PlayerID: 1, Nick: "Alice", out: outA, closed: make(chan struct{})}
	m.slots[2] = &Slot{PlayerID: 2, Nick: "Bob", out: outB, closed: make(chan struct{})}
	return m, outA, outB
}

func drainNow(ch chan OutboundFrame) []OutboundFrame {
	var fs []OutboundFrame
	for {
		select {
		case f := <-ch:
			fs = append(fs, f)
		default:
			return fs
		}
	}
}

// findChatRelay returns the (event, text) of the first ChatRelay pair —
// an Event{ChatRelay} immediately followed by a Chat text frame.
func findChatRelay(frames []OutboundFrame) (proto.Event, string, bool) {
	for i := 0; i < len(frames); i++ {
		if frames[i].Type != proto.MsgEvent {
			continue
		}
		ev, err := proto.DecodeEvent(frames[i].Payload)
		if err != nil || ev.Kind != uint8(proto.EventChatRelay) {
			continue
		}
		if i+1 < len(frames) && frames[i+1].Type == proto.MsgChat {
			text, ok := DecodeChatText(frames[i+1].Payload)
			if ok {
				return ev, text, true
			}
		}
		return ev, "", true
	}
	return proto.Event{}, "", false
}

func countChatRelays(frames []OutboundFrame) int {
	n := 0
	for _, f := range frames {
		if f.Type != proto.MsgEvent {
			continue
		}
		if ev, err := proto.DecodeEvent(f.Payload); err == nil && ev.Kind == uint8(proto.EventChatRelay) {
			n++
		}
	}
	return n
}

func TestMatch_ChatRelayBroadcastsToAllSlots(t *testing.T) {
	m, outA, outB := chatSetup(t)
	m.handleChat(ctlChat{PlayerID: 1, Text: "  hello world  "}) // trimmed

	for name, ch := range map[string]chan OutboundFrame{"A": outA, "B": outB} {
		ev, text, ok := findChatRelay(drainNow(ch))
		if !ok {
			t.Fatalf("slot %s: no ChatRelay pair", name)
		}
		if ev.Actor != 1 {
			t.Fatalf("slot %s: ChatRelay Actor=%d, want 1 (sender entity id)", name, ev.Actor)
		}
		if ev.Reason != chatScopeMatch {
			t.Fatalf("slot %s: ChatRelay Reason=%d, want %d (match scope)", name, ev.Reason, chatScopeMatch)
		}
		if text != "hello world" {
			t.Fatalf("slot %s: chat text=%q, want %q", name, text, "hello world")
		}
	}
}

func TestMatch_ChatRelayDropsEmpty(t *testing.T) {
	m, outA, _ := chatSetup(t)
	m.handleChat(ctlChat{PlayerID: 1, Text: "   "}) // whitespace-only
	if n := countChatRelays(drainNow(outA)); n != 0 {
		t.Fatalf("expected 0 relays for empty message, got %d", n)
	}
}

func TestMatch_ChatRelayUnknownSenderIgnored(t *testing.T) {
	m, outA, _ := chatSetup(t)
	m.handleChat(ctlChat{PlayerID: 99, Text: "hi"}) // no such slot
	if n := countChatRelays(drainNow(outA)); n != 0 {
		t.Fatalf("expected 0 relays for unknown sender, got %d", n)
	}
}

func TestMatch_ChatRelayRateLimited(t *testing.T) {
	m, outA, _ := chatSetup(t)
	// Burst of 6 within the same tick window (ServerTick stays 0 with no
	// sim): only chatBurst (4) relay; the rest are dropped.
	for i := 0; i < 6; i++ {
		m.handleChat(ctlChat{PlayerID: 1, Text: "spam"})
	}
	if n := countChatRelays(drainNow(outA)); n != chatBurst {
		t.Fatalf("rate-limited relay count=%d, want %d", n, chatBurst)
	}
}

func TestMatch_DeadCamMayChat(t *testing.T) {
	m, _, outB := chatSetup(t)
	// §3.9: a dead-cam player may still chat. handleChat does not gate on
	// the sender's DeadCam flag (unlike Input).
	m.slots[1].DeadCam = true
	m.handleChat(ctlChat{PlayerID: 1, Text: "ggs"})
	_, text, ok := findChatRelay(drainNow(outB))
	if !ok || text != "ggs" {
		t.Fatalf("dead-cam chat not relayed: ok=%v text=%q", ok, text)
	}
}

func TestMatch_ChatTextTruncatedToMax(t *testing.T) {
	m, _, outB := chatSetup(t)
	long := make([]byte, chatMaxLen+50)
	for i := range long {
		long[i] = 'x'
	}
	m.handleChat(ctlChat{PlayerID: 1, Text: string(long)})
	_, text, ok := findChatRelay(drainNow(outB))
	if !ok {
		t.Fatal("no relay for long message")
	}
	if len(text) != chatMaxLen {
		t.Fatalf("text len=%d, want %d (truncated to u8 ceiling)", len(text), chatMaxLen)
	}
}

func TestMatch_DecodeChatText(t *testing.T) {
	if _, ok := DecodeChatText(nil); ok {
		t.Fatal("empty payload should fail")
	}
	if _, ok := DecodeChatText([]byte{3, 'h', 'i'}); ok {
		t.Fatal("len mismatch should fail")
	}
	text, ok := DecodeChatText(encodeChatPayload("hey"))
	if !ok || text != "hey" {
		t.Fatalf("round-trip failed: ok=%v text=%q", ok, text)
	}
}
