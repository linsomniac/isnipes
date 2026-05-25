// Package observ is the process-wide, dependency-free observability
// surface for isnipes: a lock-free tick-duration histogram, an exact
// sampler for test gates, and a Prometheus text-exposition /metrics
// registry. It imports nothing outside the standard library (PHASE8 §6,
// open question §19.1).
package observ

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

// histogramBounds are the inclusive upper bounds of the finite buckets,
// ascending. They bracket the 33 ms tick budget so a quantile estimate is
// meaningful around the < 10 ms / < 5 ms thresholds (PHASE8 §6.1). An
// implicit +Inf bucket follows.
var histogramBounds = []time.Duration{
	100 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	20 * time.Millisecond,
	33 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
}

// Histogram is a lock-free fixed-bucket latency histogram. Observe is safe
// for concurrent callers (atomic bucket increments). Quantile/Count read a
// race-free atomic snapshot. It is an OBSERVABILITY surface — its Quantile
// is a bucket-interpolated ESTIMATE and must not be used for a strict
// pass/fail gate (use RecordingSampler for that; PHASE8 §6.1).
type Histogram struct {
	bounds []time.Duration
	counts []atomic.Uint64 // len(bounds)+1; last is the +Inf bucket
	sumNS  atomic.Uint64
	total  atomic.Uint64
}

// NewLatencyHistogram returns a Histogram with the tick-budget buckets.
func NewLatencyHistogram() *Histogram {
	return &Histogram{
		bounds: histogramBounds,
		counts: make([]atomic.Uint64, len(histogramBounds)+1),
	}
}

// bucketIndex returns the bucket d falls into (first bound where d <= bound,
// else the +Inf bucket).
func (h *Histogram) bucketIndex(d time.Duration) int {
	for i, b := range h.bounds {
		if d <= b {
			return i
		}
	}
	return len(h.bounds)
}

// Observe records a single duration. Negative durations are clamped to 0.
func (h *Histogram) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.counts[h.bucketIndex(d)].Add(1)
	h.sumNS.Add(uint64(d.Nanoseconds()))
	h.total.Add(1)
}

// Count returns the number of observations.
func (h *Histogram) Count() uint64 { return h.total.Load() }

// Sum returns the summed duration of all observations.
func (h *Histogram) Sum() time.Duration { return time.Duration(h.sumNS.Load()) }

// Reset zeroes every bucket and the running sum/count.
func (h *Histogram) Reset() {
	for i := range h.counts {
		h.counts[i].Store(0)
	}
	h.sumNS.Store(0)
	h.total.Store(0)
}

// snapshot returns a consistent-enough copy of bucket counts and the total.
func (h *Histogram) snapshot() (counts []uint64, total uint64) {
	counts = make([]uint64, len(h.counts))
	for i := range h.counts {
		counts[i] = h.counts[i].Load()
	}
	return counts, h.total.Load()
}

// Quantile estimates the q-quantile (0..1) via linear interpolation within
// the bracketing bucket. Returns 0 with no data. A quantile landing in the
// +Inf bucket returns the last finite bound (a conservative lower estimate;
// documented as an estimate). This is for /metrics reporting only.
func (h *Histogram) Quantile(q float64) time.Duration {
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	counts, total := h.snapshot()
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var cum float64
	for i, c := range counts {
		prevCum := cum
		cum += float64(c)
		if cum < target || c == 0 {
			continue
		}
		// target is within bucket i.
		if i == len(h.bounds) {
			// +Inf bucket: no finite upper edge to interpolate toward.
			return h.bounds[len(h.bounds)-1]
		}
		lower := time.Duration(0)
		if i > 0 {
			lower = h.bounds[i-1]
		}
		upper := h.bounds[i]
		frac := (target - prevCum) / float64(c)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		return lower + time.Duration(frac*float64(upper-lower))
	}
	return h.bounds[len(h.bounds)-1]
}

// Sampler is the minimal interface internal/match depends on so the match
// package never imports the metrics/HTTP surface. A nil Sampler is a valid
// no-op (the actor checks for nil before calling).
type Sampler interface{ Observe(d time.Duration) }

// RecordingSampler keeps every observed duration so callers can compute an
// EXACT quantile. The smoke gate injects this (not the bucketed Histogram)
// so its P99 assertion cannot be smeared by bucket interpolation
// (PHASE8 §6.1, §8). Safe for concurrent Observe.
type RecordingSampler struct {
	mu      sync.Mutex
	samples []time.Duration
}

// Observe appends a sample.
func (s *RecordingSampler) Observe(d time.Duration) {
	s.mu.Lock()
	s.samples = append(s.samples, d)
	s.mu.Unlock()
}

// Len returns the number of recorded samples.
func (s *RecordingSampler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.samples)
}

// Quantile returns the exact q-quantile (nearest-rank) over a sorted copy
// of the samples; 0 with no data.
func (s *RecordingSampler) Quantile(q float64) time.Duration {
	if q < 0 {
		q = 0
	}
	if q > 1 {
		q = 1
	}
	s.mu.Lock()
	cp := make([]time.Duration, len(s.samples))
	copy(cp, s.samples)
	s.mu.Unlock()
	n := len(cp)
	if n == 0 {
		return 0
	}
	slices.Sort(cp)
	// Nearest-rank: rank = ceil(q*n), 1-based; clamp to [1,n].
	rank := int(float64(n)*q + 0.9999999999)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return cp[rank-1]
}
