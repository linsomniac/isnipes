package loadtest

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/observ"
)

// TestLoad_DriverSmall is the fast, default-tagged harness sanity check so
// the load path stays compiled and honest in `go test ./...` without the
// 60s gate. (PHASE8 §16.4)
func TestLoad_DriverSmall(t *testing.T) {
	reg := observ.NewRegistry()
	rep := Run(t, Config{Matches: 2, ClientsEach: 2, Duration: 2 * time.Second, InputHz: 30}, reg)
	t.Logf("\n%s", rep)

	if rep.ClientErrors != 0 {
		t.Fatalf("client errors=%d", rep.ClientErrors)
	}
	if rep.MatchAborts != 0 {
		t.Fatalf("match aborts=%d", rep.MatchAborts)
	}
	if rep.TickP99 <= 0 {
		t.Fatal("no tick samples recorded (match never went live?)")
	}
	if rep.LatencyP99 <= 0 {
		t.Fatal("no latency samples (no snapshots echoed an input?)")
	}
	if rep.BytesInPerClientPerSec <= 0 || rep.BytesOutPerClientPerSec <= 0 {
		t.Fatalf("bytes not counted: in=%.3f out=%.3f", rep.BytesInPerClientPerSec, rep.BytesOutPerClientPerSec)
	}
	if rep.GoroutineLeaked(2) {
		t.Fatalf("goroutine leak: before=%d after=%d", rep.GoroutinesBefore, rep.GoroutinesAfter)
	}
}

// TestLoad_ReportHelpers unit-tests the Report predicates.
func TestLoad_ReportHelpers(t *testing.T) {
	leak := Report{GoroutinesBefore: 10, GoroutinesAfter: 13}
	if !leak.GoroutineLeaked(2) {
		t.Fatal("13 > 10+2 should report a leak")
	}
	if leak.GoroutineLeaked(5) {
		t.Fatal("13 <= 10+5 should not report a leak")
	}
	rss := Report{RSSStartBytes: 1000, RSSEndBytes: 1100}
	if !rss.RSSGrewMoreThan(0.05) {
		t.Fatal("10% growth exceeds 5%")
	}
	if rss.RSSGrewMoreThan(0.20) {
		t.Fatal("10% growth is under 20%")
	}
	if (Report{}).RSSGrewMoreThan(0.05) {
		t.Fatal("zero start RSS must not report growth")
	}
}
