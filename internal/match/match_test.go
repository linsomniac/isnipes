package match

import (
	"sync"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// fakeTicker is the test ticker. Caller drives ticks via Send().
type fakeTicker struct {
	c chan time.Time
}

func newFakeTicker() *fakeTicker          { return &fakeTicker{c: make(chan time.Time, 16)} }
func (f *fakeTicker) C() <-chan time.Time { return f.c }
func (f *fakeTicker) Stop()               {}
func (f *fakeTicker) Send(t time.Time)    { f.c <- t }

// drainOut collects every frame for a slot, with timeout, until the
// slot's outbound channel is closed by Match.endMatch.
func drainOut(t *testing.T, ch <-chan OutboundFrame, timeout time.Duration) []OutboundFrame {
	t.Helper()
	var out []OutboundFrame
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, f)
		case <-timer.C:
			return out
		}
	}
}

// twoPlayerSetup builds a Match with two pending join slots and
// returns the match, fakeTicker, fake clock, and the two outbound
// channels.
func twoPlayerSetup(t *testing.T) (*Match, *fakeTicker, *fakeClock, chan OutboundFrame, chan OutboundFrame) {
	t.Helper()
	ft := newFakeTicker()
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
		Ticker: ft,
		Clock:  fc.Now,
	}
	m, err := NewMatch(cfg)
	if err != nil {
		t.Fatalf("NewMatch: %v", err)
	}
	outA := make(chan OutboundFrame, 64)
	outB := make(chan OutboundFrame, 64)
	return m, ft, fc, outA, outB
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMatchActorJoinDeliversMapInitSnapshotScoreboard(t *testing.T) {
	m, _, _, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { <-done }()
	defer m.Abort("test cleanup")

	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatalf("join A: %v", err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatalf("join B: %v", err)
	}
	if m.State() != StateLive {
		t.Fatalf("state = %v, want LIVE", m.State())
	}
	// Give the actor time to send initial frames.
	time.Sleep(50 * time.Millisecond)
	// Trigger abort, which closes outA/outB; drainOut sees closure.
	m.Abort("test")
	aFrames := drainOut(t, outA, 2*time.Second)
	bFrames := drainOut(t, outB, 2*time.Second)
	for _, frames := range [][]OutboundFrame{aFrames, bFrames} {
		if !hasFrameOfType(frames, proto.MsgMapInit) {
			t.Fatalf("missing MapInit: types=%v", frameTypes(frames))
		}
		if !hasFrameOfType(frames, proto.MsgSnapshot) {
			t.Fatalf("missing Snapshot")
		}
		if !hasFrameOfType(frames, proto.MsgScoreboard) {
			t.Fatalf("missing Scoreboard")
		}
	}
}

func hasFrameOfType(frames []OutboundFrame, t proto.MsgType) bool {
	for _, f := range frames {
		if f.Type == t {
			return true
		}
	}
	return false
}

func frameTypes(frames []OutboundFrame) []proto.MsgType {
	out := make([]proto.MsgType, len(frames))
	for i, f := range frames {
		out[i] = f.Type
	}
	return out
}

func TestMatchActorBroadcastsSnapshotEvery2Ticks(t *testing.T) {
	m, ft, fc, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { <-done }()
	defer m.Abort("test")

	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	// Send 8 ticks.
	for i := 0; i < 8; i++ {
		fc.Advance(33 * time.Millisecond)
		ft.Send(fc.Now())
		time.Sleep(5 * time.Millisecond)
	}

	// Abort to close channels for draining.
	m.Abort("test")

	aFrames := drainOut(t, outA, 2*time.Second)
	bFrames := drainOut(t, outB, 2*time.Second)

	countSnap := func(fs []OutboundFrame) int {
		n := 0
		for _, f := range fs {
			if f.Type == proto.MsgSnapshot {
				n++
			}
		}
		return n
	}
	nA := countSnap(aFrames)
	nB := countSnap(bFrames)
	// Ticks 2, 4, 6, 8 trigger broadcasts → 4 per recipient. Plus the
	// initial snapshot at handshake.
	if nA < 4 || nB < 4 {
		t.Fatalf("snapshot counts: A=%d B=%d, want ≥4 each", nA, nB)
	}
}

