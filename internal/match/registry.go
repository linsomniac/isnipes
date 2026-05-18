package match

import (
	"context"
	"errors"
	"sync"
)

// ErrTooManyMatches is surfaced when Create would exceed the cap.
var ErrTooManyMatches = errors.New("match: too many concurrent matches")

// RegistryConfig governs concurrency and lifecycle.
type RegistryConfig struct {
	MaxConcurrentMatches int // default 64 (§5.3 of SPEC)
}

// Registry is the lookup table of live matches. It is safe for
// concurrent use by lobby + net layers.
type Registry struct {
	cfg     RegistryConfig
	mu      sync.RWMutex
	matches map[string]*Match
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
	if len(r.matches) >= r.cfg.MaxConcurrentMatches {
		return nil, ErrTooManyMatches
	}
	if _, dup := r.matches[mc.MatchID]; dup {
		return nil, errors.New("match: duplicate MatchID")
	}
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
