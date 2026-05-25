package loadtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

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
	// joined_players is a real producer wired through the actor: every join
	// (+1) must be balanced by drop/end (-N), so after teardown it is 0.
	if jp := reg.JoinedPlayers(); jp != 0 {
		t.Fatalf("joined_players gauge=%d after teardown, want 0 (unbalanced join/leave)", jp)
	}
	if am := reg.ActiveMatches(); am != 0 {
		t.Fatalf("active_matches gauge=%d after teardown, want 0", am)
	}
}

// TestLoad_ClientCountsMidRunDrop — a client whose socket the server drops
// mid-run (before the duration elapses) must record an error, so the gate
// can't pass after clients silently disconnected. (codex)
func TestLoad_ClientCountsMidRunDrop(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/match/", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		// Consume the MatchJoin, then drop abruptly mid-run.
		_, _, _ = c.Read(r.Context())
		c.Close(websocket.StatusInternalError, "boom")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	stats := &clientStats{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runClient(ctx, strings.Replace(ts.URL, "http://", "ws://", 1)+"/ws/match/x", "tok", 30, stats)

	if stats.errors.Load() == 0 {
		t.Fatal("expected a client error when the server drops the socket mid-run")
	}
}

// TestLoad_SoakSmoke exercises the soak sampling path at tiny scale (the
// full 24h soak is operator-run). It must complete with no leak and a
// populated peak-goroutine figure.
func TestLoad_SoakSmoke(t *testing.T) {
	reg := observ.NewRegistry()
	rep := Run(t, Config{
		Matches: 2, ClientsEach: 2, Duration: 2 * time.Second,
		InputHz: 30, Soak: true, RotateEvery: 500 * time.Millisecond,
	}, reg)
	t.Logf("\n%s", rep)
	if rep.ClientErrors != 0 || rep.MatchAborts != 0 {
		t.Fatalf("soak smoke not clean: errors=%d aborts=%d", rep.ClientErrors, rep.MatchAborts)
	}
	if rep.GoroutineMax < rep.GoroutinesBefore {
		t.Fatalf("soak peak goroutines=%d < before=%d (sampler didn't run?)", rep.GoroutineMax, rep.GoroutinesBefore)
	}
	if rep.GoroutineLeaked(2) {
		t.Fatalf("soak smoke goroutine leak: before=%d after=%d", rep.GoroutinesBefore, rep.GoroutinesAfter)
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
