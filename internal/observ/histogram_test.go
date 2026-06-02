package observ

import (
	"sync"
	"testing"
	"time"
)

// TestObserv_HistogramQuantileEstimate — known samples → Quantile(0.99)
// lands in the bracketing bucket; Count is exact; Reset clears. (DoD #7)
func TestObserv_HistogramQuantileEstimate(t *testing.T) {
	h := NewLatencyHistogram()
	// 98 fast ticks at ~1ms, 2 slow ticks at 15ms → the 99th-ranked value
	// (P99 over 100 samples) falls in the (10ms,20ms] bucket.
	for i := 0; i < 98; i++ {
		h.Observe(time.Millisecond)
	}
	h.Observe(15 * time.Millisecond)
	h.Observe(15 * time.Millisecond)

	if got := h.Count(); got != 100 {
		t.Fatalf("Count=%d want 100", got)
	}
	q := h.Quantile(0.99)
	if q <= 10*time.Millisecond || q > 20*time.Millisecond {
		t.Fatalf("Quantile(0.99)=%v want in (10ms,20ms]", q)
	}
	// P50 should be at/around the 1ms bucket.
	if p50 := h.Quantile(0.50); p50 > time.Millisecond {
		t.Fatalf("Quantile(0.50)=%v want <=1ms", p50)
	}

	h.Reset()
	if got := h.Count(); got != 0 {
		t.Fatalf("after Reset Count=%d want 0", got)
	}
	if q := h.Quantile(0.99); q != 0 {
		t.Fatalf("after Reset Quantile=%v want 0", q)
	}
}

// TestObserv_HistogramInfBucketAndEmpty — empty histogram returns 0; a
// quantile in the +Inf bucket returns the last finite bound.
func TestObserv_HistogramInfBucketAndEmpty(t *testing.T) {
	h := NewLatencyHistogram()
	if q := h.Quantile(0.5); q != 0 {
		t.Fatalf("empty Quantile=%v want 0", q)
	}
	h.Observe(time.Second) // beyond 100ms → +Inf bucket
	if q := h.Quantile(0.99); q != 100*time.Millisecond {
		t.Fatalf("Quantile in +Inf bucket=%v want 100ms (last finite bound)", q)
	}
}

// TestObserv_RecordingSamplerExact — exact nearest-rank quantile. (DoD #7)
func TestObserv_RecordingSamplerExact(t *testing.T) {
	var s RecordingSampler
	// Insert 1..100 ms out of order.
	for i := 100; i >= 1; i-- {
		s.Observe(time.Duration(i) * time.Millisecond)
	}
	if s.Len() != 100 {
		t.Fatalf("Len=%d want 100", s.Len())
	}
	// nearest-rank: ceil(0.99*100)=99 → 99th smallest = 99ms.
	if q := s.Quantile(0.99); q != 99*time.Millisecond {
		t.Fatalf("Quantile(0.99)=%v want 99ms", q)
	}
	if q := s.Quantile(0.50); q != 50*time.Millisecond {
		t.Fatalf("Quantile(0.50)=%v want 50ms", q)
	}
	if q := s.Quantile(1.0); q != 100*time.Millisecond {
		t.Fatalf("Quantile(1.0)=%v want 100ms", q)
	}
	var empty RecordingSampler
	if q := empty.Quantile(0.99); q != 0 {
		t.Fatalf("empty sampler Quantile=%v want 0", q)
	}
}

// TestObserv_RecordingSamplerBounded — SetCap bounds retention to the most
// recent N observations so a long soak run cannot let the measuring instrument
// outgrow the server it measures. Under the cap it is still exact; past it,
// Len plateaus at the cap and the retained window is the trailing tail.
func TestObserv_RecordingSamplerBounded(t *testing.T) {
	var s RecordingSampler
	s.SetCap(100)

	// Under the cap: identical to the unbounded sampler.
	for i := 1; i <= 100; i++ {
		s.Observe(time.Duration(i) * time.Millisecond)
	}
	if s.Len() != 100 {
		t.Fatalf("at cap: Len=%d want 100", s.Len())
	}
	if q := s.Quantile(1.0); q != 100*time.Millisecond {
		t.Fatalf("at cap: Quantile(1.0)=%v want 100ms", q)
	}

	// Past the cap: 901..1000ms remain, so Len holds at 100 and the floor of
	// the retained window has advanced from 1ms to 901ms.
	for i := 101; i <= 1000; i++ {
		s.Observe(time.Duration(i) * time.Millisecond)
	}
	if s.Len() != 100 {
		t.Fatalf("past cap: Len=%d want 100 (bounded)", s.Len())
	}
	if q := s.Quantile(1.0); q != 1000*time.Millisecond {
		t.Fatalf("past cap: Quantile(1.0)=%v want 1000ms (newest retained)", q)
	}
	if q := s.Quantile(0.01); q != 901*time.Millisecond {
		t.Fatalf("past cap: Quantile(0.01)=%v want 901ms (tail-window floor)", q)
	}
}

// TestObserv_HistogramConcurrentObserve — race-free concurrent Observe;
// total Count equals the number of observations. (run under -race)
func TestObserv_HistogramConcurrentObserve(t *testing.T) {
	h := NewLatencyHistogram()
	const goroutines, each = 8, 1000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				h.Observe(time.Duration(i%5) * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if got, want := h.Count(), uint64(goroutines*each); got != want {
		t.Fatalf("Count=%d want %d", got, want)
	}
}

// Sampler interface is satisfied by both *Histogram and *RecordingSampler.
var _ Sampler = (*Histogram)(nil)
var _ Sampler = (*RecordingSampler)(nil)
