package loadtest

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLoad_BaselineRoundTrip — missing file → exists=false; save/load
// round-trips; SaveBaseline creates the parent dir.
func TestLoad_BaselineRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "perf_baseline.json")

	if _, exists, err := LoadBaseline(p); err != nil || exists {
		t.Fatalf("missing baseline: exists=%v err=%v (want false,nil)", exists, err)
	}
	want := Baseline{TickP99Us: 1000, BytesInKBps: 2, BytesOutKBps: 1, Host: "h"}
	if err := SaveBaseline(p, want); err != nil {
		t.Fatalf("SaveBaseline: %v", err)
	}
	got, exists, err := LoadBaseline(p)
	if err != nil || !exists {
		t.Fatalf("LoadBaseline after save: exists=%v err=%v", exists, err)
	}
	if got.TickP99Us != want.TickP99Us || got.BytesInKBps != want.BytesInKBps {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, want)
	}
}

// TestLoad_CheckRegression — tick regresses beyond tolerance; bandwidth
// regresses beyond the absolute target; empty baseline skips the tick check.
func TestLoad_CheckRegression(t *testing.T) {
	base := Baseline{TickP99Us: 1000, BytesInKBps: 8, BytesOutKBps: 8}

	ok := Report{TickP99: 1100 * time.Microsecond, BytesInPerClientPerSec: 5, BytesOutPerClientPerSec: 5}
	if regs := CheckRegression(ok, base, 0.20, 8.0); len(regs) != 0 {
		t.Fatalf("within tolerance flagged: %v", regs)
	}
	tickBad := Report{TickP99: 1300 * time.Microsecond, BytesInPerClientPerSec: 1, BytesOutPerClientPerSec: 1}
	if regs := CheckRegression(tickBad, base, 0.20, 8.0); len(regs) == 0 {
		t.Fatal("tick p99 1300µs > 1000×1.2 should regress")
	}
	bwBad := Report{TickP99: 900 * time.Microsecond, BytesInPerClientPerSec: 9, BytesOutPerClientPerSec: 1}
	if regs := CheckRegression(bwBad, base, 0.20, 8.0); len(regs) == 0 {
		t.Fatal("bandwidth in 9 > target 8 should regress")
	}
	// Empty baseline (first run): no tick comparison; healthy bandwidth → clean.
	if regs := CheckRegression(Report{TickP99: 5 * time.Second, BytesInPerClientPerSec: 1, BytesOutPerClientPerSec: 1}, Baseline{}, 0.20, 8.0); len(regs) != 0 {
		t.Fatalf("empty baseline should skip tick check: %v", regs)
	}
}
