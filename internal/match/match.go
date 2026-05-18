package match

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// Phase 2 constants (mirror PHASE2.md §9, §10).
const (
	TickHz             = 30
	tickInterval       = time.Second / TickHz
	snapshotEveryTicks = 2 // → 15 Hz snapshot cadence

	MaxPlayersPerMatch = 8

	matchWarmupTimeout = 10 * time.Second
	idleFirstFrame     = 5 * time.Second
)

// Ticker is an injectable abstraction for unit tests. Production uses
// time.NewTicker; tests use a fake ticker driven by an explicit channel.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

// NewRealTicker returns a Ticker driven by time.NewTicker.
func NewRealTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

// MatchState is the lifecycle state (§9.1).
type MatchState uint8

const (
	StateNew MatchState = iota
	StateWaitingForJoins
	StateLive
	StateEnded
)

// Slot is one player's reservation in the match.
type Slot struct {
	PlayerID  sim.EntityID
	Nick      string
	Joined    bool
	out       chan<- OutboundFrame
	closeOnce sync.Once
	closed    chan struct{}
}

// OutboundFrame is what the match actor pushes to a WS writer.
type OutboundFrame struct {
	Type    proto.MsgType
	Payload []byte
}

// PendingJoin is the per-(matchId, token) state created when the
// lobby mints a token; the match actor consumes it on MatchJoin.
type PendingJoin struct {
	MatchID  string
	Token    string
	PlayerID sim.EntityID
	Nick     string
	IssuedAt time.Time
}

// MatchConfig is supplied at construction.
type MatchConfig struct {
	MatchID     string
	MapSeed     uint32
	MapWidth    int
	MapHeight   int
	PlayerSlots []PendingJoin    // pre-allocated EntityIDs for joining players
	Ticker      Ticker           // nil → NewRealTicker(tickInterval)
	Clock       func() time.Time // nil → time.Now

	// Phase 3: level table. Zero/Zero = Phase 2 PvP-only (no snipes,
	// no generators). Non-zero pair = full PvE match.
	LevelLetter byte
	LevelNumber int
}

// controlMsg is the internal inbox payload.
type controlMsg interface{ controlTag() }

type ctlJoin struct {
	Token string
	Out   chan<- OutboundFrame
	Reply chan<- joinResult
}

type joinResult struct {
	PlayerID sim.EntityID
	Err      error
}

type ctlInput struct {
	PlayerID sim.EntityID
	Input    proto.Input
}

type ctlPong struct {
	PlayerID sim.EntityID
	RTTMs    uint32
}

type ctlClose struct {
	PlayerID sim.EntityID
	Reason   uint8 // §6.4: 0 clean, 1 timeout
}

type ctlAbort struct{ Reason string }

func (ctlJoin) controlTag()  {}
func (ctlInput) controlTag() {}
func (ctlPong) controlTag()  {}
func (ctlClose) controlTag() {}
func (ctlAbort) controlTag() {}

// Match is the actor. Construct with NewMatch and run via Run().
type Match struct {
	cfg              MatchConfig
	in               chan controlMsg
	stateAtomic      atomic.Uint32 // holds MatchState; updated by Run goroutine, read by anyone
	serverTickAtomic atomic.Uint32 // mirrors sim.ServerTick() for race-free external reads
	sim              *sim.Sim
	slots            map[sim.EntityID]*Slot
	bySession        map[string]sim.EntityID // token → playerID
	startedAt        time.Time
	endedAt          time.Time
	clock            func() time.Time
	ticker           Ticker

	// per-player input buffer: latest input received since previous tick.
	pendingInputs map[sim.EntityID]proto.Input

	// Phase 4 §7 / §15.1: per-player OWT estimator driven by Pong
	// frames. Read at tick() time to stamp PlayerInput.LagComp.
	owt map[sim.EntityID]*OWTEstimator

	// emitted match-started flag (per §6.4 "once").
	matchStartedEmitted bool

	// for tests: events of last tick, for inspection.
	lastTickEvents []proto.Event

	// Captured at transition to StateLive (since players are GC'd as
	// they die under NoRespawn=true; we cannot recount later).
	startingPlayerCount int

	// isPvE flips to true on transition to StateLive when the sim was
	// constructed with a level table (LevelLetter != 0). Determines
	// whether evaluateMatchEnd evaluates the PVE_COMPLETE rule.
	isPvE bool
}

