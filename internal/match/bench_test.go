//go:build testhooks

package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// BenchmarkAOIPriorityBuilder — Phase 5 DoD #6. Measures the
// per-recipient AOI priority builder against a 60-entity fixture.
// Target: ≤ 50 µs/op on ubuntu-latest.
func BenchmarkAOIPriorityBuilder(b *testing.B) {
	cfg := sim.Config{
		Seed:        0xC0FFEE,
		Width:       60,
		Height:      40,
		PlayerIDs:   []sim.EntityID{1, 2, 3, 4},
		LevelLetter: 'A',
		LevelNumber: 1,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		b.Fatal(err)
	}
	// Add ~50 snipes via the public-ish test seam (cross-pkg).
	for i := 0; i < 50; i++ {
		sim.SpawnSnipeOrZeroForTest(s, 0, 5+(i%50), 5+(i%30))
	}
	m := &Match{
		slots:               make(map[sim.EntityID]*Slot, 4),
		sim:                 s,
		startingPlayerCount: 4,
		aoiPrev:             make(map[sim.EntityID]map[sim.EntityID]struct{}),
	}
	for _, id := range []sim.EntityID{1, 2, 3, 4} {
		m.slots[id] = &Slot{PlayerID: id, Joined: true}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.buildPriorityAOISnapshotFor(1)
	}
}

// BenchmarkMatchTick_8P_Level9 — Phase 5 DoD #6. Full match-actor
// tick including sim.Tick + AOI snapshot build + scoreboard delta
// detector. Target: ≤ 4 ms/op without -race.
func BenchmarkMatchTick_8P_Level9(b *testing.B) {
	cfg := sim.Config{
		Seed:        0xC0FFEE,
		Width:       80,
		Height:      60,
		PlayerIDs:   []sim.EntityID{1, 2, 3, 4, 5, 6, 7, 8},
		LevelLetter: 'T',
		LevelNumber: 9,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		b.Fatal(err)
	}
	m := &Match{
		slots:               make(map[sim.EntityID]*Slot, 8),
		sim:                 s,
		startingPlayerCount: 8,
		isPvE:               true,
		aoiPrev:             make(map[sim.EntityID]map[sim.EntityID]struct{}),
		lastScores:          newScoreSnapshot(),
	}
	for _, id := range []sim.EntityID{1, 2, 3, 4, 5, 6, 7, 8} {
		m.slots[id] = &Slot{PlayerID: id, Joined: true}
	}
	// Warm up: drive 60 ticks to populate snipes/projectiles.
	for i := 0; i < 60; i++ {
		_, _ = s.Tick(nil)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Tick(nil)
		// Mirror tick()'s snapshot path for each joined slot.
		for pid := range m.slots {
			_ = m.buildSnapshotFor(pid)
		}
		_ = proto.MsgSnapshot
	}
}
