package loadtest

import (
	"context"
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/observ"
	"github.com/jafo/isnipes/internal/sim"
)

// soakWarmupDuration is the leading slice of a soak run excluded from the RSS
// leak trend, giving the working set (512 connections, sims, snapshot buffers)
// time to reach steady state before we start watching for unbounded growth.
const soakWarmupDuration = 2 * time.Minute

// soakSamplerCap bounds the harness's per-observation tick/latency samplers in
// soak mode. The exact (unbounded) RecordingSampler is right for the short
// nightly/smoke gate, but over a long soak it retains O(duration) samples
// (~9.6k/s at 64×8 = 1.9k tick + 7.7k latency), so the measuring instrument
// outgrows the server it measures and the RSS leak gate fires on the harness's
// own growth — a false positive. The cap is reached well inside
// soakWarmupDuration (tick at ~1.9k/s fills 100k in <1m), so the post-warmup
// steady-state trend the gate watches sees a constant sampler footprint, and
// the ring then overwrites in place (no allocation, quiescing GC churn). Soak
// gates only on goroutines and RSS, never on these quantiles, so the trailing
// window the cap leaves in the printed report is fine.
// AIDEV-NOTE: do NOT remove this bound — see git history (c04aa3b) and
// client.go's sendAt bound; an unbounded harness sampler reintroduces the
// soak-RSS false positive this was added to kill.
const soakSamplerCap = 100_000

// fanOut sends each tick observation to every observer (the exact
// RecordingSampler for the gate AND the registry histogram for /metrics).
type fanOut []match.TickObserver

func (f fanOut) Observe(d time.Duration) {
	for _, o := range f {
		o.Observe(d)
	}
}

// Run boots an in-process server, drives the load, and returns the Report.
// The caller supplies the observ.Registry so its histogram/counters are
// populated alongside the exact gate samples (PHASE8 §7.2). Fatals via tb
// on setup error.
func Run(tb testing.TB, cfg Config, reg *observ.Registry) Report {
	tb.Helper()
	rep, err := runLoad(cfg, reg)
	if err != nil {
		tb.Fatalf("loadtest: %v", err)
	}
	return rep
}

// RunStandalone is the CLI entry: boots its own registry, runs, and reports
// whether the run was clean (no client errors, no match aborts).
func RunStandalone(cfg Config) (Report, bool) {
	rep, err := runLoad(cfg, observ.NewRegistry())
	if err != nil {
		fmt.Println("loadtest error:", err)
		return rep, false
	}
	return rep, rep.ClientErrors == 0 && rep.MatchAborts == 0
}

