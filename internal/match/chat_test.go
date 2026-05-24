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

// findChatRelay returns the (sender, text) of the first relay Chat frame —
// a single self-attributing Chat (0x05) frame [u32 sender][u8 len][text].
func findChatRelay(frames []OutboundFrame) (uint32, string, bool) {
	for _, f := range frames {
		if f.Type != proto.MsgChat {
			continue
		}
		if sender, text, ok := DecodeRelayChat(f.Payload); ok {
			return uint32(sender), text, true
		}
	}
	return 0, "", false
}

func countChatRelays(frames []OutboundFrame) int {
	n := 0
	for _, f := range frames {
		if f.Type == proto.MsgChat {
			n++
		}
	}
	return n
}

func TestMatch_ChatRelayBroadcastsToAllSlots(t *testing.T) {
	m, outA, outB := chatSetup(t)
	m.handleChat(ctlChat{PlayerID: 1, Text: "  hello world  "}) // trimmed

	for name, ch := range map[string]chan OutboundFrame{"A": outA, "B": outB} {
		sender, text, ok := findChatRelay(drainNow(ch))
		if !ok {
			t.Fatalf("slot %s: no relay Chat frame", name)
		}
		if sender != 1 {
			t.Fatalf("slot %s: relay sender=%d, want 1 (sender entity id)", name, sender)
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

func TestMatch_ChatRejectsNonUTF8(t *testing.T) {
	m, _, outB := chatSetup(t)
	m.handleChat(ctlChat{PlayerID: 1, Text: string([]byte{0xff, 0xfe})}) // invalid UTF-8
	if n := countChatRelays(drainNow(outB)); n != 0 {
		t.Fatalf("expected 0 relays for non-UTF-8 text, got %d", n)
	}
}

func TestMatch_DecodeChatText(t *testing.T) {
	// C→S Chat payload: [u8 len][text].
	if _, ok := DecodeChatText(nil); ok {
		t.Fatal("empty payload should fail")
	}
	if _, ok := DecodeChatText([]byte{3, 'h', 'i'}); ok {
		t.Fatal("len mismatch should fail")
	}
	text, ok := DecodeChatText([]byte{3, 'h', 'e', 'y'})
	if !ok || text != "hey" {
		t.Fatalf("C→S round-trip failed: ok=%v text=%q", ok, text)
	}
	// S→C relay Chat payload: [u32 sender][u8 len][text].
	sender, rtext, ok := DecodeRelayChat(encodeRelayChat(7, "gg"))
	if !ok || sender != 7 || rtext != "gg" {
		t.Fatalf("S→C round-trip failed: ok=%v sender=%d text=%q", ok, sender, rtext)
	}
	if _, _, ok := DecodeRelayChat([]byte{1, 2, 3}); ok {
		t.Fatal("short relay payload should fail")
	}
}
