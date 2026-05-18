package match

// Coverage-targeted tests for the small Match accessors that aren't
// driven by the integration-style tests. The Phase 4 owt round-
// trip already exercises handleControl(ctlPong{}); this file
// targets SubmitPong, MatchID, In, and SubmitClose for ≥ 70%
// statement coverage (DoD #12).

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/sim"
)

func TestMatch_MatchIDAndIn(t *testing.T) {
	m, _, _, _, _ := twoPlayerSetup(t)
	if m.MatchID() != "M1" {
		t.Fatalf("MatchID=%q want M1", m.MatchID())
	}
	if m.In() == nil {
		t.Fatal("In() returned nil")
	}
}

// TestMatch_SubmitPongViaActor exercises the goroutine path: drive
// SubmitPong from outside, let the actor consume the ctlPong.
func TestMatch_SubmitPongViaActor(t *testing.T) {
	m, _, _, _, _ := twoPlayerSetup(t)
	done := make(chan struct{})
	go func() { m.Run(); close(done) }()
	m.SubmitPong(sim.EntityID(1), 100)
	m.SubmitPong(sim.EntityID(99), 200) // unknown player → no-op
	// Stop the actor so we can read owt without racing it.
	m.Abort("test")
	<-done
	if got := m.owt[1].OWTMs(); got != 50 {
		t.Fatalf("OWTMs=%d, want 50 (RTT 100 → OWT 50)", got)
	}
}

// TestNewRealTicker verifies the ticker factory returns a usable
// ticker.
func TestNewRealTicker(t *testing.T) {
	tk := NewRealTicker(50 * time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ticker did not fire within 200ms")
	}
}
