package match

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrTooManyMatches is surfaced when Create would exceed the cap.
var ErrTooManyMatches = errors.New("match: too many concurrent matches")

// ErrRegistryClosed is returned by Create after StopAll has begun, so a
// match cannot be created into a registry that is shutting down (which would
// otherwise make StopAll wait until its context deadline).
var ErrRegistryClosed = errors.New("match: registry is shutting down")

// RegistryConfig governs concurrency and lifecycle.
type RegistryConfig struct {
	MaxConcurrentMatches int // default 64 (§5.3 of SPEC)

	// Phase 8 §6.4/§6.5 observability hooks, threaded into every Match's
	// MatchConfig at Create time. All nil-safe.
	TickSampler      TickObserver
	OnTickOverBudget func()
	OnSnapshotDrop   func()
}

// Registry is the lookup table of live matches. It is safe for
// concurrent use by lobby + net layers.
type Registry struct {
	cfg      RegistryConfig
	mu       sync.RWMutex
	matches  map[string]*Match
	stopping bool // set by StopAll; Create is rejected once true
}

// NewRegistry constructs a Registry. nil config gets defaults.
func NewRegistry(cfg RegistryConfig) *Registry {
	if cfg.MaxConcurrentMatches == 0 {
		cfg.MaxConcurrentMatches = 64
	}
	return &Registry{cfg: cfg, matches: make(map[string]*Match)}
}

// Create adds a new match to the registry and starts its Run loop
// in a goroutine. Returns ErrTooManyMatches if the cap is reached.
func (r *Registry) Create(mc MatchConfig) (*Match, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopping {
		return nil, ErrRegistryClosed
	}
	if len(r.matches) >= r.cfg.MaxConcurrentMatches {
		return nil, ErrTooManyMatches
	}
	if _, dup := r.matches[mc.MatchID]; dup {
		return nil, errors.New("match: duplicate MatchID")
	}
	// Thread the observability hooks from the registry into the match.
	mc.TickSampler = r.cfg.TickSampler
	mc.OnTickOverBudget = r.cfg.OnTickOverBudget
	mc.OnSnapshotDrop = r.cfg.OnSnapshotDrop
	m, err := NewMatch(mc)
	if err != nil {
		return nil, err
	}
	r.matches[mc.MatchID] = m
	go func() {
		m.Run()
		r.RemoveEnded(mc.MatchID)
	}()
	return m, nil
}

// Lookup returns the live match for an ID, or false.
func (r *Registry) Lookup(id string) (*Match, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.matches[id]
	return m, ok
}

// RemoveEnded deletes the entry once Run() has returned.
func (r *Registry) RemoveEnded(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.matches, id)
}

// Len reports the number of live matches.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.matches)
}

// StopAll signals every live match to end cleanly (ctlShutdown → graceful
// close, NOT SERVER_ERROR, so it is not counted as a MatchAbort) and waits
// until the registry drains or ctx is done. Used by graceful process
// shutdown and the load-harness teardown so a goroutine-leak check samples
// after a deterministic drain (Registry.Close alone does not stop matches).
// PHASE8 §7.2.
func (r *Registry) StopAll(ctx context.Context) error {
	// Mark stopping under the lock and snapshot the live matches in one
	// critical section so a concurrent Create either lost the race (and is
	// included) or is rejected with ErrRegistryClosed — StopAll can never
	// be outrun by new matches.
	r.mu.Lock()
	r.stopping = true
	matches := make([]*Match, 0, len(r.matches))
	for _, m := range r.matches {
		matches = append(matches, m)
	}
	r.mu.Unlock()

	// Submit shutdown context-awarely: a stalled/full inbox must not block
	// past the deadline.
	for _, m := range matches {
		select {
		case m.in <- ctlShutdown{}:
		case <-m.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Wait for each Run goroutine to exit, then for RemoveEnded to drain
	// the map (RemoveEnded runs just after Run returns / done closes).
	for _, m := range matches {
		select {
		case <-m.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for r.Len() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

// Close shuts down the registry. It does not signal individual
// matches to stop (that would require a richer control protocol);
// it just waits up to ctx.Done() for them to drain via natural
// MatchOver. Phase 2 keeps this best-effort.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	count := len(r.matches)
	r.mu.Unlock()
	if count == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
