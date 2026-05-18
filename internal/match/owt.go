package match

import "github.com/jafo/isnipes/internal/sim"

// OWTEstimator is a per-connection one-way-time (OWT) estimator
// driven by Ping/Pong round-trip samples. PHASE4.md §7.
//
// Math is integer-only on ms; the EWMA is integer division
// (new = old*4/5 + sample/5, equivalent to α = 0.2). Fractional
// drift below 1 ms is below the per-tick (33.33 ms) quantisation
// anyway and is irrelevant after the OWTTicks() rounding step.
type OWTEstimator struct {
	smoothedMs uint32
	first      bool
}

// NewOWTEstimator returns a freshly-initialised estimator whose
// OWTTicks() is 0 until at least one Pong is observed.
func NewOWTEstimator() *OWTEstimator {
	return &OWTEstimator{first: true}
}

// ObservePong updates the EWMA with one RTT sample (round-trip
// milliseconds). The OWT estimate is RTT / 2.
func (o *OWTEstimator) ObservePong(rttMs uint32) {
	owt := rttMs / 2
	if o.first {
		o.smoothedMs = owt
		o.first = false
		return
	}
	// α = 0.2 → new = old*4/5 + sample/5.
	o.smoothedMs = (o.smoothedMs*4 + owt) / 5
}

// OWTMs returns the current smoothed OWT in milliseconds.
func (o *OWTEstimator) OWTMs() uint32 {
	return o.smoothedMs
}

// OWTTicks returns the OWT rounded to sim ticks (33.33 ms each),
// clamped to sim.LagCompTicks. Before the first Pong observation,
// OWTTicks returns 0 (present-time hit detection).
func (o *OWTEstimator) OWTTicks() uint8 {
	if o.first {
		return 0
	}
	// round(ms / 33.33) == round(ms * 3 / 100); +50 for nearest.
	ticks := (o.smoothedMs*3 + 50) / 100
	if ticks > sim.LagCompTicks {
		return sim.LagCompTicks
	}
	return uint8(ticks)
}
