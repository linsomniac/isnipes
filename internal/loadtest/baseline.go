package loadtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Baseline is the committed nightly perf reference (PHASE8 §9). Tick budget
// is compared with a tolerance; bandwidth is compared to an absolute target.
type Baseline struct {
	TickP99Us    float64 `json:"tick_p99_us"`
	BytesInKBps  float64 `json:"bytes_in_kbps"`
	BytesOutKBps float64 `json:"bytes_out_kbps"`
	RecordedAt   string  `json:"recorded_at"`
	Host         string  `json:"host"`
}

// BaselineFromReport snapshots a run as a new baseline.
func BaselineFromReport(r Report) Baseline {
	host, _ := os.Hostname()
	return Baseline{
		TickP99Us:    float64(r.TickP99.Microseconds()),
		BytesInKBps:  r.BytesInPerClientPerSec,
		BytesOutKBps: r.BytesOutPerClientPerSec,
		RecordedAt:   time.Now().UTC().Format(time.RFC3339),
		Host:         host,
	}
}

// LoadBaseline reads a baseline file. The bool is false (no error) when the
// file does not exist yet — the first nightly run seeds it.
func LoadBaseline(path string) (Baseline, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Baseline{}, false, nil
	}
	if err != nil {
		return Baseline{}, false, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, false, err
	}
	return b, true, nil
}

// SaveBaseline writes a baseline file (pretty JSON, trailing newline),
// creating the parent directory if needed.
func SaveBaseline(path string, b Baseline) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// CheckRegression returns human-readable regression messages (empty = OK).
// Tick P99 regresses if it exceeds baseline × (1+tickTol); bandwidth
// regresses if it exceeds the absolute target (post-AOI, §9). A zero/empty
// baseline tick skips the tick comparison (first-run seeding).
func CheckRegression(r Report, base Baseline, tickTol, bwTargetKBps float64) []string {
	var regs []string
	if base.TickP99Us > 0 {
		budget := base.TickP99Us * (1 + tickTol)
		got := float64(r.TickP99.Microseconds())
		if got > budget {
			regs = append(regs, fmt.Sprintf("tick p99 %.0fµs > baseline %.0fµs +%.0f%% (%.0fµs)",
				got, base.TickP99Us, tickTol*100, budget))
		}
	}
	if r.BytesInPerClientPerSec > bwTargetKBps {
		regs = append(regs, fmt.Sprintf("bandwidth in %.2f KB/s > target %.2f KB/s",
			r.BytesInPerClientPerSec, bwTargetKBps))
	}
	if r.BytesOutPerClientPerSec > bwTargetKBps {
		regs = append(regs, fmt.Sprintf("bandwidth out %.2f KB/s > target %.2f KB/s",
			r.BytesOutPerClientPerSec, bwTargetKBps))
	}
	return regs
}
