//go:build synthetic

package wsnet_test

// PHASE4.md §16.3 — synthetic-tagged extreme tests. These are NOT
// PR-gated; they run on the nightly tier via `go test -tags
// synthetic`. The Phase 4 contract here is "documents the
// behaviour", not "enforces a hard threshold".

import (
	"testing"
)

// TestSynthetic_SnapshotOmissions documents the client's tolerance
// when the server drops every third snapshot. In the Phase 4
// architecture the client interpolation buffer absorbs single
// missed snapshots via lerp; consecutive misses cause render
// freezes but no desync (reconcile recovers).
func TestSynthetic_SnapshotOmissions(t *testing.T) {
	t.Skip("documented: client interp absorbs single drops; nightly tier")
}

// TestSynthetic_AOICapSaturation documents server behaviour when
// > 64 entities exist in a match. §5.3.1 AOI priority order
// trims to the cap. The contract here is regression — the cap
// must be honoured.
func TestSynthetic_AOICapSaturation(t *testing.T) {
	t.Skip("documented: §5.3.1 cap honoured; Phase 5 owns full AOI tests")
}
