package match

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/sim"
)

// TestRegistry_OnMatchEnded — the hook fires exactly once per match, with the
// match's ID, when its Run loop returns (here via StopAll). The hook runs on
// the match Run goroutine, so IDs are collected through a buffered channel.
// Mirrors TestRegistry_ActiveMatchesGauge.
func TestRegistry_OnMatchEnded(t *testing.T) {
	const n = 3
	ended := make(chan string, n)
	reg := NewRegistry(RegistryConfig{
		MaxConcurrentMatches: 16,
		OnMatchEnded:         func(id string) { ended <- id },
	})
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("end-%d", i)
		want[id] = true
		if _, err := reg.Create(MatchConfig{
			MatchID:     id,
			PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: fmt.Sprintf("t-%d", i)}},
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	got := map[string]bool{}
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for i := 0; i < n; i++ {
		select {
		case id := <-ended:
			if got[id] {
				t.Fatalf("OnMatchEnded fired twice for %s", id)
			}
			got[id] = true
		case <-timeout.C:
			t.Fatalf("OnMatchEnded fired only %d/%d times", i, n)
		}
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("OnMatchEnded never fired for %s", id)
		}
	}
}

// TestRegistry_OnMatchEnded_NilSafe — a registry without the hook still drains
// cleanly (no panic on the nil callback).
func TestRegistry_OnMatchEnded_NilSafe(t *testing.T) {
	reg := NewRegistry(RegistryConfig{MaxConcurrentMatches: 4})
	if _, err := reg.Create(MatchConfig{
		MatchID:     "nohook",
		PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: "x"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
}
