package loadtest

import (
	"fmt"
	"strings"
	"time"
)

// Config governs a load run.
type Config struct {
	Matches     int           // M
	ClientsEach int           // N per match (2..MaxPlayersPerMatch)
	Duration    time.Duration // run length
	InputHz     int           // default 30
	Soak        bool          // rotate clients for the whole Duration
	RotateEvery time.Duration // soak client churn interval
}

func (c Config) withDefaults() Config {
	if c.Matches <= 0 {
		c.Matches = 4
	}
	if c.ClientsEach <= 0 {
		c.ClientsEach = 4
	}
	if c.Duration <= 0 {
		c.Duration = 60 * time.Second
	}
	if c.InputHz <= 0 {
		c.InputHz = 30
	}
	if c.RotateEvery <= 0 {
		c.RotateEvery = 30 * time.Second
	}
	return c
}

// Report is the outcome of a load run.
type Report struct {
	Matches, Clients        int
	Duration                time.Duration
	LatencyP50, LatencyP99  time.Duration // input.clientTick → echoing snapshot
	TickP50, TickP99        time.Duration // EXACT, from the RecordingSampler
	TicksOverBudget         uint64
	SnapshotDrops           uint64
	BytesInPerClientPerSec  float64 // KB/s (application WS-message bytes)
	BytesOutPerClientPerSec float64 // KB/s
	GoroutinesBefore        int
	GoroutinesAfter         int
	GoroutineMax            int // peak observed during a soak run (0 otherwise)
	GoroutineGrowth         int // soak: last steady sample − first (trend; 0 otherwise)
	RSSStartBytes           uint64
	RSSEndBytes             uint64
	RSSSteadyStartBytes     uint64 // first post-warmup soak sample (0 if not a soak / too short)
	RSSSteadyEndBytes       uint64 // last soak sample
	ClientErrors            int
	MatchAborts             int
}

// GoroutineLeaked reports whether goroutines grew beyond before+slack.
func (r Report) GoroutineLeaked(slack int) bool {
	return r.GoroutinesAfter > r.GoroutinesBefore+slack
}

// RSSGrewMoreThan reports whether RSS grew by more than frac (e.g. 0.05).
func (r Report) RSSGrewMoreThan(frac float64) bool {
	if r.RSSStartBytes == 0 {
		return false
	}
	return float64(r.RSSEndBytes) > float64(r.RSSStartBytes)*(1+frac)
}

// RSSSteadyGrowthFrac is the fractional RSS change across the post-warmup soak
// window — the leak signal. Zero when the run was too short to sample past the
// warmup, in which case the trend is indeterminate (not "no growth").
func (r Report) RSSSteadyGrowthFrac() float64 {
	if r.RSSSteadyStartBytes == 0 {
		return 0
	}
	return float64(r.RSSSteadyEndBytes)/float64(r.RSSSteadyStartBytes) - 1
}

// String renders a fixed-width human-readable table.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "load report: %d matches × %d clients (%d players) for %s\n",
		r.Matches, r.Clients, r.Matches*r.Clients, r.Duration.Round(time.Millisecond))
	fmt.Fprintf(&b, "  latency  p50=%-8s p99=%s\n", r.LatencyP50.Round(time.Microsecond), r.LatencyP99.Round(time.Microsecond))
	fmt.Fprintf(&b, "  tick     p50=%-8s p99=%s  over-budget=%d  snap-drops=%d\n",
		r.TickP50.Round(time.Microsecond), r.TickP99.Round(time.Microsecond), r.TicksOverBudget, r.SnapshotDrops)
	fmt.Fprintf(&b, "  bandwidth/client in=%.2f KB/s out=%.2f KB/s\n", r.BytesInPerClientPerSec, r.BytesOutPerClientPerSec)
	fmt.Fprintf(&b, "  goroutines before=%d after=%d\n", r.GoroutinesBefore, r.GoroutinesAfter)
	fmt.Fprintf(&b, "  rss idle=%dKiB peak≈%dKiB (working set vs bare process; not a leak signal)\n",
		r.RSSStartBytes/1024, r.RSSEndBytes/1024)
	if r.RSSSteadyStartBytes != 0 {
		fmt.Fprintf(&b, "  rss steady-trend %dKiB -> %dKiB (%.1f%%)\n",
			r.RSSSteadyStartBytes/1024, r.RSSSteadyEndBytes/1024, 100*r.RSSSteadyGrowthFrac())
	}
	fmt.Fprintf(&b, "  client-errors=%d match-aborts=%d\n", r.ClientErrors, r.MatchAborts)
	return b.String()
}
