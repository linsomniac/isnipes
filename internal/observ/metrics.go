package observ

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Registry is the process-wide metrics surface scraped by /metrics. Each
// series is DESIGNED to have a real producer wired by its owning package in
// later Phase 8 work (PHASE8 §6.2): the tick histogram + over-budget counter
// from the match actor (§6.4/§6.5), bytes from internal/net, the drop
// counter from the over-budget rule, and the active-matches / joined-players
// gauges from the registry / actor. This package provides the thread-safe
// sinks + exposition; the production wiring + admin-listener mount land with
// DoD #9/#10 / the load harness. All mutators are atomic; scrape reads an
// atomic snapshot.
type Registry struct {
	tickHist *Histogram

	ticksOverBudget atomic.Uint64
	bytesIn         atomic.Uint64
	bytesOut        atomic.Uint64
	snapshotDrops   atomic.Uint64

	activeMatches atomic.Int64
	joinedPlayers atomic.Int64
}

// NewRegistry constructs an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tickHist: NewLatencyHistogram()}
}

// TickHistogram returns the histogram the match actor observes into.
func (r *Registry) TickHistogram() *Histogram { return r.tickHist }

// IncTickOverBudget records a tick that exceeded the budget.
func (r *Registry) IncTickOverBudget() { r.ticksOverBudget.Add(1) }

// AddBytesIn/AddBytesOut accumulate connection traffic (internal/net).
func (r *Registry) AddBytesIn(n uint64)  { r.bytesIn.Add(n) }
func (r *Registry) AddBytesOut(n uint64) { r.bytesOut.Add(n) }

// IncSnapshotDrop records a snapshot suppressed by the over-budget rule.
func (r *Registry) IncSnapshotDrop() { r.snapshotDrops.Add(1) }

// AddActiveMatches / AddJoinedPlayers adjust gauges from their owning
// goroutine (the registry on create/remove; the actor on join/leave/drop).
func (r *Registry) AddActiveMatches(delta int) { r.activeMatches.Add(int64(delta)) }
func (r *Registry) AddJoinedPlayers(delta int) { r.joinedPlayers.Add(int64(delta)) }

// SetActiveMatches / SetJoinedPlayers set gauges absolutely.
func (r *Registry) SetActiveMatches(n int) { r.activeMatches.Store(int64(n)) }
func (r *Registry) SetJoinedPlayers(n int) { r.joinedPlayers.Store(int64(n)) }

// secondsLabel formats a duration as a Prometheus le bound in seconds.
func secondsLabel(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'g', -1, 64)
}

// WriteProm emits the metrics in Prometheus text-exposition format (v0.0.4):
// HELP/TYPE comments, then samples, each line \n-terminated.
func (r *Registry) WriteProm(w io.Writer) {
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	counts, total := r.tickHist.snapshot()

	// isnipes_tick_seconds histogram (cumulative buckets + sum + count).
	fmt.Fprintln(bw, "# HELP isnipes_tick_seconds Match simulation tick duration in seconds.")
	fmt.Fprintln(bw, "# TYPE isnipes_tick_seconds histogram")
	var cum uint64
	for i, b := range r.tickHist.bounds {
		cum += counts[i]
		fmt.Fprintf(bw, "isnipes_tick_seconds_bucket{le=\"%s\"} %d\n", secondsLabel(b), cum)
	}
	cum += counts[len(counts)-1] // +Inf bucket
	fmt.Fprintf(bw, "isnipes_tick_seconds_bucket{le=\"+Inf\"} %d\n", cum)
	fmt.Fprintf(bw, "isnipes_tick_seconds_sum %s\n", strconv.FormatFloat(r.tickHist.Sum().Seconds(), 'g', -1, 64))
	fmt.Fprintf(bw, "isnipes_tick_seconds_count %d\n", total)

	writeCounter(bw, "isnipes_ticks_total", "Total simulation ticks observed.", total)
	writeCounter(bw, "isnipes_ticks_over_budget_total", "Ticks that exceeded the 33ms budget.", r.ticksOverBudget.Load())
	writeCounter(bw, "isnipes_bytes_in_total", "Bytes received from clients (application WS-message bytes).", r.bytesIn.Load())
	writeCounter(bw, "isnipes_bytes_out_total", "Bytes sent to clients (application WS-message bytes).", r.bytesOut.Load())
	writeCounter(bw, "isnipes_snapshot_drops_total", "Snapshots suppressed by the over-budget catch-up rule.", r.snapshotDrops.Load())

	writeGauge(bw, "isnipes_active_matches", "Currently live matches.", r.activeMatches.Load())
	writeGauge(bw, "isnipes_joined_players", "Currently joined players across all matches.", r.joinedPlayers.Load())
}

func writeCounter(w io.Writer, name, help string, v uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
}

func writeGauge(w io.Writer, name, help string, v int64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
}

// Handler serves GET/HEAD /metrics in Prometheus text-exposition format. It
// is intended for the admin listener only (PHASE8 §6.3), never the public
// mux. Non-GET/HEAD methods get 405.
func (r *Registry) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if req.Method == http.MethodHead {
			return
		}
		r.WriteProm(w)
	}
}
