// Package loadtest is the in-process synthetic-client load harness
// (PHASE8 §7). It drives the real lobby→match wire protocol against an
// httptest server and measures end-to-end latency, server tick budget,
// per-client bandwidth, goroutines, and RSS.
package loadtest

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// rssBytes returns the process resident set size. On Linux it reads
// /proc/self/statm (resident pages × page size); elsewhere it falls back to
// runtime.MemStats.Sys (a documented approximation — the soak RSS check
// runs on the Linux deploy target). No cgo.
func rssBytes() uint64 {
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}
