package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/sim"
)

func TestOWT_FirstObservation(t *testing.T) {
	o := NewOWTEstimator()
	o.ObservePong(100)
	if got := o.OWTMs(); got != 50 {
		t.Fatalf("OWTMs after first 100ms RTT = %d, want 50", got)
	}
}

func TestOWT_EWMAConverges(t *testing.T) {
	o := NewOWTEstimator()
	o.ObservePong(200) // first: seeds smoothedMs = 100
	for i := 0; i < 100; i++ {
		o.ObservePong(200)
	}
	if got := o.OWTMs(); got != 100 {
		t.Fatalf("converged OWTMs=%d, want 100", got)
	}
}

func TestOWT_TickConversion(t *testing.T) {
	cases := []struct {
		rtt   uint32 // first-observation seed: smoothedMs = rtt/2
		ticks uint8
	}{
		{rtt: 0, ticks: 0},    // 0 ms
		{rtt: 32, ticks: 0},   // 16 ms → 0.48 ticks → 0
		{rtt: 34, ticks: 1},   // 17 ms → 0.51 ticks → 1
		{rtt: 100, ticks: 2},  // 50 ms → 1.50 ticks → 2 (round-half-up)
		{rtt: 134, ticks: 2},  // 67 ms → 2.01 ticks → 2
		{rtt: 200, ticks: 3},  // 100 ms → 3.0 ticks → 3
		{rtt: 1000, ticks: 8}, // 500 ms → 15 ticks → clamped to 8
	}
	for _, tc := range cases {
		o := NewOWTEstimator()
		o.ObservePong(tc.rtt)
		if got := o.OWTTicks(); got != tc.ticks {
			t.Errorf("rtt=%d ms: OWTTicks=%d want %d", tc.rtt, got, tc.ticks)
		}
	}
}

func TestOWT_TicksClamped(t *testing.T) {
	o := NewOWTEstimator()
	o.ObservePong(2000)
	if got := o.OWTTicks(); got != sim.LagCompTicks {
		t.Fatalf("OWTTicks=%d want clamp=%d", got, sim.LagCompTicks)
	}
}

// TestOWT_RTTSampleClamped: an adversarial RTT (e.g. uint32 max from
// a forged client timestamp) does not overflow the EWMA math.
func TestOWT_RTTSampleClamped(t *testing.T) {
	o := NewOWTEstimator()
	o.ObservePong(1 << 31) // larger than maxObservedRTTMs
	// Should clamp internally and converge to a sane value.
	if got := o.OWTMs(); got > maxObservedRTTMs {
		t.Fatalf("OWTMs=%d unclamped (> %d)", got, maxObservedRTTMs)
	}
	if got := o.OWTTicks(); got != sim.LagCompTicks {
		t.Fatalf("OWTTicks=%d, want clamp=%d", got, sim.LagCompTicks)
	}
	// Hammer with adversarial samples; smoothed value stays bounded.
	for i := 0; i < 100; i++ {
		o.ObservePong(0xFFFFFFFF)
	}
	if got := o.OWTMs(); got > maxObservedRTTMs {
		t.Fatalf("after hammer: OWTMs=%d unclamped", got)
	}
}

func TestOWT_ZeroBeforeFirstPong(t *testing.T) {
	o := NewOWTEstimator()
	if got := o.OWTTicks(); got != 0 {
		t.Fatalf("pre-pong OWTTicks=%d want 0", got)
	}
	if got := o.OWTMs(); got != 0 {
		t.Fatalf("pre-pong OWTMs=%d want 0", got)
	}
}

// TestMatch_PongDrivesOWT exercises the ctlPong → OWTEstimator
// plumbing without spinning up the Run goroutine. Tests in the same
// package can drive handleControl synchronously.
func TestMatch_PongDrivesOWT(t *testing.T) {
	m, _, _, _, _ := twoPlayerSetup(t)
	// Synchronously dispatch ctlPong; equivalent to the actor's
	// in-channel path but without goroutine concurrency.
	m.handleControl(ctlPong{PlayerID: 1, RTTMs: 80})
	if got := m.owt[1].OWTMs(); got != 40 {
		t.Fatalf("player 1 OWTMs=%d, want 40", got)
	}
	if got := m.owt[2].OWTMs(); got != 0 {
		t.Fatalf("player 2 OWTMs=%d, want 0 (no Pong observed)", got)
	}
}

// TestMatch_OWTPerConn verifies per-player OWT isolation: two
// players at different RTTs maintain independent EWMA estimates.
func TestMatch_OWTPerConn(t *testing.T) {
	m, _, _, _, _ := twoPlayerSetup(t)
	m.handleControl(ctlPong{PlayerID: 1, RTTMs: 60})
	m.handleControl(ctlPong{PlayerID: 2, RTTMs: 200})
	if got := m.owt[1].OWTTicks(); got != 1 {
		t.Fatalf("p1 OWTTicks=%d, want 1 (30ms ≈ 0.9 ticks → 1)", got)
	}
	if got := m.owt[2].OWTTicks(); got != 3 {
		t.Fatalf("p2 OWTTicks=%d, want 3 (100ms = 3 ticks)", got)
	}
}
