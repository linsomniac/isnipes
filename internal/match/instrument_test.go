package match

import (
	"context"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// countingSampler records how many durations it observed.
type countingSampler struct{ n int }

func (c *countingSampler) Observe(time.Duration) { c.n++ }

// TestMatch_TickDurationRecorded — recordTick reports to the sampler, fires
// the over-budget hook only when a tick exceeds tickInterval, and is a
// no-op when the hooks are nil. (DoD #10)
func TestMatch_TickDurationRecorded(t *testing.T) {
	cs := &countingSampler{}
	overBudget := 0
	m := &Match{
		tickObserver:     cs,
		onTickOverBudget: func() { overBudget++ },
	}

	// Three in-budget ticks → 3 observations, 0 over-budget.
	for i := 0; i < 3; i++ {
		m.recordTick(time.Millisecond)
	}
	if cs.n != 3 {
		t.Fatalf("sampler observations=%d want 3", cs.n)
	}
	if overBudget != 0 {
		t.Fatalf("overBudget=%d want 0", overBudget)
	}
	if m.overBudget {
		t.Fatalf("overBudget flag set after in-budget ticks")
	}

	// One over-budget tick → hook fires, flag armed.
	m.recordTick(tickInterval + time.Millisecond)
	if cs.n != 4 {
		t.Fatalf("sampler observations=%d want 4", cs.n)
	}
	if overBudget != 1 {
		t.Fatalf("overBudget=%d want 1", overBudget)
	}
	if !m.overBudget {
		t.Fatalf("overBudget flag not armed after a slow tick")
	}

	// nil hooks must be a no-op (no panic).
	bare := &Match{}
	bare.recordTick(time.Second)
	if !bare.overBudget {
		t.Fatalf("bare match should still arm overBudget on a slow tick")
	}
}

// liveMatchForInstrument builds a 2-player live PvP match with buffered out
// channels, mirroring the hand-built setup other match tests use.
func liveMatchForInstrument(t *testing.T, onDrop func()) (*Match, map[sim.EntityID]chan OutboundFrame) {
	t.Helper()
	cfg := sim.Config{
		Seed:         0xDEADBEEF,
		Width:        60,
		Height:       40,
		PlayerIDs:    []sim.EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	m := &Match{
		cfg:                 MatchConfig{MatchID: "instr", Clock: time.Now},
		in:                  make(chan controlMsg, 256),
		sim:                 s,
		slots:               make(map[sim.EntityID]*Slot, 2),
		startingPlayerCount: 2,
		aoiPrev:             make(map[sim.EntityID]map[sim.EntityID]struct{}),
		pendingInputs:       make(map[sim.EntityID]proto.Input),
		owt:                 make(map[sim.EntityID]*OWTEstimator),
		dcTokens:            make(map[string]sim.EntityID),
		lastScores:          newScoreSnapshot(),
		done:                make(chan struct{}),
		clock:               time.Now,
		onSnapshotDrop:      onDrop,
	}
	m.stateAtomic.Store(uint32(StateLive))
	chans := make(map[sim.EntityID]chan OutboundFrame, 2)
	for _, id := range []sim.EntityID{1, 2} {
		ch := make(chan OutboundFrame, 1024)
		chans[id] = ch
		m.slots[id] = &Slot{PlayerID: id, Joined: true, out: ch, closed: make(chan struct{})}
		m.owt[id] = NewOWTEstimator()
	}
	return m, chans
}

func drainSnapshots(ch chan OutboundFrame) int {
	n := 0
	for {
		select {
		case f := <-ch:
			if f.Type == proto.MsgSnapshot {
				n++
			}
		default:
			return n
		}
	}
}

// TestMatch_OverBudgetSkipsNextSnapshot — an over-budget tick suppresses
// exactly the next scheduled snapshot beat (one-shot; never accumulates)
// and increments the drop counter; cadence then resumes. (DoD #10a)
func TestMatch_OverBudgetSkipsNextSnapshot(t *testing.T) {
	drops := 0
	m, chans := liveMatchForInstrument(t, func() { drops++ })
	ch := chans[1]

	// Snapshots fire when ServerTick % snapshotEveryTicks == 0. Confirm the
	// normal cadence: tick1 (ST=1) none, tick2 (ST=2) one snapshot.
	m.tick()
	if got := drainSnapshots(ch); got != 0 {
		t.Fatalf("tick1 (ST=%d): snapshots=%d want 0", m.sim.ServerTick(), got)
	}
	m.tick()
	if got := drainSnapshots(ch); got != 1 {
		t.Fatalf("tick2 (ST=%d): snapshots=%d want 1", m.sim.ServerTick(), got)
	}

	// Arm over-budget during the non-beat tick; the NEXT beat must be
	// suppressed and counted.
	m.overBudget = true
	m.tick() // ST=3, non-beat
	if got := drainSnapshots(ch); got != 0 {
		t.Fatalf("tick3 (ST=%d): snapshots=%d want 0", m.sim.ServerTick(), got)
	}
	m.tick() // ST=4, beat — suppressed
	if got := drainSnapshots(ch); got != 0 {
		t.Fatalf("tick4 (ST=%d): expected suppressed snapshot, got %d", m.sim.ServerTick(), got)
	}
	if drops != 1 {
		t.Fatalf("snapshot drops=%d want 1", drops)
	}
	if m.overBudget {
		t.Fatalf("overBudget flag not cleared after suppression (must be one-shot)")
	}

	// Recovery: the following beat broadcasts again.
	m.tick() // ST=5, non-beat
	_ = drainSnapshots(ch)
	m.tick() // ST=6, beat — broadcasts
	if got := drainSnapshots(ch); got != 1 {
		t.Fatalf("tick6 (ST=%d): snapshots=%d want 1 (cadence resumed)", m.sim.ServerTick(), got)
	}
	if drops != 1 {
		t.Fatalf("snapshot drops=%d want 1 (no accumulation)", drops)
	}
}

// TestMatch_JoinedGaugeBalancesUnderBackpressure — a slot dropped for
// outbound backpressure releases its gauge contribution, and termination
// releases the rest with no double-count. (codex)
func TestMatch_JoinedGaugeBalancesUnderBackpressure(t *testing.T) {
	gauge := 0
	m := &Match{
		slots:         make(map[sim.EntityID]*Slot),
		onJoinedDelta: func(d int) { gauge += d },
	}
	s1 := &Slot{PlayerID: 1, Joined: true, out: make(chan OutboundFrame, 1), closed: make(chan struct{})}
	s2 := &Slot{PlayerID: 2, Joined: true, out: make(chan OutboundFrame, 1), closed: make(chan struct{})}
	m.slots[1], m.slots[2] = s1, s2
	m.adjustJoined(1) // simulate s1 join
	m.adjustJoined(1) // simulate s2 join
	if gauge != 2 {
		t.Fatalf("after 2 joins gauge=%d want 2", gauge)
	}

	// Fill s1's queue so the next send backpressures and drops it.
	s1.out <- OutboundFrame{}
	m.sendFrameTo(s1, proto.MsgEvent, proto.Event{Kind: 1})
	if gauge != 1 {
		t.Fatalf("after backpressure drop gauge=%d want 1", gauge)
	}
	if s1.Joined {
		t.Fatal("s1 still Joined after backpressure drop")
	}

	// Termination releases the still-joined s2; the already-dropped s1 is a
	// no-op (no double-decrement).
	m.releaseJoinedGauge()
	if gauge != 0 {
		t.Fatalf("after termination gauge=%d want 0 (unbalanced)", gauge)
	}
}

// TestRegistry_StopAll — StopAll ends every live match cleanly, drains the
// registry deterministically, and (crucially for the DoD #5 leak check)
// leaves NO tail goroutine per match. (DoD #5 support)
func TestRegistry_StopAll(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()

	reg := NewRegistry(RegistryConfig{MaxConcurrentMatches: 16})
	const n = 4
	for i := 0; i < n; i++ {
		_, err := reg.Create(MatchConfig{
			MatchID:     fmt.Sprintf("stopall-%d", i),
			PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: fmt.Sprintf("tok-%d", i)}},
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if reg.Len() != n {
		t.Fatalf("Len=%d want %d", reg.Len(), n)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if reg.Len() != 0 {
		t.Fatalf("after StopAll Len=%d want 0", reg.Len())
	}

	// Goroutine quiescence: no per-match absorber should linger. Allow a
	// brief settle for the Run goroutines' final scheduling.
	var after int
	for i := 0; i < 100; i++ {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= before+1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after > before+1 {
		t.Fatalf("goroutine leak after StopAll: before=%d after=%d (want ~before; "+
			"a tail absorber per match would show n=%d extra)", before, after, n)
	}

	// Idempotent: a second StopAll on a stopped registry is a no-op.
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("second StopAll: %v", err)
	}
}

// TestRegistry_ActiveMatchesGauge — OnActiveMatchesDelta is +1 per Create
// and -1 per drain, so the gauge reflects live matches. (DoD #8 producer)
func TestRegistry_ActiveMatchesGauge(t *testing.T) {
	// The hook is called from both the caller goroutine (Create) and match
	// Run goroutines (RemoveEnded), so it must be atomic — as the production
	// observ.Registry gauge is.
	var gauge atomic.Int64
	reg := NewRegistry(RegistryConfig{
		MaxConcurrentMatches: 16,
		OnActiveMatchesDelta: func(d int) { gauge.Add(int64(d)) },
	})
	const n = 3
	for i := 0; i < n; i++ {
		if _, err := reg.Create(MatchConfig{
			MatchID:     fmt.Sprintf("gauge-%d", i),
			PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: fmt.Sprintf("t-%d", i)}},
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if got := gauge.Load(); got != n {
		t.Fatalf("active gauge=%d want %d after Create", got, n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if got := gauge.Load(); got != 0 {
		t.Fatalf("active gauge=%d want 0 after drain", got)
	}
}

// TestRegistry_CreateRejectedAfterStopAll — once StopAll has begun, Create
// is rejected so shutdown can never be outrun by a new match. (codex P3)
func TestRegistry_CreateRejectedAfterStopAll(t *testing.T) {
	reg := NewRegistry(RegistryConfig{MaxConcurrentMatches: 16})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	_, err := reg.Create(MatchConfig{
		MatchID:     "after-stop",
		PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: "t"}},
	})
	if err != ErrRegistryClosed {
		t.Fatalf("Create after StopAll: err=%v want ErrRegistryClosed", err)
	}
}
