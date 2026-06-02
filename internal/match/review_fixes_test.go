package match

import (
	"testing"
	"time"
)

// Regression tests for the review fixes around disconnect-during-warmup and
// SubmitJoin after graceful shutdown. They use only the public Submit* API +
// the untagged two-player harness, so they run in both the default and
// testhooks race gates.

// TestWarmupDC_ReconnectDuringWarmupIsCleanErrAuth — a DC while the match is
// still StateWaitingForJoins (sim==nil) must NOT register a reconnect token
// that a subsequent reconnect dereferences against a nil sim. The reconnect
// returns ErrAuth cleanly and the match stays in warmup (no SERVER_ERROR
// abort). Previously handleReconnect nil-dereferenced m.sim and the panic
// recovery aborted the match.
func TestWarmupDC_ReconnectDuringWarmupIsCleanErrAuth(t *testing.T) {
	m, _, _, outA, _ := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { m.Abort("test"); <-done }()

	// Only A joins → still StateWaitingForJoins, sim==nil.
	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatalf("join A: %v", err)
	}
	m.SubmitDC(1) // A's WS drops during warmup (async, FIFO-ordered before the reconnect)

	outA2 := make(chan OutboundFrame, 64)
	if _, err := m.SubmitReconnect("tokA", outA2); err != ErrAuth {
		t.Fatalf("warmup reconnect err = %v, want ErrAuth", err)
	}
	if st := m.State(); st == StateEnded {
		t.Fatal("match aborted on a warmup DC+reconnect; want still warming up (nil-deref regression)")
	}
}

// TestWarmupDC_ArmedNotDroppedOnFirstLiveTick — a slot that DC'd during
// warmup must, once the match goes live, get its grace deadline armed from
// the first live tick (not left at 0). Previously DCDeadlineTick==0 made the
// §9.2 drop loop treat it as already-expired, dropping the slot on tick 1 and
// instantly ending a 2-player match via LAST_STANDING. The slot must also be
// reconnectable within grace (proving the token was registered at start).
func TestWarmupDC_ArmedNotDroppedOnFirstLiveTick(t *testing.T) {
	m, ft, _, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	defer func() { m.Abort("test"); <-done }()

	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatalf("join A: %v", err)
	}
	m.SubmitDC(1) // A drops during warmup (sim==nil)
	// B joins → allSlotsJoined → startOrAbort → StateLive, arming A's grace.
	if _, err := m.SubmitJoin("tokB", outB); err != nil {
		t.Fatalf("join B: %v", err)
	}
	if st := m.State(); st != StateLive {
		t.Fatalf("state after start = %v, want StateLive", st)
	}

	// One live tick must NOT drop A (armed to a future deadline).
	ft.Send(time.Now())
	time.Sleep(20 * time.Millisecond)
	if st := m.State(); st != StateLive {
		t.Fatalf("state after first tick = %v, want StateLive (A dropped on tick 1 → LAST_STANDING)", st)
	}

	// A reconnects within grace: confirms the token was registered at start
	// and the now-live sim deref is safe.
	outA2 := make(chan OutboundFrame, 64)
	if pid, err := m.SubmitReconnect("tokA", outA2); err != nil || pid != 1 {
		t.Fatalf("reconnect after warmup-DC start: pid=%d err=%v, want 1/nil", pid, err)
	}
}

// TestSubmitJoinAfterGracefulShutdownReturnsErrAuth — a ctlJoin that races a
// graceful shutdown (which skips the tail absorber) must return ErrAuth
// promptly via the m.done guard, not block forever on a reply no one sends.
func TestSubmitJoinAfterGracefulShutdownReturnsErrAuth(t *testing.T) {
	m, _, _, outA, outB := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()

	if _, err := m.SubmitJoin("tokA", outA); err != nil {
		t.Fatalf("join A: %v", err)
	}
	m.in <- ctlShutdown{} // graceful shutdown (as Registry.StopAll would)
	<-done                // actor exited; m.done closed, no absorber spawned

	res := make(chan error, 1)
	go func() {
		_, err := m.SubmitJoin("tokB", outB)
		res <- err
	}()
	select {
	case err := <-res:
		if err != ErrAuth {
			t.Fatalf("SubmitJoin after shutdown err = %v, want ErrAuth", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitJoin after graceful shutdown hung (missing m.done guard)")
	}
}
