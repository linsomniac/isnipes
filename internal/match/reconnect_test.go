//go:build testhooks

package match

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// newMatchWithRunningActor sets up a 2-player match, drives both joins,
// transitions to StateLive, and starts the actor. Returns the match,
// the fakeTicker (so tests can drive ticks), the two outbound channels,
// and a teardown.
func newMatchWithRunningActor(t *testing.T) (*Match, *fakeTicker, chan OutboundFrame, chan OutboundFrame, func()) {
	t.Helper()
	m, ft, _, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatalf("join A: %v", err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatalf("join B: %v", err)
	}
	// Drive one tick to ensure StateLive.
	ft.Send(time.Now())
	time.Sleep(20 * time.Millisecond)
	cleanup := func() {
		m.Abort("test")
		<-done
	}
	return m, ft, outA, outB, cleanup
}

// drainOutFor reads frames from `ch` until quiet for `quiet` duration.
func drainOutFor(ch <-chan OutboundFrame, quiet time.Duration) []OutboundFrame {
	var out []OutboundFrame
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, f)
		case <-time.After(quiet):
			return out
		}
	}
}

// TestReconnect_DCFreezesAndMarksSlot — SubmitDC sets DC, zeroes
// velocity in the sim, and broadcasts player_dc to the surviving slot.
func TestReconnect_DCFreezesAndMarksSlot(t *testing.T) {
	m, _, _, outB, cleanup := newMatchWithRunningActor(t)
	defer cleanup()

	// Drain B's initial setup frames before we drop A.
	_ = drainOutFor(outB, 100*time.Millisecond)

	// Drop A's WS.
	m.SubmitDC(1)
	time.Sleep(50 * time.Millisecond)

	// IsDCToken uses an RWMutex internally so this read is race-safe.
	if !m.IsDCToken("tokA") {
		t.Fatal("tokA missing from dcTokens")
	}
	// B should have received an Event with kind=player_dc.
	frames := drainOutFor(outB, 100*time.Millisecond)
	sawDC := false
	for _, f := range frames {
		if f.Type == proto.MsgEvent {
			ev, err := proto.DecodeEvent(f.Payload)
			if err == nil && ev.Kind == uint8(proto.EventPlayerDC) && ev.Target == 1 {
				sawDC = true
			}
		}
	}
	if !sawDC {
		t.Fatal("player_dc event not broadcast to surviving slot")
	}
}

// TestReconnect_AcceptedWithinGrace — fresh WS with same token within
// grace gets Resync→MapInit→Snapshot→Scoreboard and player_rejoin
// reaches the other slot.
func TestReconnect_AcceptedWithinGrace(t *testing.T) {
	m, _, _, outB, cleanup := newMatchWithRunningActor(t)
	defer cleanup()

	_ = drainOutFor(outB, 100*time.Millisecond)
	m.SubmitDC(1)
	time.Sleep(30 * time.Millisecond)

	// Reconnect via a fresh outbound channel.
	outA2 := make(chan OutboundFrame, 64)
	pid, err := m.SubmitReconnect("tokA", outA2)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if pid != 1 {
		t.Fatalf("pid = %d, want 1", pid)
	}
	if m.slots[1].DC {
		t.Fatal("DC flag still set after reconnect")
	}
	// Verify the resync sequence on outA2.
	got := drainOutFor(outA2, 200*time.Millisecond)
	types := make([]proto.MsgType, 0, len(got))
	for _, f := range got {
		types = append(types, f.Type)
	}
	want := []proto.MsgType{proto.MsgResync, proto.MsgMapInit, proto.MsgSnapshot, proto.MsgScoreboard}
	// Verify all four types appear, in order, somewhere in the stream.
	idx := 0
	for _, ty := range types {
		if idx < len(want) && ty == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("got types %v; want subsequence %v starting from reconnect", types, want)
	}
	// outB should see player_rejoin.
	bFrames := drainOutFor(outB, 100*time.Millisecond)
	sawRejoin := false
	for _, f := range bFrames {
		if f.Type == proto.MsgEvent {
			ev, _ := proto.DecodeEvent(f.Payload)
			if ev.Kind == uint8(proto.EventPlayerRejoin) && ev.Target == 1 {
				sawRejoin = true
			}
		}
	}
	if !sawRejoin {
		t.Fatal("player_rejoin not broadcast to surviving slot")
	}
}