func TestMatchJoinRejectsUnknownToken(t *testing.T) {
	m, _, _, outA, _ := twoPlayerSetup(t)
	go m.Run()
	defer m.Abort("test cleanup")
	if _, err := m.SubmitJoin("bogus", outA); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestMatchJoinRejectsExpiredToken(t *testing.T) {
	ft := newFakeTicker()
	fc := &fakeClock{now: time.Unix(0, 0)}
	cfg := MatchConfig{
		MatchID:  "M1",
		MapSeed:  1,
		MapWidth: 60, MapHeight: 40,
		PlayerSlots: []PendingJoin{
			{MatchID: "M1", Token: "tokA", PlayerID: 1, Nick: "A", IssuedAt: fc.Now()},
			{MatchID: "M1", Token: "tokB", PlayerID: 2, Nick: "B", IssuedAt: fc.Now()},
		},
		Ticker: ft, Clock: fc.Now,
	}
	m, _ := NewMatch(cfg)
	go m.Run()
	defer m.Abort("test")
	// Advance past 60s TTL.
	fc.Advance(61 * time.Second)
	out := make(chan OutboundFrame, 4)
	if _, err := m.SubmitJoin("tokA", out); err == nil {
		t.Fatalf("expected expired-token error, got nil")
	}
}

func TestMatchJoinRejectsLateJoin(t *testing.T) {
	// 3-slot match: 2 join immediately, transitioning to LIVE; the
	// third (still-valid token) is then rejected.
	ft := newFakeTicker()
	fc := &fakeClock{now: time.Unix(0, 0)}
	cfg := MatchConfig{
		MatchID:  "M1",
		MapSeed:  1,
		MapWidth: 60, MapHeight: 40,
		PlayerSlots: []PendingJoin{
			{MatchID: "M1", Token: "tokA", PlayerID: 1, Nick: "A", IssuedAt: fc.Now()},
			{MatchID: "M1", Token: "tokB", PlayerID: 2, Nick: "B", IssuedAt: fc.Now()},
			{MatchID: "M1", Token: "tokC", PlayerID: 3, Nick: "C", IssuedAt: fc.Now()},
		},
		Ticker: ft, Clock: fc.Now,
	}
	m, err := NewMatch(cfg)
	if err != nil {
		t.Fatal(err)
	}
	go m.Run()
	defer m.Abort("test")
	outA := make(chan OutboundFrame, 64)
	outB := make(chan OutboundFrame, 64)
	outC := make(chan OutboundFrame, 64)
	_, _ = m.SubmitJoin("tokA", outA)
	_, _ = m.SubmitJoin("tokB", outB)
	// Advance warmup timeout to force StateLive even with 2 of 3.
	fc.Advance(matchWarmupTimeout + time.Second)
	ft.Send(fc.Now())
	// Wait for state transition.
	for i := 0; i < 50; i++ {
		if m.State() == StateLive {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m.State() != StateLive {
		t.Fatalf("expected StateLive, got %v", m.State())
	}
	// Late join must be rejected.
	if _, err := m.SubmitJoin("tokC", outC); err == nil {
		t.Fatalf("late join was accepted")
	}
}

func TestMatchJoinRejectsReusedToken(t *testing.T) {
	m, _, _, outA, outB := twoPlayerSetup(t)
	go m.Run()
	defer m.Abort("test cleanup")
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitJoin("tokA", outB); err == nil {
		t.Fatalf("expected reuse error")
	}
}

func TestEntityIDOrderAscendingInSnapshot(t *testing.T) {
	m, _, _, outA, outB := twoPlayerSetup(t)
	go m.Run()
	defer m.Abort("test cleanup")
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	frames := drainOut(t, outA, 500*time.Millisecond)
	for _, f := range frames {
		if f.Type != proto.MsgSnapshot {
			continue
		}
		s, err := proto.DecodeSnapshot(f.Payload)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		for i := 1; i < len(s.Entities); i++ {
			if s.Entities[i-1].ID >= s.Entities[i].ID {
				t.Fatalf("unsorted entity IDs at snap server_tick=%d: %v",
					s.ServerTick, s.Entities)
			}
		}
	}
}

func TestSnapshotPerRecipientYourEntityID(t *testing.T) {
	m, _, _, outA, outB := twoPlayerSetup(t)
	go m.Run()
	defer m.Abort("test cleanup")
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	aFrames := drainOut(t, outA, 500*time.Millisecond)
	bFrames := drainOut(t, outB, 500*time.Millisecond)
	for _, f := range aFrames {
		if f.Type != proto.MsgSnapshot {
			continue
		}
		s, _ := proto.DecodeSnapshot(f.Payload)
		if s.YourEntityID != 1 {
			t.Fatalf("A: your_entity_id = %d, want 1", s.YourEntityID)
		}
	}
	for _, f := range bFrames {
		if f.Type != proto.MsgSnapshot {
			continue
		}
		s, _ := proto.DecodeSnapshot(f.Payload)
		if s.YourEntityID != 2 {
			t.Fatalf("B: your_entity_id = %d, want 2", s.YourEntityID)
		}
	}
}

// Test that the actor's ServerTick equals sim's after one tick.
func TestMatchTickInSync(t *testing.T) {
	m, ft, fc, outA, outB := twoPlayerSetup(t)
	go m.Run()
	defer m.Abort("test cleanup")
	_, _ = m.SubmitJoin("tokA", outA)
	_, _ = m.SubmitJoin("tokB", outB)
	time.Sleep(50 * time.Millisecond)
	fc.Advance(33 * time.Millisecond)
	ft.Send(fc.Now())
	time.Sleep(50 * time.Millisecond)
	if got := m.ServerTick(); got != 1 {
		t.Fatalf("ServerTick = %d, want 1", got)
	}
}

func TestRoundTripPlayerInputThroughActor(t *testing.T) {
	m, ft, fc, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { <-done }()
	_, _ = m.SubmitJoin("tokA", outA)
	_, _ = m.SubmitJoin("tokB", outB)
	time.Sleep(50 * time.Millisecond)
	m.SubmitInput(1, proto.Input{ClientTick: 42, Dir: uint8(sim.DirE)})
	// Two ticks: input consumed on tick 1; broadcast snapshot on tick 2.
	for i := 0; i < 2; i++ {
		fc.Advance(33 * time.Millisecond)
		ft.Send(fc.Now())
		time.Sleep(20 * time.Millisecond)
	}
	m.Abort("test")
	frames := drainOut(t, outA, 1*time.Second)
	for _, f := range frames {
		if f.Type != proto.MsgSnapshot {
			continue
		}
		s, _ := proto.DecodeSnapshot(f.Payload)
		if s.ServerTick >= 1 && s.YourLastInputTick == 42 {
			return
		}
	}
	t.Fatalf("input client_tick=42 did not surface in snapshot; got %d frames", len(frames))
}
