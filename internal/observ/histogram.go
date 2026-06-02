// Package observ is the process-wide, dependency-free observability
// surface for isnipes: a lock-free tick-duration histogram, an exact
// sampler for test gates, and a Prometheus text-exposition /metrics
// registry. It imports nothing outside the standard library (PHASE8 §6,
// open question §19.1).
package observ

import (
	"math"
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
// The observation count is derived from the buckets (not a separate atomic),
// so the exported _count always equals the +Inf bucket — the Prometheus
// histogram invariant cannot be transiently violated (codex P1).
func (h *Histogram) Observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.counts[h.bucketIndex(d)].Add(1)
	h.sumNS.Add(uint64(d.Nanoseconds()))
}

// Count returns the number of observations (sum of all buckets).
func (h *Histogram) Count() uint64 {
	_, total := h.snapshot()
	return total
}

// Sum returns the summed duration of all observations. (Sum may skew from
// Count by at most one in-flight observation under concurrent Observe; the
// hard invariant _count == +Inf bucket is preserved because both derive
// from the same snapshot.)
func (h *Histogram) Sum() time.Duration { return time.Duration(h.sumNS.Load()) }

// Reset zeroes every bucket and the running sum. NOT safe to call
// concurrently with Observe — intended for quiescent reuse (tests). Calling
// it while observations are in flight may leave sum and bucket counts
// momentarily inconsistent (codex P2).
func (h *Histogram) Reset() {
	for i := range h.counts {
		h.counts[i].Store(0)
	}
	h.sumNS.Store(0)
}

// snapshot returns a copy of bucket counts and their sum (the observation
// total). Deriving total from the same loaded counts guarantees coherence
// between the +Inf bucket and _count.
func (h *Histogram) snapshot() (counts []uint64, total uint64) {
	counts = make([]uint64, len(h.counts))
	for i := range h.counts {
		c := h.counts[i].Load()
		counts[i] = c
		total += c
	}
	return counts, total
}

// Quantile estimates the q-quantile (0..1) via linear interpolation within
// the bracketing bucket. Returns 0 with no data. A quantile landing in the
// +Inf bucket returns the last finite bound (a conservative lower estimate;
// documented as an estimate). This is for /metrics reporting only.
func (h *Histogram) Quantile(q float64) time.Duration {
	q = clampQuantile(q)
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
	cap     int // 0 = unbounded (exact); >0 = retain only the most-recent cap
	next    int // ring cursor, used once len(samples) == cap
}

// SetCap bounds retention to the most-recent n observations (0, the default,
// keeps every sample for an exact quantile). Once n are held, each further
// Observe overwrites the oldest in place — constant memory, no allocation.
// Call before the first Observe. Intended for long soak runs, where unbounded
// exact retention is itself a leak: the measuring instrument must not outgrow
// the server it measures (see the loadtest driver and client.go's sendAt
// bound). Quantiles then cover a trailing window rather than the whole run.
func (s *RecordingSampler) SetCap(n int) {
	s.mu.Lock()
	s.cap = n
	s.mu.Unlock()
}

// Observe records a sample. Unbounded by default; with a cap set it retains
// only the most-recent cap samples via in-place ring overwrite. Quantile sorts
// a copy, so the ring's write order does not affect the result.
func (s *RecordingSampler) Observe(d time.Duration) {
	s.mu.Lock()
	switch {
	case s.cap <= 0 || len(s.samples) < s.cap:
		s.samples = append(s.samples, d)
	default:
		s.samples[s.next] = d
		s.next++
		if s.next == s.cap {
			s.next = 0
		}
	}
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
	q = clampQuantile(q)
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
	rank := int(math.Ceil(float64(n) * q))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return cp[rank-1]
}

// clampQuantile maps q into [0,1]; NaN → 0.
func clampQuantile(q float64) float64 {
	if math.IsNaN(q) || q < 0 {
		return 0
	}
	if q > 1 {
		return 1
	}
	return q
}
