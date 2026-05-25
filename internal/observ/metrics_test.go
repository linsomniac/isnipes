package observ

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestObserv_MetricsEndpoint — /metrics returns 200 with the Prometheus
// content-type and parseable text exposition; every documented series is
// present and its producer is exercised. (DoD #8)
func TestObserv_MetricsEndpoint(t *testing.T) {
	r := NewRegistry()
	// Exercise every producer.
	r.TickHistogram().Observe(2 * time.Millisecond)
	r.TickHistogram().Observe(40 * time.Millisecond)
	r.IncTickOverBudget()
	r.AddBytesIn(1234)
	r.AddBytesOut(5678)
	r.IncSnapshotDrop()
	r.SetActiveMatches(3)
	r.SetJoinedPlayers(7)

	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content-type=%q", ct)
	}

	body := rec.Body.String()
	values := parseExposition(t, body)

	wantSeries := []string{
		"isnipes_tick_seconds_sum",
		"isnipes_tick_seconds_count",
		"isnipes_ticks_total",
		"isnipes_ticks_over_budget_total",
		"isnipes_bytes_in_total",
		"isnipes_bytes_out_total",
		"isnipes_snapshot_drops_total",
		"isnipes_active_matches",
		"isnipes_joined_players",
	}
	for _, s := range wantSeries {
		if _, ok := values[s]; !ok {
			t.Errorf("missing series %s\n--- body ---\n%s", s, body)
		}
	}
	// Spot-check producer values.
	assertVal(t, values, "isnipes_bytes_in_total", 1234)
	assertVal(t, values, "isnipes_bytes_out_total", 5678)
	assertVal(t, values, "isnipes_ticks_total", 2)
	assertVal(t, values, "isnipes_ticks_over_budget_total", 1)
	assertVal(t, values, "isnipes_snapshot_drops_total", 1)
	assertVal(t, values, "isnipes_active_matches", 3)
	assertVal(t, values, "isnipes_joined_players", 7)

	// The histogram must have a +Inf bucket equal to the count, and at least
	// one finite bucket line.
	if !strings.Contains(body, `isnipes_tick_seconds_bucket{le="+Inf"} 2`) {
		t.Errorf("missing/incorrect +Inf bucket\n%s", body)
	}
	if !strings.Contains(body, `isnipes_tick_seconds_bucket{le=`) {
		t.Errorf("missing finite buckets")
	}
}

// TestObserv_WritePromValidLines — every non-comment, non-empty line is
// "<name>[{labels}] <number>" so the exposition parses.
func TestObserv_WritePromValidLines(t *testing.T) {
	r := NewRegistry()
	r.TickHistogram().Observe(time.Millisecond)
	var sb strings.Builder
	r.WriteProm(&sb)
	for _, line := range strings.Split(strings.TrimRight(sb.String(), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.LastIndexByte(line, ' ')
		if idx < 0 {
			t.Fatalf("unparseable line: %q", line)
		}
		val := line[idx+1:]
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			t.Fatalf("non-numeric value in line %q: %v", line, err)
		}
	}
}

// TestObserv_MetricsMethodNotAllowed — non-GET/HEAD gets 405 with Allow.
func TestObserv_MetricsMethodNotAllowed(t *testing.T) {
	r := NewRegistry()
	rec := httptest.NewRecorder()
	r.Handler()(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rec.Code)
	}
	if a := rec.Header().Get("Allow"); a != "GET, HEAD" {
		t.Fatalf("Allow=%q", a)
	}
}

// TestObserv_HistogramCountEqualsInfBucket asserts the Prometheus invariant
// _count == +Inf bucket holds even while a writer races the scrape — both
// derive from the same bucket snapshot. (codex P1)
func TestObserv_HistogramCountEqualsInfBucket(t *testing.T) {
	r := NewRegistry()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				r.TickHistogram().Observe(time.Duration(time.Now().UnixNano()%5_000_000) * time.Nanosecond)
			}
		}
	}()
	for i := 0; i < 200; i++ {
		var sb strings.Builder
		r.WriteProm(&sb)
		vals := parseExposition(t, sb.String())
		inf := infBucket(t, sb.String())
		if vals["isnipes_tick_seconds_count"] != inf {
			t.Fatalf("_count=%v != +Inf bucket=%v", vals["isnipes_tick_seconds_count"], inf)
		}
		if vals["isnipes_ticks_total"] != inf {
			t.Fatalf("ticks_total=%v != +Inf bucket=%v", vals["isnipes_ticks_total"], inf)
		}
	}
	close(stop)
	wg.Wait()
}

func infBucket(t *testing.T, body string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, `isnipes_tick_seconds_bucket{le="+Inf"} `) {
			idx := strings.LastIndexByte(line, ' ')
			v, err := strconv.ParseFloat(line[idx+1:], 64)
			if err != nil {
				t.Fatalf("parse +Inf bucket: %v", err)
			}
			return v
		}
	}
	t.Fatalf("no +Inf bucket line in:\n%s", body)
	return 0
}

// parseExposition collects the last value seen for each bare series name
// (ignores bucket lines with label sets and comments).
func parseExposition(t *testing.T, body string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.LastIndexByte(line, ' ')
		if idx < 0 {
			continue
		}
		name := line[:idx]
		if strings.ContainsAny(name, "{") {
			continue // labelled (bucket) line
		}
		v, err := strconv.ParseFloat(line[idx+1:], 64)
		if err != nil {
			continue
		}
		out[name] = v
	}
	return out
}

func assertVal(t *testing.T, m map[string]float64, name string, want float64) {
	t.Helper()
	got, ok := m[name]
	if !ok {
		t.Errorf("series %s absent", name)
		return
	}
	if got != want {
		t.Errorf("%s=%v want %v", name, got, want)
	}
}