// NewMatch constructs a Match in StateNew.
func NewMatch(cfg MatchConfig) (*Match, error) {
	if cfg.MatchID == "" {
		return nil, errors.New("match: empty MatchID")
	}
	if len(cfg.PlayerSlots) < 1 || len(cfg.PlayerSlots) > MaxPlayersPerMatch {
		return nil, fmt.Errorf("match: invalid PlayerSlots len %d", len(cfg.PlayerSlots))
	}
	if cfg.MapWidth == 0 {
		cfg.MapWidth = 60
	}
	if cfg.MapHeight == 0 {
		cfg.MapHeight = 40
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.Ticker == nil {
		cfg.Ticker = NewRealTicker(tickInterval)
	}
	m := &Match{
		cfg:           cfg,
		in:            make(chan controlMsg, 256),
		slots:         make(map[sim.EntityID]*Slot, len(cfg.PlayerSlots)),
		bySession:     make(map[string]sim.EntityID, len(cfg.PlayerSlots)),
		clock:         cfg.Clock,
		ticker:        cfg.Ticker,
		pendingInputs: make(map[sim.EntityID]proto.Input),
		owt:           make(map[sim.EntityID]*OWTEstimator),
	}
	for _, p := range cfg.PlayerSlots {
		m.slots[p.PlayerID] = &Slot{PlayerID: p.PlayerID, Nick: p.Nick, closed: make(chan struct{})}
		m.bySession[p.Token] = p.PlayerID
		m.owt[p.PlayerID] = NewOWTEstimator()
	}
	m.setState(StateWaitingForJoins)
	return m, nil
}

// In returns the control inbox for producers.
func (m *Match) In() chan<- controlMsg { return m.in }

// MatchID returns the configured match ID.
func (m *Match) MatchID() string { return m.cfg.MatchID }

// state / setState are the actor-internal getters; for race-free
// external observation, see (*Match).State().
func (m *Match) state() MatchState     { return MatchState(m.stateAtomic.Load()) }
func (m *Match) setState(s MatchState) { m.stateAtomic.Store(uint32(s)) }

// State returns the current lifecycle state. Safe for concurrent
// callers (atomic load).
func (m *Match) State() MatchState { return m.state() }

// ServerTick returns the sim's current tick (0 if sim not yet created).
// Safe for concurrent reads (atomic load mirrored from the actor goroutine).
func (m *Match) ServerTick() uint32 {
	return m.serverTickAtomic.Load()
}

// SubmitJoin is the synchronous join helper used by tests and by the
// net layer's reader goroutine. It blocks until the actor accepts or
// rejects.
func (m *Match) SubmitJoin(token string, out chan<- OutboundFrame) (sim.EntityID, error) {
	reply := make(chan joinResult, 1)
	m.in <- ctlJoin{Token: token, Out: out, Reply: reply}
	r := <-reply
	return r.PlayerID, r.Err
}

// SubmitInput posts a player input to the actor. Non-blocking when
// the inbox has room.
func (m *Match) SubmitInput(playerID sim.EntityID, inp proto.Input) {
	m.in <- ctlInput{PlayerID: playerID, Input: inp}
}

// SubmitPong forwards an inbound Pong round-trip sample to the
// match actor so its per-player OWT estimator can update.
// rttMs is the server-side RTT in milliseconds (now - pong.TsOrigin).
// PHASE4.md §7.2.
func (m *Match) SubmitPong(playerID sim.EntityID, rttMs uint32) {
	m.in <- ctlPong{PlayerID: playerID, RTTMs: rttMs}
}

// SubmitClose informs the actor that a player's WS has closed.
func (m *Match) SubmitClose(playerID sim.EntityID, reason uint8) {
	m.in <- ctlClose{PlayerID: playerID, Reason: reason}
}

// Run drives the match to completion. Returns when ENDED or aborted.
func (m *Match) Run() {
	defer func() {
		if r := recover(); r != nil {
			m.abortFromPanic(r)
		}
		m.ticker.Stop()
	}()

	warmupStart := m.clock()
	for {
		select {
		case msg := <-m.in:
			m.handleControl(msg)
			if m.state() == StateEnded {
				return
			}
		case <-m.ticker.C():
			switch m.state() {
			case StateWaitingForJoins:
				if m.clock().Sub(warmupStart) >= matchWarmupTimeout {
					m.startOrAbort()
				}
			case StateLive:
				m.tick()
				if m.state() == StateEnded {
					return
				}
			}
		}
	}
}

// handleControl dispatches one inbox message.
func (m *Match) handleControl(msg controlMsg) {
	switch v := msg.(type) {
	case ctlJoin:
		m.handleJoin(v)
	case ctlInput:
		if m.state() != StateLive {
			return // silently drop pre-live inputs
		}
		if _, ok := m.slots[v.PlayerID]; !ok {
			return
		}
		// Latest input for this player wins (§9.2).
		m.pendingInputs[v.PlayerID] = v.Input
	case ctlPong:
		if est, ok := m.owt[v.PlayerID]; ok {
			est.ObservePong(v.RTTMs)
		}
	case ctlClose:
		m.handleClose(v)
	case ctlAbort:
		m.abort(v.Reason)
	}
}

// tokenTTL is the per-token validity window applied at MatchJoin time.
// Mirrors lobby.TokenTTL (60s) so an expired token gets the same
// AUTH rejection regardless of which side checks first.
const tokenTTL = 60 * time.Second

// handleJoin processes a MatchJoin. Reply is sent via v.Reply.
func (m *Match) handleJoin(v ctlJoin) {
	// Phase 2 does not support late join (§9.3 / §1 "no late-join").
	if m.state() == StateLive || m.state() == StateEnded {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	pid, ok := m.bySession[v.Token]
	if !ok {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	// Find the PendingJoin to check TTL.
	for _, p := range m.cfg.PlayerSlots {
		if p.Token == v.Token {
			if m.clock().Sub(p.IssuedAt) > tokenTTL {
				v.Reply <- joinResult{Err: ErrAuth}
				delete(m.bySession, v.Token)
				return
			}
			break
		}
	}
	slot, ok := m.slots[pid]
	if !ok {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	if slot.Joined {
		v.Reply <- joinResult{Err: ErrAuth} // single-use token
		return
	}
	slot.Joined = true
	slot.out = v.Out
	delete(m.bySession, v.Token) // tokens are single-use

	// Phase 2 §8.2: send player_join, MapInit, Scoreboard, Snapshot,
	// (match_started if first), in that order.
	m.sendFrameTo(slot, proto.MsgEvent, proto.Event{
		Kind: uint8(proto.EventPlayerJoin), Target: uint32(pid),
	})

	// If sim isn't built yet, build it on transition to LIVE.
	if m.allSlotsJoined() {
		m.startOrAbort()
	}

	// If sim is already live, send MapInit/Scoreboard/Snapshot now.
	if m.state() == StateLive {
		m.sendMapInitTo(slot)
		m.sendScoreboardTo(slot)
		m.sendSnapshotTo(slot)
	}

	// Reply *after* state transition so the caller observes the
	// post-transition state via Match.State(). The post-transition
	// path above always reaches StateLive (or aborts to StateEnded
	// via the < 2 check inside startOrAbort, in which case the
	// reply still goes out — callers are expected to treat ErrAuth
	// on close as a transient close, not as a join failure).
	v.Reply <- joinResult{PlayerID: pid}
}

// allSlotsJoined reports whether every reserved slot has had its
// MatchJoin processed.
func (m *Match) allSlotsJoined() bool {
	for _, s := range m.slots {
		if !s.Joined {
			return false
		}
	}
	return true
}

// joinedCount returns how many slots have completed MatchJoin.
func (m *Match) joinedCount() int {
	n := 0
	for _, s := range m.slots {
		if s.Joined {
			n++
		}
	}
	return n
}

// startOrAbort transitions to LIVE if enough players have joined,
// else aborts with NO_OPPONENT.
//
// PvP-only matches (LevelLetter=0) require ≥ 2 players per §9.3
// LAST_STANDING semantics. PvE matches (level table active) accept a
// single player — SPEC §3.8.2 explicitly allows solo PvE.
func (m *Match) startOrAbort() {
	if m.state() != StateWaitingForJoins {
		return
	}
	minPlayers := 2
	if m.cfg.LevelLetter != 0 {
		minPlayers = 1
	}
	if m.joinedCount() < minPlayers {
		m.abort("NO_OPPONENT")
		return
	}
	pids := make([]sim.EntityID, 0, m.joinedCount())
	for _, s := range m.slots {
		if s.Joined {
			pids = append(pids, s.PlayerID)
		}
	}
	// Deterministic ID order for the sim.
	sortIDs(pids)
	cfg := sim.Config{
		Seed:        m.cfg.MapSeed,
		Width:       m.cfg.MapWidth,
		Height:      m.cfg.MapHeight,
		PlayerIDs:   pids,
		NoRespawn:   true,
		LevelLetter: m.cfg.LevelLetter,
		LevelNumber: m.cfg.LevelNumber,
	}
	// Phase 2 PvP-only mode has no level table; suppress generators.
	if m.cfg.LevelLetter == 0 {
		cfg.NoGenerators = true
	}
	s, err := sim.NewSim(cfg)
	if err != nil {
		m.abort("sim init: " + err.Error())
		return
	}
	m.sim = s
	m.setState(StateLive)
	m.startedAt = m.clock()

	// Send MapInit/Scoreboard/Snapshot/match_started to every joined slot.
	for _, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		m.sendMapInitTo(slot)
		m.sendScoreboardTo(slot)
		m.sendSnapshotTo(slot)
	}
	m.broadcastEvent(proto.Event{Kind: uint8(proto.EventMatchStarted)})
	m.matchStartedEmitted = true
	m.startingPlayerCount = len(pids)
	m.isPvE = cfg.LevelLetter != 0
}

// tick runs one sim step.
func (m *Match) tick() {
	// Build per-player input list (one input per joined+alive slot).
	inputs := make([]sim.PlayerInput, 0, len(m.pendingInputs))
	for pid, inp := range m.pendingInputs {
		var lc sim.FireOptions
		if est, ok := m.owt[pid]; ok {
			lc.OWTTicks = est.OWTTicks()
		}
		inputs = append(inputs, sim.PlayerInput{
			PlayerID:   pid,
			Dir:        sim.Dir(inp.Dir),
			Turbo:      inp.Turbo == 1,
			FireDir:    sim.Dir(inp.FireDir),
			ClientTick: inp.ClientTick,
			LagComp:    lc,
		})
	}
	// Clear the pending buffer (latest-wins consumed).
	m.pendingInputs = make(map[sim.EntityID]proto.Input, len(m.slots))

	events, err := m.sim.Tick(inputs)
	if err != nil {
		m.abort("sim tick: " + err.Error())
		return
	}
	m.serverTickAtomic.Store(m.sim.ServerTick())
	m.lastTickEvents = make([]proto.Event, 0, len(events))
	// Translate P1 sim events to wire events per §6.4.
	for _, e := range events {
		// Skip player entity_spawn (Phase 2 has no respawn).
		// In Phase 2 (NoRespawn=true) we never emit entity_spawn for
		// a player. P1 only emits entity_spawn for projectiles here
		// (and for player respawn, which is disabled). Forward all.
		ev := proto.Event{
			Kind:   e.Kind,
			Actor:  uint32(e.Actor),
			Target: uint32(e.Target),
			Reason: e.Reason,
		}
		m.lastTickEvents = append(m.lastTickEvents, ev)
		m.broadcastEvent(ev)
	}

	// Broadcast snapshot every snapshotEveryTicks.
	if m.sim.ServerTick()%snapshotEveryTicks == 0 {
		m.broadcastSnapshot()
	}

	// Evaluate match end (§9.3).
	if reason, winner, done := m.evaluateMatchEnd(); done {
		m.endMatch(reason, winner)
	}
}

// evaluateMatchEnd returns (reason, winner, done) per §9.3 + Phase 3
// §14 (PVE_COMPLETE first in priority).
func (m *Match) evaluateMatchEnd() (uint8, sim.EntityID, bool) {
	live := 0
	var lastAlive sim.EntityID
	startCount := m.joinedAtStart()
	liveGens := 0
	liveSnipes := 0
	for _, ent := range m.sim.Entities() {
		if ent.Flags&sim.FlagDead != 0 {
			continue
		}
		switch ent.Kind {
		case sim.KindPlayer:
			live++
			lastAlive = ent.ID
		case sim.KindGenerator:
			liveGens++
		case sim.KindSnipe:
			liveSnipes++
		}
	}
	// Phase 3 §14: PVE_COMPLETE wins ties — but only when the match
	// is a PvE match (level table active). PvP-only matches (Phase 2's
	// LevelLetter=0 mode) never have generators or snipes by design;
	// without this guard every PvP match would terminate at tick 1.
	if m.isPvE && liveGens == 0 && liveSnipes == 0 && live >= 1 {
		// SPEC §3.8: in PVE_COMPLETE the winner field is 0 (Phase 5
		// owns the scoreboard / tie resolution).
		return proto.EndPVEComplete, 0, true
	}
	switch {
	case live == 1 && startCount >= 2:
		return proto.EndLastStanding, lastAlive, true
	case live == 0:
		return proto.EndAllEliminated, 0, true
	}
	return 0, 0, false
}

// joinedAtStart returns the player count when the sim was created.
// Captured at transition to StateLive.
func (m *Match) joinedAtStart() int { return m.startingPlayerCount }

// endMatch broadcasts match_end and MatchOver, then closes all writers.
func (m *Match) endMatch(reason uint8, winner sim.EntityID) {
	if m.state() == StateEnded {
		return
	}
	m.setState(StateEnded)
	m.endedAt = m.clock()

	finalTick := m.sim.ServerTick()
	m.broadcastEvent(proto.Event{
		Kind:   uint8(proto.EventMatchEnd),
		Actor:  uint32(winner),
		Reason: reason,
	})

	mo := proto.MatchOver{
		FinalTick:   finalTick,
		Reason:      reason,
		WinnerIDOr0: uint32(winner),
		Entries:     m.buildMatchOverEntries(winner, reason),
	}
	for _, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		m.sendFrameTo(slot, proto.MsgMatchOver, mo)
		m.closeSlot(slot)
	}
	m.drainInboxAfterEnd()
}

func (m *Match) buildMatchOverEntries(winner sim.EntityID, reason uint8) []proto.MatchOverEntry {
	// Phase 3 §14: under PVE_COMPLETE all *surviving* players have
	// LivesRemaining=1; under LAST_STANDING only the winner does.
	// ALL_ELIMINATED leaves all at 0.
	living := make(map[sim.EntityID]bool)
	if m.sim != nil {
		for _, ent := range m.sim.Entities() {
			if ent.Kind == sim.KindPlayer && ent.Flags&sim.FlagDead == 0 {
				living[ent.ID] = true
			}
		}
	}
	entries := make([]proto.MatchOverEntry, 0, len(m.slots))
	for pid, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		var lives uint8
		switch reason {
		case proto.EndPVEComplete:
			if living[pid] {
				lives = 1
			}
		case proto.EndLastStanding:
			if pid == winner {
				lives = 1
			}
		}
		entries = append(entries, proto.MatchOverEntry{
			PlayerID:       uint32(pid),
			Score:          0,
			LivesRemaining: lives,
		})
	}
	sortEntries(entries)
	return entries
}

// abort transitions to ENDED with reason SERVER_ERROR (or NO_OPPONENT).
func (m *Match) abort(reason string) {
	if m.state() == StateEnded {
		return
	}
	m.setState(StateEnded)
	m.endedAt = m.clock()
	if m.sim != nil {
		// Best-effort MatchOver. One match_end broadcast, then one
		// MatchOver per slot.
		m.broadcastEvent(proto.Event{
			Kind:   uint8(proto.EventMatchEnd),
			Reason: proto.EndServerError,
		})
		mo := proto.MatchOver{
			FinalTick: m.sim.ServerTick(),
			Reason:    proto.EndServerError,
		}
		for _, slot := range m.slots {
			if !slot.Joined {
				continue
			}
			m.sendFrameTo(slot, proto.MsgMatchOver, mo)
			m.closeSlot(slot)
		}
	} else {
		// No sim yet — just close the slots.
		for _, slot := range m.slots {
			if slot.out != nil {
				m.closeSlot(slot)
			}
		}
	}
	// Drain any pending inbox messages so SubmitJoin callers don't
	// block forever waiting for a reply.
	m.drainInboxAfterEnd()
	_ = reason // logged at the caller layer
}

func (m *Match) abortFromPanic(r interface{}) {
	m.abort(fmt.Sprintf("panic: %v", r))
}

// handleClose marks a slot as left and emits player_leave.
func (m *Match) handleClose(v ctlClose) {
	slot, ok := m.slots[v.PlayerID]
	if !ok {
		return
	}
	if !slot.Joined {
		return
	}
	m.broadcastEvent(proto.Event{
		Kind:   uint8(proto.EventPlayerLeave),
		Target: uint32(v.PlayerID),
		Reason: v.Reason,
	})
	slot.Joined = false
	m.closeSlot(slot)
}

func (m *Match) closeSlot(slot *Slot) {
	slot.closeOnce.Do(func() {
		close(slot.closed)
		if slot.out != nil {
			close(slot.out)
			slot.out = nil
		}
	})
}

// Abort terminates the match immediately with SERVER_ERROR. Used by
// tests and by the lobby on shutdown.
func (m *Match) Abort(reason string) {
	select {
	case m.in <- ctlAbort{Reason: reason}:
	default:
		// Inbox full — best effort.
	}
}

// drainInboxAfterEnd best-effort drains any control messages still in
// the inbox after the match ENDs. ctlJoin senders are blocked on
// Reply; we send ErrAuth so they unblock.
//
// We drain non-blockingly so this never stalls the actor; a producer
// that *races* with this drain — i.e. enqueues a ctlJoin after we
// returned — will block forever. That race is unobservable in Phase 2
// because callers serialise through the lobby and registry handoff,
// but Phase 5's reconnect work will need a stronger contract.
func (m *Match) drainInboxAfterEnd() {
	for {
		select {
		case msg := <-m.in:
			if j, ok := msg.(ctlJoin); ok {
				j.Reply <- joinResult{Err: ErrAuth}
			}
		default:
			return
		}
	}
}

// endMatch's tail also needs to drain so a join that landed just
// before the natural end transition doesn't block. Handled by the
// same drainInboxAfterEnd call below.

// sendFrameTo encodes msg and pushes it to the slot's out channel.
// Drops the slot if the channel is full (PHASE2.md §10.2 backpressure
// policy). In Phase 2 we'd actually close the WS here; for now we
// just stop writing to that slot.
func (m *Match) sendFrameTo(slot *Slot, t proto.MsgType, msg encodable) {
	payload, err := msg.Encode(nil)
	if err != nil {
		// Encoder error means we built an invalid message; this is a
		// server bug. Abort the match.
		m.abort("encode: " + err.Error())
		return
	}
	if slot.out == nil {
		return
	}
	select {
	case slot.out <- OutboundFrame{Type: t, Payload: payload}:
	default:
		// Queue full: drop the slot.
		slot.Joined = false
		m.closeSlot(slot)
	}
}

// encodable is the common encoder interface used by every proto
// message type.
type encodable interface {
	Encode(dst []byte) ([]byte, error)
}

func (m *Match) sendMapInitTo(slot *Slot) {
	mi := proto.MapInit{
		Seed:        m.cfg.MapSeed,
		Width:       uint16(m.sim.Width()),
		Height:      uint16(m.sim.Height()),
		Packing:     1,
		PackedTiles: m.sim.MapBytes(),
	}
	m.sendFrameTo(slot, proto.MsgMapInit, mi)
}

func (m *Match) sendScoreboardTo(slot *Slot) {
	sb := proto.Scoreboard{
		ServerTick: m.sim.ServerTick(),
		Entries:    m.buildScoreboardEntries(),
	}
	m.sendFrameTo(slot, proto.MsgScoreboard, sb)
}

func (m *Match) buildScoreboardEntries() []proto.ScoreboardEntry {
	entries := make([]proto.ScoreboardEntry, 0, len(m.slots))
	for pid, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		// In Phase 2, lives = 1 while alive, 0 after death.
		lives := uint8(1)
		idx := m.sim.Entities()
		for _, e := range idx {
			if e.ID == pid && e.Flags&sim.FlagDead != 0 {
				lives = 0
			}
		}
		nick := slot.Nick
		if nick == "" {
			nick = fmt.Sprintf("P%d", pid)
		}
		entries = append(entries, proto.ScoreboardEntry{
			PlayerID: uint32(pid),
			Nick:     nick,
			Lives:    lives,
			Score:    0,
		})
	}
	sortScoreboardEntries(entries)
	return entries
}

func (m *Match) sendSnapshotTo(slot *Slot) {
	snap := m.buildSnapshotFor(slot.PlayerID)
	m.sendFrameTo(slot, proto.MsgSnapshot, snap)
}

func (m *Match) broadcastSnapshot() {
	// Build the shared entity bytes once; per recipient, only
	// `your_entity_id` differs. (See §17 Risks in PHASE2.md.)
	shared := m.buildSharedSnapshotPrefix()
	_ = shared
	for _, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		m.sendSnapshotTo(slot)
	}
}

func (m *Match) broadcastEvent(ev proto.Event) {
	for _, slot := range m.slots {
		if !slot.Joined {
			continue
		}
		m.sendFrameTo(slot, proto.MsgEvent, ev)
	}
}
