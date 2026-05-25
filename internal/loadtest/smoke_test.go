//go:build loadtest

package loadtest

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/observ"
)

// TestLoad_Smoke is the PR-gating smoke load (PHASE8 §8, DoD #3–#6):
// 4 matches × 4 clients (16 players) for 60s. Under -short it runs an
// abbreviated 5s pass for local iteration; CI runs the full 60s via
// `make test-load`.
func TestLoad_Smoke(t *testing.T) {
	dur := 60 * time.Second
	if testing.Short() {
		dur = 5 * time.Second
	}
	reg := observ.NewRegistry()
	rep := Run(t, Config{Matches: 4, ClientsEach: 4, Duration: dur, InputHz: 30}, reg)
	t.Logf("\n%s", rep)

	// #3: no client errors, no match aborts.
	if rep.ClientErrors != 0 {
		t.Errorf("client errors=%d (want 0)", rep.ClientErrors)
	}
	if rep.MatchAborts != 0 {
		t.Errorf("match aborts=%d (want 0)", rep.MatchAborts)
	}
	// #4: P99 server tick budget < 10ms, computed from the exact sampler.
	if rep.TickP99 <= 0 {
		t.Errorf("no tick samples recorded")
	} else if rep.TickP99 >= 10*time.Millisecond {
		t.Errorf("tick p99=%v (want < 10ms)", rep.TickP99)
	}
	// #5: no goroutine leak after the deterministic drain.
	if rep.GoroutineLeaked(2) {
		t.Errorf("goroutine leak: before=%d after=%d", rep.GoroutinesBefore, rep.GoroutinesAfter)
	}
	// #6: mean per-client bandwidth ≤ 12 KB/s in AND out.
	if rep.BytesInPerClientPerSec > 12 {
		t.Errorf("bandwidth in=%.2f KB/s (want ≤ 12)", rep.BytesInPerClientPerSec)
	}
	if rep.BytesOutPerClientPerSec > 12 {
		t.Errorf("bandwidth out=%.2f KB/s (want ≤ 12)", rep.BytesOutPerClientPerSec)
	}
}