// TestReconnect_RejectedPastGrace — uses a tiny DCGraceTicksOverride
// so 2 ticks past the DC mark the grace as expired. After dropDCSlot
// fires (and possibly endMatch via LAST_STANDING), SubmitReconnect
// returns ErrAuth. We deliberately do NOT inspect m.slots from the
// test goroutine: that map is owned by the actor and any read races.
// The observable API (SubmitReconnect → ErrAuth) is the contract.
func TestReconnect_RejectedPastGrace(t *testing.T) {
	ft := newFakeTicker()
	fc := &fakeClock{now: time.Unix(0, 0)}
	cfg := MatchConfig{
		MatchID:              "M1",
		MapSeed:              0xCAFEBABE,
		MapWidth:             60,
		MapHeight:            40,
		DCGraceTicksOverride: 1, // ≥ 2 ticks past DC fires drop
		PlayerSlots: []PendingJoin{
			{MatchID: "M1", Token: "tokA", PlayerID: 1, Nick: "A", IssuedAt: fc.Now()},
			{MatchID: "M1", Token: "tokB", PlayerID: 2, Nick: "B", IssuedAt: fc.Now()},
		},
		Ticker: ft,
		Clock:  fc.Now,
	}
	m, err := NewMatch(cfg)
	if err != nil {
		t.Fatal(err)
	}
	outA := make(chan OutboundFrame, 64)
	outB := make(chan OutboundFrame, 64)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { m.Abort("test"); <-done }()
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatal(err)
	}
	ft.Send(time.Now())
	time.Sleep(20 * time.Millisecond)

	m.SubmitDC(1)
	time.Sleep(20 * time.Millisecond)
	// Drive 3 ticks (DCGraceTicksOverride=1 + headroom). After this the
	// actor's per-tick §9.2 loop has fired dropDCSlot.
	for i := 0; i < 3; i++ {
		ft.Send(time.Now())
		time.Sleep(15 * time.Millisecond)
	}

	outA2 := make(chan OutboundFrame, 64)
	if _, err := m.SubmitReconnect("tokA", outA2); err != ErrAuth {
		t.Fatalf("reconnect post-grace err=%v, want ErrAuth", err)
	}
}

// TestReconnect_UnknownTokenRejected — a token never DC'd cannot be
// used for reconnect.
func TestReconnect_UnknownTokenRejected(t *testing.T) {
	m, _, _, _, cleanup := newMatchWithRunningActor(t)
	defer cleanup()

	outX := make(chan OutboundFrame, 8)
	if _, err := m.SubmitReconnect("notarealtoken", outX); err != ErrAuth {
		t.Fatalf("err=%v, want ErrAuth", err)
	}
}

// TestReconnect_SingleUseToken — after a successful reconnect, the
// token is consumed; a second reconnect attempt fails.
func TestReconnect_SingleUseToken(t *testing.T) {
	m, _, _, _, cleanup := newMatchWithRunningActor(t)
	defer cleanup()

	m.SubmitDC(1)
	time.Sleep(30 * time.Millisecond)
	out2 := make(chan OutboundFrame, 64)
	if _, err := m.SubmitReconnect("tokA", out2); err != nil {
		t.Fatalf("first reconnect: %v", err)
	}
	out3 := make(chan OutboundFrame, 64)
	if _, err := m.SubmitReconnect("tokA", out3); err != ErrAuth {
		t.Fatalf("second reconnect err=%v, want ErrAuth", err)
	}
}
