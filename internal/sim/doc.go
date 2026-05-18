// Package sim is the deterministic Phase 1 simulation core for isnipes.
//
// See PHASE1.md and SPEC.md at the repo root. The package is a pure,
// headless library with no I/O, no transport, no AI, and no lobby; it
// generates a maze deterministically from a seed, ticks player and
// projectile physics at a fixed 30 Hz cadence, and exposes a small
// public API (see sim.go) that internal/match drives in Phase 2.
package sim
