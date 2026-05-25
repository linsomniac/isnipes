//go:build ignore

// Command loadtest is the isnipes load-harness CLI (PHASE8 §7.5, §9). It is
// excluded from `go build ./...` / `go test ./...` by the ignore tag; run it
// directly:
//
//	go run scripts/loadtest.go                 # 4×4 × 60s smoke-shape
//	go run scripts/loadtest.go --nightly       # 64×8 × 60s, regression vs baseline
//	go run scripts/loadtest.go --soak          # 64×8 × 24h, goroutine/RSS soak
//	go run scripts/loadtest.go --update-baseline
//
// Exits non-zero on a clean-run failure (client errors / match aborts) or a
// flagged nightly/soak regression, so a scheduled CI job goes red.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jafo/isnipes/internal/loadtest"
)

func main() {
	var (
		matches        = flag.Int("matches", 4, "number of concurrent matches (M)")
		clients        = flag.Int("clients", 4, "clients per match (N)")
		duration       = flag.Duration("duration", 0, "run length (default 60s; 24h with --soak)")
		inputHz        = flag.Int("input-hz", 30, "client input rate")
		nightly        = flag.Bool("nightly", false, "nightly profile: 64×8, regression vs baseline")
		soak           = flag.Bool("soak", false, "soak: long run, goroutine/RSS regression")
		rotateEvery    = flag.Duration("rotate-every", 30*time.Second, "soak sampling interval")
		baselinePath   = flag.String("baseline", "testdata/perf_baseline.json", "nightly baseline file")
		updateBaseline = flag.Bool("update-baseline", false, "(re)seed the baseline from this run and exit")
	)
	flag.Parse()

	cfg := loadtest.Config{
		Matches:     *matches,
		ClientsEach: *clients,
		Duration:    *duration,
		InputHz:     *inputHz,
		RotateEvery: *rotateEvery,
	}
	if *nightly || *soak {
		cfg.Matches = 64
		cfg.ClientsEach = 8
	}
	if *soak {
		cfg.Soak = true
	}
	if cfg.Duration <= 0 {
		if *soak {
			cfg.Duration = 24 * time.Hour
		} else {
			cfg.Duration = 60 * time.Second
		}
	}

	rep, clean := loadtest.RunStandalone(cfg)
	fmt.Print(rep.String())

	exit := 0
	if !clean {
		fmt.Printf("FAIL: client-errors=%d match-aborts=%d\n", rep.ClientErrors, rep.MatchAborts)
		exit = 1
	}

	// Never record a baseline from a dirty run — it would establish a
	// reference from an invalid measurement.
	if *updateBaseline {
		if !clean {
			fmt.Println("refusing to write baseline from a non-clean run")
			os.Exit(1)
		}
		if err := loadtest.SaveBaseline(*baselinePath, loadtest.BaselineFromReport(rep)); err != nil {
			fmt.Println("baseline write error:", err)
			os.Exit(1)
		}
		fmt.Println("wrote baseline", *baselinePath)
		os.Exit(exit)
	}

	if *nightly {
		base, exists, err := loadtest.LoadBaseline(*baselinePath)
		if err != nil {
			fmt.Println("baseline read error:", err)
			os.Exit(1)
		}
		switch {
		case !exists && clean:
			if err := loadtest.SaveBaseline(*baselinePath, loadtest.BaselineFromReport(rep)); err != nil {
				fmt.Println("baseline seed error:", err)
				os.Exit(1)
			}
			fmt.Println("seeded baseline", *baselinePath, "(first run)")
		case !exists:
			fmt.Println("no baseline and run was not clean — not seeding")
		default:
			for _, r := range loadtest.CheckRegression(rep, base, 0.20, 8.0) {
				fmt.Println("REGRESSION:", r)
				exit = 1
			}
		}
	}

	if *soak {
		// Trend during the run catches goroutines that accumulate while
		// active and are freed on cancellation (the post-drain check alone
		// would miss that).
		if rep.GoroutineGrowth > 2 {
			fmt.Printf("REGRESSION: goroutines trended up during soak (+%d; peak %d)\n", rep.GoroutineGrowth, rep.GoroutineMax)
			exit = 1
		}
		if rep.GoroutineLeaked(2) {
			fmt.Printf("REGRESSION: goroutines %d -> %d after drain\n", rep.GoroutinesBefore, rep.GoroutinesAfter)
			exit = 1
		}
		if rep.RSSGrewMoreThan(0.05) {
			fmt.Printf("REGRESSION: RSS grew >5%% (%dKiB -> %dKiB)\n", rep.RSSStartBytes/1024, rep.RSSEndBytes/1024)
			exit = 1
		}
	}

	os.Exit(exit)
}
