package sim

// Per-entity position-history ring for §3.4 lag compensation.
// PHASE4.md §6 — every live entity carries a HistoryDepth-entry ring
// indexed by serverTick; resolveProjectile rewinds candidate positions
// to tick T_view when a player-fired projectile has captured OWT > 0.

// LagCompTicks is the maximum rewind window for lag-compensated hit
// detection, in sim ticks. SPEC §3.4 / PHASE4.md §5.1 == 8.
const LagCompTicks = 8

// InterpTicks is the client-interpolation offset added to OWT-derived
// rewind. SPEC §3.4 / PHASE4.md §5.1 == 2.
const InterpTicks = 2

// HistoryDepth is the number of samples the per-entity ring keeps,
// retaining ticks [T_now - LagCompTicks, T_now] inclusive (== 9).
const HistoryDepth = LagCompTicks + 1

// histSample is one position sample written at the end of a tick's
// movement phase (PHASE4.md §6.2 step 4.9).
type histSample struct {
	tick  uint32
	x, y  int32
	kind  EntityKind
	flags uint8
}

// entityHistory is a fixed-capacity ring of HistoryDepth samples.
// head indexes the newest sample; the next write goes to
// (head + 1) % HistoryDepth. count saturates at HistoryDepth.
type entityHistory struct {
	samples [HistoryDepth]histSample
	head    uint8
	count   uint8
}

// write appends a sample for serverTick t to the ring. The newest
// sample lands at index (head+1) % HistoryDepth except on first
// write where it lands at index 0.
func (h *entityHistory) write(t uint32, x, y int32, kind EntityKind, flags uint8) {
	var next uint8
	if h.count == 0 {
		next = 0
	} else {
		next = (h.head + 1) % HistoryDepth
	}
	h.samples[next] = histSample{tick: t, x: x, y: y, kind: kind, flags: flags}
	h.head = next
	if h.count < HistoryDepth {
		h.count++
	}
}

// at returns the sample for tick t. If t is older than the oldest
// sample the ring holds, at returns the oldest valid sample (the
// §3.4 clamp — "a shot whose ideal rewind would exceed the max is
// tested at the oldest available sample"). ok is false only when
// the ring has no samples at all.
func (h *entityHistory) at(t uint32) (histSample, bool) {
	if h.count == 0 {
		return histSample{}, false
	}
	// Walk newest → oldest. Stop on first sample whose tick <= t;
	// that is the requested-or-most-recent-before-t.
	for i := uint8(0); i < h.count; i++ {
		idx := (h.head + HistoryDepth - i) % HistoryDepth
		s := h.samples[idx]
		if s.tick <= t {
			return s, true
		}
	}
	// Requested tick is older than every sample we hold.
	// Return the oldest sample (the clamp).
	oldestIdx := (h.head + HistoryDepth - (h.count - 1)) % HistoryDepth
	return h.samples[oldestIdx], true
}

// reset clears the ring without freeing it. Used on respawn so old
// pre-death samples cannot rewind into a hit on the freshly-spawned
// entity (PHASE4.md §6.4).
func (h *entityHistory) reset() {
	h.head = 0
	h.count = 0
}
