package observ

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content-type=%q", ct)
	}

	body := readAll(t, resp)
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

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
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
