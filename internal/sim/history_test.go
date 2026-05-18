package sim

import "testing"

func TestHistory_RingWriteWraps(t *testing.T) {
	var h entityHistory
	for i := uint32(100); i < 112; i++ {
		h.write(i, int32(i), int32(i*2), uint8(i&0xff))
	}
	if h.count != HistoryDepth {
		t.Fatalf("count=%d want %d", h.count, HistoryDepth)
	}
	// Newest sample is at h.head and corresponds to tick 111.
	if got := h.samples[h.head].tick; got != 111 {
		t.Fatalf("head tick=%d want 111", got)
	}
	// Oldest valid sample tick = 111 - 8 = 103.
	oldest := (h.head + HistoryDepth - (h.count - 1)) % HistoryDepth
	if got := h.samples[oldest].tick; got != 103 {
		t.Fatalf("oldest tick=%d want 103", got)
	}
}

func TestHistory_At_PresentTick(t *testing.T) {
	var h entityHistory
	for i := uint32(100); i <= 108; i++ {
		h.write(i, int32(i*10), int32(i*100), 0)
	}
	for _, want := range []uint32{100, 104, 108} {
		s, ok := h.at(want)
		if !ok || s.tick != want {
			t.Fatalf("at(%d): tick=%d ok=%v", want, s.tick, ok)
		}
		if s.x != int32(want*10) || s.y != int32(want*100) {
			t.Fatalf("at(%d): x=%d y=%d", want, s.x, s.y)
		}
	}
}

func TestHistory_At_ClampsToOldest(t *testing.T) {
	var h entityHistory
	for i := uint32(100); i <= 108; i++ {
		h.write(i, int32(i), 0, 0)
	}
	s, ok := h.at(50) // far older than oldest (=100)
	if !ok {
		t.Fatal("at(50): ok=false")
	}
	if s.tick != 100 {
		t.Fatalf("at(50): clamped tick=%d want 100", s.tick)
	}
}

func TestHistory_At_EmptyReturnsFalse(t *testing.T) {
	var h entityHistory
	if _, ok := h.at(5); ok {
		t.Fatal("at on empty history: ok=true")
	}
}

func TestHistory_DeadFlagPropagates(t *testing.T) {
	var h entityHistory
	h.write(10, 1, 2, FlagDead|FlagSpawnInvuln)
	s, ok := h.at(10)
	if !ok {
		t.Fatal("at: ok=false")
	}
	if s.flags&FlagDead == 0 || s.flags&FlagSpawnInvuln == 0 {
		t.Fatalf("flags=%08b dropped", s.flags)
	}
}

func TestHistory_Reset(t *testing.T) {
	var h entityHistory
	for i := uint32(1); i <= 5; i++ {
		h.write(i, 0, 0, 0)
	}
	h.reset()
	if h.count != 0 || h.head != 0 {
		t.Fatalf("after reset: head=%d count=%d", h.head, h.count)
	}
	if _, ok := h.at(3); ok {
		t.Fatal("at after reset: ok=true")
	}
}

func TestHistory_WrittenEveryTickInSim(t *testing.T) {
	cfg := Config{
		Seed: 0xDECAFBAD, Width: 60, Height: 40,
		PlayerIDs: []EntityID{1},
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := s.Tick(nil); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	h, ok := s.store.histories[1]
	if !ok {
		t.Fatal("player history missing")
	}
	if h.count != HistoryDepth {
		t.Fatalf("count=%d want %d", h.count, HistoryDepth)
	}
	// Newest sample should match serverTick (=12).
	if h.samples[h.head].tick != s.serverTick {
		t.Fatalf("head tick=%d want %d", h.samples[h.head].tick, s.serverTick)
	}
}

func TestHistory_PrunedOneTickAfterRemoval(t *testing.T) {
	cfg := Config{
		Seed: 0xDECAFBAD, Width: 60, Height: 40,
		PlayerIDs: []EntityID{1},
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	// Tick once so the generator histories get populated.
	if _, err := s.Tick(nil); err != nil {
		t.Fatal(err)
	}
	// Grab a generator ID; manually remove it.
	var genID EntityID
	for i := range s.store.slots {
		if s.store.slots[i].ID != 0 && s.store.slots[i].Kind == KindGenerator {
			genID = s.store.slots[i].ID
			break
		}
	}
	if genID == 0 {
		t.Skip("no generators in this seed; test relies on auto-spawned gens")
	}
	if _, ok := s.store.histories[genID]; !ok {
		t.Fatal("expected history for live generator")
	}
	s.store.remove(genID)
	// Tick once: history must still be present (one extra tick).
	if _, err := s.Tick(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.store.histories[genID]; !ok {
		t.Fatal("history pruned too early (1 tick after removal)")
	}
	// Tick again: now pruned.
	if _, err := s.Tick(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.store.histories[genID]; ok {
		t.Fatal("history not pruned after 2 ticks")
	}
}