func runLoad(cfg Config, reg *observ.Registry) (Report, error) {
	cfg = cfg.withDefaults()
	if reg == nil {
		reg = observ.NewRegistry()
	}

	// Baseline before allocating any goroutines so the leak check measures
	// the whole harness's residue after teardown.
	runtime.GC()
	before := runtime.NumGoroutine()
	rssStart := rssBytes()

	rec := &observ.RecordingSampler{} // exact tick P99 for the gate
	if cfg.Soak {
		rec.SetCap(soakSamplerCap) // bound before the registry starts ticking
	}
	var ticksOver, drops atomic.Uint64
	matchReg := match.NewRegistry(match.RegistryConfig{
		MaxConcurrentMatches: cfg.Matches + 4,
		TickSampler:          fanOut{rec, reg.TickHistogram()},
		OnTickOverBudget:     func() { ticksOver.Add(1); reg.IncTickOverBudget() },
		OnSnapshotDrop:       func() { drops.Add(1); reg.IncSnapshotDrop() },
		OnJoinedDelta:        reg.AddJoinedPlayers,
		OnActiveMatchesDelta: reg.AddActiveMatches,
	})
	lob := lobby.NewLobby(lobby.Config{Registry: matchReg})
	lobDone := make(chan struct{})
	go func() { lob.Run(); close(lobDone) }()
	srv := wsnet.NewServer(wsnet.ServerConfig{Lobby: lob, MatchRegistry: matchReg, ServerVersion: "loadtest"})
	ts := httptest.NewServer(srv.Handler())

	teardown := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = matchReg.StopAll(stopCtx)
		ts.Close()
		lob.Stop()
		<-lobDone
	}

	// Pre-create M matches with N token slots each (PvP: bots move, never
	// fire, so no deaths shrink the load before the run completes).
	type matchInfo struct {
		id     string
		tokens []string
	}
	matches := make([]matchInfo, 0, cfg.Matches)
	for mi := 0; mi < cfg.Matches; mi++ {
		id := fmt.Sprintf("load-%d", mi)
		slots := make([]match.PendingJoin, cfg.ClientsEach)
		tokens := make([]string, cfg.ClientsEach)
		for pi := 0; pi < cfg.ClientsEach; pi++ {
			tok := fmt.Sprintf("%s-p%d", id, pi)
			tokens[pi] = tok
			slots[pi] = match.PendingJoin{
				MatchID: id, Token: tok, PlayerID: sim.EntityID(pi + 1),
				Nick: fmt.Sprintf("p%d", pi), IssuedAt: time.Now(),
			}
		}
		if _, err := matchReg.Create(match.MatchConfig{
			MatchID: id, MapSeed: 0xC0FFEE + uint32(mi),
			MapWidth: 60, MapHeight: 40, PlayerSlots: slots,
		}); err != nil {
			teardown()
			return Report{}, fmt.Errorf("create match %s: %w", id, err)
		}
		matches = append(matches, matchInfo{id: id, tokens: tokens})
	}

	stats := &clientStats{}
	if cfg.Soak {
		stats.lat.SetCap(soakSamplerCap) // bound before any client connects
	}
	wsBase := strings.Replace(ts.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	// Soak mode: periodically sample goroutines + RSS for the whole run so
	// an operator sees the trend live and we capture the peak goroutine
	// count. (Full 24h soak is operator-run; the leak/RSS pass-fail uses the
	// post-drain figures below.)
	goroutineMax := 0          // stays 0 on a non-soak run (matches Report doc + its sibling "0 otherwise" fields)
	var goroutineSamples []int // sampler-goroutine only; read after join (no race)
	var rssSamples []uint64    // ditto; drives the steady-state RSS leak trend
	sampleStop := make(chan struct{})
	var sampleDone sync.WaitGroup
	if cfg.Soak {
		goroutineMax = before // seed the peak with the pre-load baseline for soak runs
		sampleDone.Add(1)
		go func() {
			defer sampleDone.Done()
			t := time.NewTicker(cfg.RotateEvery)
			defer t.Stop()
			start := time.Now()
			for {
				select {
				case <-sampleStop:
					return
				case <-t.C:
					g := runtime.NumGoroutine()
					rss := rssBytes()
					goroutineSamples = append(goroutineSamples, g)
					rssSamples = append(rssSamples, rss)
					if g > goroutineMax {
						goroutineMax = g
					}
					fmt.Printf("soak t=%-6s goroutines=%d rss=%dKiB\n",
						time.Since(start).Round(time.Second), g, rss/1024)
				}
			}
		}()
	}

	var wg sync.WaitGroup
	for _, mInfo := range matches {
		url := wsBase + "/ws/match/" + mInfo.id
		for _, tok := range mInfo.tokens {
			wg.Add(1)
			go func(url, token string) {
				defer wg.Done()
				runClient(ctx, url, token, cfg.InputHz, stats)
			}(url, tok)
		}
	}
	wg.Wait() // all clients return when the run context (Duration) expires
	close(sampleStop)
	sampleDone.Wait()

	// Goroutine TREND across the soak: last steady-state sample minus the
	// first. A non-leaking sustained load plateaus (≈0); an upward trend is
	// a leak even if the goroutines are released on cancellation (which the
	// post-drain Before/After check would miss).
	goroutineGrowth := 0
	if n := len(goroutineSamples); n >= 2 {
		goroutineGrowth = goroutineSamples[n-1] - goroutineSamples[0]
	}

	// RSS steady-state trend: skip a warmup window (the working set ramps as
	// the connections, sims and snapshot buffers populate), then take the first
	// post-warmup sample vs the last. A healthy server plateaus; a real leak
	// keeps climbing. This is the leak signal — NOT rssStart→rssEnd, which
	// compares the bare pre-load process to a fully-loaded one and so always
	// shows a huge, meaningless increase.
	var rssSteadyStart, rssSteadyEnd uint64
	if cfg.RotateEvery > 0 {
		warmup := int(soakWarmupDuration / cfg.RotateEvery)
		if len(rssSamples)-warmup >= 2 {
			rssSteadyStart = rssSamples[warmup]
			rssSteadyEnd = rssSamples[len(rssSamples)-1]
		}
	}

	teardown()

	// Goroutine quiescence after deterministic drain.
	var after int
	for i := 0; i < 200; i++ {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before+2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	rssEnd := rssBytes()

	secs := cfg.Duration.Seconds()
	nClients := float64(cfg.Matches * cfg.ClientsEach)
	kbps := func(total uint64) float64 {
		if secs <= 0 || nClients <= 0 {
			return 0
		}
		return float64(total) / nClients / secs / 1024
	}
	return Report{
		Matches:                 cfg.Matches,
		Clients:                 cfg.ClientsEach,
		Duration:                cfg.Duration,
		LatencyP50:              stats.lat.Quantile(0.5),
		LatencyP99:              stats.lat.Quantile(0.99),
		TickP50:                 rec.Quantile(0.5),
		TickP99:                 rec.Quantile(0.99),
		TicksOverBudget:         ticksOver.Load(),
		SnapshotDrops:           drops.Load(),
		BytesInPerClientPerSec:  kbps(stats.bytesIn.Load()),
		BytesOutPerClientPerSec: kbps(stats.bytesOut.Load()),
		GoroutinesBefore:        before,
		GoroutinesAfter:         after,
		GoroutineMax:            goroutineMax,
		GoroutineGrowth:         goroutineGrowth,
		RSSStartBytes:           rssStart,
		RSSEndBytes:             rssEnd,
		RSSSteadyStartBytes:     rssSteadyStart,
		RSSSteadyEndBytes:       rssSteadyEnd,
		ClientErrors:            int(stats.errors.Load()),
		MatchAborts:             int(stats.matchAborts.Load()),
	}, nil
}
