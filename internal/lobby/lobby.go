package lobby

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jafo/isnipes/internal/match"
	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// SessionID uniquely identifies a connected lobby WS.
type SessionID string

// Outbound is what the lobby pushes to a WS writer for one session.
type Outbound struct {
	Type    string // proto.Lobby* tag (or "close" for terminal)
	Payload any    // typed payload struct (Hello, Welcome, ...)
	// If CloseCode > 0, after sending Payload the WS should close.
	CloseCode int
}

// Config governs the lobby actor.
type Config struct {
	Registry      *match.Registry
	Clock         func() time.Time // nil → time.Now
	IdleTimeout   time.Duration    // default 60s
	TokenTTL      time.Duration    // default TokenTTL (60s)
	MOTD          string
	ServerVersion string

	// Phase 6 §10.3 — janitor cadence override for tests. Zero means
	// the default 10s cadence (production).
	JanitorInterval time.Duration
	// Phase 6 §10.1/§10.2 — empty-room / zero-player threshold. Zero
	// means the default 30s.
	GCInactivityThreshold time.Duration
}

// Session is one connected lobby WS.
type Session struct {
	ID        SessionID
	Nick      string
	out       chan Outbound
	closed    chan struct{}
	closeOnce sync.Once
	roomID    string // empty when not in a room
	// Last activity, for idle eviction.
	lastSeen time.Time

	// Phase 6 §6.2 — chat token bucket (lazy-init in handleChat).
	chat *chatBucket
}

// Lobby is the actor. Construct with NewLobby and run via Run().
type Lobby struct {
	cfg Config

	in      chan controlMsg
	done    chan struct{}
	clock   func() time.Time
	stopped atomic.Bool

	// Actor-owned state. Only Run() touches these.
	sessions map[SessionID]*Session
	rooms    map[string]*Room
	tokens   map[string]*pendingMatchJoin // active joinTokens

	// Phase 6 §10.2 — per-match zero-player tracking.
	matchTrackers map[string]*matchTracker

	// next EntityID for the lobby to hand out per room. Player IDs
	// must be globally unique within the (sim Config) check; we keep
	// them lobby-scoped and let each match's NewSim accept them.
	nextEntityID sim.EntityID

	// roomsDirty is set when dropSession frees an OPEN-room slot for a
	// force-dropped session. The room-list resync is deferred to the end of
	// the current actor step (flushRoomsDirty) rather than broadcast inline:
	// dropSession can run from inside send() during an in-flight broadcast
	// fan-out, so a synchronous rebroadcast would nest inside that loop —
	// overwriting the corrected list with the in-flight stale payload and
	// amplifying outbound pressure. Actor-owned (only Run() touches it).
	roomsDirty bool
}

// NewLobby constructs a Lobby actor.
func NewLobby(cfg Config) *Lobby {
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 60 * time.Second
	}
	if cfg.TokenTTL == 0 {
		cfg.TokenTTL = TokenTTL
	}
	if cfg.ServerVersion == "" {
		cfg.ServerVersion = "v1.0.1"
	}
	return &Lobby{
		cfg:           cfg,
		in:            make(chan controlMsg, 1024),
		done:          make(chan struct{}),
		clock:         cfg.Clock,
		sessions:      make(map[SessionID]*Session),
		rooms:         make(map[string]*Room),
		tokens:        make(map[string]*pendingMatchJoin),
		matchTrackers: make(map[string]*matchTracker),
		nextEntityID:  1,
	}
}

// Stop signals the actor to exit. Idempotent. Closes done; producers
// observe l.stopped.Load() before sending to l.in.
func (l *Lobby) Stop() {
	if l.stopped.Swap(true) {
		return
	}
	close(l.done)
}

// controlMsg dispatch.
type controlMsg interface{ controlTag() }

type ctlConnect struct {
	Out   chan Outbound
	Reply chan<- *Session
}

type ctlMessage struct {
	SessionID SessionID
	Raw       []byte
}

type ctlDisconnect struct {
	SessionID SessionID
}

// ctlTouch refreshes a session's lastSeen from a transport-level keepalive
// (a WS pong) so the idle sweep doesn't reap a connection that is alive but
// quiet — the lobby client legitimately sends no messages while a player
// reads the screen. See net.handleLobby's ping keepalive.
type ctlTouch struct {
	SessionID SessionID
}

type ctlMatchEnded struct{ MatchID string }

type ctlJanitor struct{}

func (ctlConnect) controlTag()    {}
func (ctlMessage) controlTag()    {}
func (ctlDisconnect) controlTag() {}
func (ctlTouch) controlTag()      {}
func (ctlMatchEnded) controlTag() {}
func (ctlJanitor) controlTag()    {}

// Connect registers a new WS session and returns its handle. The
// caller forwards subsequent inbound JSON bytes via PostMessage.
// The returned Session.Out can be drained by the WS writer.
// Returns nil if the lobby has stopped.
func (l *Lobby) Connect(out chan Outbound) *Session {
	if l.stopped.Load() {
		close(out)
		return nil
	}
	reply := make(chan *Session, 1)
	select {
	case l.in <- ctlConnect{Out: out, Reply: reply}:
	case <-l.done:
		close(out)
		return nil
	}
	select {
	case s := <-reply:
		return s
	case <-l.done:
		close(out)
		return nil
	}
}

// PostMessage delivers one inbound lobby WS message.
func (l *Lobby) PostMessage(sid SessionID, raw []byte) {
	if l.stopped.Load() {
		return
	}
	select {
	case l.in <- ctlMessage{SessionID: sid, Raw: raw}:
	case <-l.done:
	}
}

// Disconnect cleans up after the WS closes.
func (l *Lobby) Disconnect(sid SessionID) {
	if l.stopped.Load() {
		return
	}
	select {
	case l.in <- ctlDisconnect{SessionID: sid}:
	case <-l.done:
	}
}

// MatchEnded notifies the lobby that a match has finished so its hosting room
// is closed and removed from the listing (see handleMatchEnded). Posted by the
// match registry's OnMatchEnded hook. Safe to drop on shutdown — a stopped
// lobby has no rooms left to clean up. Like Disconnect (and unlike Touch),
// there is no default: arm — room cleanup must not be silently dropped on
// inbox backpressure; the send blocks until the actor accepts it or done is
// closed.
func (l *Lobby) MatchEnded(matchID string) {
	if l.stopped.Load() {
		return
	}
	select {
	case l.in <- ctlMatchEnded{MatchID: matchID}:
	case <-l.done:
	}
}

// Touch refreshes a session's idle timer from a transport keepalive (WS
// pong). Non-blocking and safe to drop: it's a liveness hint, not state.
func (l *Lobby) Touch(sid SessionID) {
	if l.stopped.Load() {
		return
	}
	select {
	case l.in <- ctlTouch{SessionID: sid}:
	case <-l.done:
	default:
	}
}

// Run drives the actor. Returns when Stop is called.
func (l *Lobby) Run() {
	interval := l.cfg.JanitorInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	janitorTicker := time.NewTicker(interval)
	defer janitorTicker.Stop()
	for {
		select {
		case <-l.done:
			return
		case msg := <-l.in:
			l.handle(msg)
			// Coalesce deferred room-list resyncs: while a burst of
			// control messages is still queued, hold off — one resync
			// once the inbox drains covers them all and keeps a forced-
			// drop storm from amplifying into a per-message fan-out.
			if len(l.in) == 0 {
				l.flushRoomsDirty()
			}
		case <-janitorTicker.C:
			l.expireTokens()
			l.expireIdleSessions()
			l.sweepEmptyRooms()
			l.sweepZeroPlayerMatches()
			l.flushRoomsDirty()
		}
	}
}

func (l *Lobby) handle(msg controlMsg) {
	switch v := msg.(type) {
	case ctlConnect:
		l.handleConnect(v)
	case ctlMessage:
		l.handleMessage(v)
	case ctlDisconnect:
		l.handleDisconnect(v)
	case ctlTouch:
		if s, ok := l.sessions[v.SessionID]; ok {
			s.lastSeen = l.clock()
		}
	case ctlMatchEnded:
		l.handleMatchEnded(v)
	}
}

func (l *Lobby) handleConnect(v ctlConnect) {
	sid := SessionID(newSessionID())
	s := &Session{
		ID:       sid,
		out:      v.Out,
		closed:   make(chan struct{}),
		lastSeen: l.clock(),
	}
	l.sessions[sid] = s
	v.Reply <- s
}

func (l *Lobby) handleMessage(v ctlMessage) {
	s, ok := l.sessions[v.SessionID]
	if !ok {
		return
	}
	s.lastSeen = l.clock()
	env, err := proto.DecodeLobbyEnvelope(v.Raw)
	if err != nil {
		// §6.2: envelope version mismatch is fatal (close 1003);
		// any other decode failure is recoverable (BAD_REQUEST).
		if err == proto.ErrLobbyVersion {
			l.sendErrorAndClose(s, proto.LobbyErrVersion, err.Error(), 1003)
			return
		}
		l.sendError(s, proto.LobbyErrBadRequest, err.Error())
		return
	}
	switch env.T {
	case proto.LobbyHello:
		var h proto.Hello
		if err := jsonUnmarshal(env.D, &h); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleHello(s, h)
	case proto.LobbyCreateRoom:
		var c proto.CreateRoom
		if err := jsonUnmarshal(env.D, &c); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleCreateRoom(s, c)
	case proto.LobbyJoinRoom:
		var j proto.JoinRoom
		if err := jsonUnmarshal(env.D, &j); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleJoinRoom(s, j)
	case proto.LobbyLeaveRoom:
		l.handleLeaveRoom(s)
	case proto.LobbyStartMatch:
		var sm proto.StartMatch
		if err := jsonUnmarshal(env.D, &sm); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleStartMatch(s, sm)
	case proto.LobbyChat:
		var c proto.LobbyChatPayload
		if err := jsonUnmarshal(env.D, &c); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleChat(s, c)
	case proto.LobbyKick:
		var k proto.LobbyKickPayload
		if err := jsonUnmarshal(env.D, &k); err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, err.Error())
			return
		}
		l.handleKick(s, k)
	default:
		l.sendError(s, proto.LobbyErrBadRequest, "unknown type: "+env.T)
	}
}

func (l *Lobby) handleHello(s *Session, h proto.Hello) {
	if h.SchemaChecksum != proto.SchemaChecksum() {
		l.sendErrorAndClose(s, proto.LobbyErrVersion, "schemaChecksum mismatch", 1003)
		return
	}
	nick := sanitizeNick(h.Nick)
	if nick == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "nick required")
		return
	}
	if l.nickInUse(nick, s.ID) {
		// Append #nnn suffix.
		for i := 1; i < 1000; i++ {
			candidate := fmt.Sprintf("%s#%03d", nick, i)
			if !l.nickInUse(candidate, s.ID) {
				nick = candidate
				break
			}
		}
	}
	s.Nick = nick
	// Reply with welcome + roomList.
	l.send(s, proto.LobbyWelcome, proto.Welcome{
		PlayerID:       string(s.ID),
		Nick:           nick,
		ServerVersion:  l.cfg.ServerVersion,
		SchemaChecksum: proto.SchemaChecksum(),
		MOTD:           l.cfg.MOTD,
	})
	l.send(s, proto.LobbyRoomList, l.buildRoomList())
}

func (l *Lobby) handleCreateRoom(s *Session, c proto.CreateRoom) {
	if s.Nick == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "hello first")
		return
	}
	if s.roomID != "" {
		l.sendError(s, proto.LobbyErrAlreadyInRoom, "already in a room")
		return
	}
	if c.Max < 2 || c.Max > 8 {
		l.sendError(s, proto.LobbyErrBadRequest, "max out of range")
		return
	}
	// Phase 6 §8.1: level validation.
	letter, num, err := ValidateLevel(c.Level)
	if err != nil {
		l.sendError(s, proto.LobbyErrBadLevel, "level must be A-Z × 1-9")
		return
	}
	// Regenerate on the (astronomically unlikely) collision so a new room
	// never silently overwrites a live one and orphans its members. The
	// lobby actor is single-threaded, so there is no TOCTOU here. Mirrors
	// the match registry's duplicate-ID guard.
	id := newRoomID()
	for i := 0; i < 8; i++ {
		if _, exists := l.rooms[id]; !exists {
			break
		}
		id = newRoomID()
	}
	room := &Room{
		ID:      id,
		Name:    strings.TrimSpace(c.Name),
		Host:    string(s.ID),
		Max:     c.Max,
		Level:   proto.Level{Letter: string(letter), Number: num},
		State:   RoomOpen,
		Members: []string{string(s.ID)},
	}
	l.rooms[id] = room
	s.roomID = id
	// Phase 6 §11: full roomList for the creator (synchronous reply to
	// their createRoom), plus a `room_added` delta to every session
	// (including the creator so the client builds a uniform delta-only
	// update path post-welcome).
	l.send(s, proto.LobbyRoomList, l.buildRoomList())
	l.broadcastRoomDelta(proto.LobbyRoomAdded, room)
}

func (l *Lobby) handleJoinRoom(s *Session, j proto.JoinRoom) {
	if s.Nick == "" {
		l.sendError(s, proto.LobbyErrBadRequest, "hello first")
		return
	}
	if s.roomID != "" {
		l.sendError(s, proto.LobbyErrAlreadyInRoom, "already in a room")
		return
	}
	room, ok := l.rooms[j.RoomID]
	if !ok || room.State == RoomClosed {
		l.sendError(s, proto.LobbyErrRoomGone, "no such room")
		return
	}
	if room.State != RoomOpen {
		l.sendError(s, proto.LobbyErrRoomGone, "room not accepting joins")
		return
	}
	if len(room.Members) >= room.Max {
		l.sendError(s, proto.LobbyErrRoomFull, "room is full")
		return
	}
	room.Members = append(room.Members, string(s.ID))
	s.roomID = room.ID
	// Phase 6 §11: room_updated delta carries the new member count.
	// Keep the full roomList broadcast for now (clients reconcile via
	// either path); a future iter may drop the full-list broadcast.
	l.broadcastRoomList()
	l.broadcastRoomDelta(proto.LobbyRoomUpdated, room)
}

func (l *Lobby) handleLeaveRoom(s *Session) {
	if s.roomID == "" {
		l.sendError(s, proto.LobbyErrNoRoom, "not in a room")
		return
	}
	room, ok := l.rooms[s.roomID]
	if !ok {
		s.roomID = ""
		return
	}
	l.removeFromRoom(s, room)
}

func (l *Lobby) removeFromRoom(s *Session, room *Room) {
	l.removeFromRoomMembership(s, room)
	l.broadcastRoomList()
	// Phase 6 §11: delta envelope alongside the legacy full-list.
	l.broadcastRoomDelta(proto.LobbyRoomUpdated, room)
}

// removeFromRoomMembership drops s from room.Members and applies the
// host-transfer + empty-stamp bookkeeping WITHOUT broadcasting. Callers
// running outside an in-flight broadcast (removeFromRoom) follow this with
// the broadcasts; the drop path (dropSession), which can be reached from
// inside send() during a broadcast, relies on broadcastRoomList's
// re-entrancy coalescing instead.
func (l *Lobby) removeFromRoomMembership(s *Session, room *Room) {
	for i, pid := range room.Members {
		if pid == string(s.ID) {
			room.Members = append(room.Members[:i], room.Members[i+1:]...)
			break
		}
	}
	s.roomID = ""
	// Phase 6 §7.4: host transfer. If the leaver was the host AND there
	// are remaining members, the oldest-joined member becomes the new host
	// (Members is kept in join order).
	if room.Host == string(s.ID) && len(room.Members) > 0 {
		room.Host = room.Members[0]
	}
	if len(room.Members) == 0 {
		// Truly empty — stamp EmptySince and let the §10.1 sweep GC it.
		room.EmptySince = l.clock()
	}
}

func (l *Lobby) handleStartMatch(s *Session, sm proto.StartMatch) {
	if s.roomID == "" {
		l.sendError(s, proto.LobbyErrNoRoom, "not in a room")
		return
	}
	room, ok := l.rooms[s.roomID]
	if !ok {
		l.sendError(s, proto.LobbyErrRoomGone, "no such room")
		return
	}
	if room.Host != string(s.ID) {
		l.sendError(s, proto.LobbyErrNotHost, "only host may start")
		return
	}
	if len(room.Members) < 2 {
		l.sendError(s, proto.LobbyErrBadRequest, "need ≥ 2 players")
		return
	}
	if room.State != RoomOpen {
		l.sendError(s, proto.LobbyErrBadRequest, "room not in OPEN state")
		return
	}
	// AIDEV-NOTE: Propagate the room's selected level into the match. If
	// this is dropped, MatchConfig.LevelLetter defaults to 0, which
	// startOrAbort (match.go) treats as Phase-2 PvP-only and sets
	// NoGenerators — suppressing generators AND the snipes they emit, so no
	// enemies appear at all. The level was validated at room creation, so a
	// failure here is defensive only.
	levelLetter, levelNumber, err := ValidateLevel(room.Level)
	if err != nil {
		l.sendError(s, proto.LobbyErrBadLevel, "room level invalid")
		return
	}
	room.State = RoomStarting
	// Allocate per-room EntityIDs and tokens.
	matchID := newMatchID()
	mapSeed := randomSeed()
	pending := make([]match.PendingJoin, 0, len(room.Members))
	for _, pidStr := range room.Members {
		eid := l.nextEntityID
		l.nextEntityID++
		tok, err := generateToken()
		if err != nil {
			l.sendError(s, proto.LobbyErrBadRequest, "token gen: "+err.Error())
			// Mirror the Registry.Create failure path: roll the room back
			// to OPEN and drop the tokens already issued this start, so a
			// transient token-gen failure does not leave the room wedged in
			// STARTING with leaked tokens.
			room.State = RoomOpen
			for _, p := range pending {
				delete(l.tokens, p.Token)
			}
			return
		}
		nick := ""
		if other, ok := l.sessions[SessionID(pidStr)]; ok {
			nick = other.Nick
		}
		pending = append(pending, match.PendingJoin{
			MatchID:  matchID,
			Token:    tok,
			PlayerID: eid,
			Nick:     nick,
			IssuedAt: l.clock(),
		})
		// Record in lobby's token map for tracking + janitor TTL.
		l.tokens[tok] = &pendingMatchJoin{
			Token:     tok,
			MatchID:   matchID,
			SessionID: SessionID(pidStr),
			PlayerID:  eid,
			Nick:      nick,
			IssuedAt:  l.clock(),
		}
	}
	// Create the match.
	mc := match.MatchConfig{
		MatchID:     matchID,
		MapSeed:     mapSeed,
		MapWidth:    120, // MAZE_REVAMP.md: wide-corridor maze default
		MapHeight:   80,
		PlayerSlots: pending,
		LevelLetter: levelLetter,
		LevelNumber: levelNumber,
	}
	if l.cfg.Registry != nil {
		_, err := l.cfg.Registry.Create(mc)
		if err != nil {
			l.sendError(s, proto.LobbyErrRoomFull, err.Error())
			room.State = RoomOpen
			// Clean up the issued tokens.
			for _, p := range pending {
				delete(l.tokens, p.Token)
			}
			return
		}
	}
	room.MatchID = matchID
	l.matchTrackers[matchID] = &matchTracker{matchID: matchID}
	// §7.2: stay in STARTING until at least one MatchJoin succeeds.
	// Phase 2 does not wire a match→lobby callback for that
	// transition; rooms remain STARTING until ctlMatchEnded fires.
	// This is preferable to mislabelling the state.

	// Send each member their own matchStarted.
	for i, pidStr := range room.Members {
		sess, ok := l.sessions[SessionID(pidStr)]
		if !ok {
			continue
		}
		ms := proto.MatchStarted{
			MatchID:        matchID,
			GameSocketPath: "/ws/match/" + matchID,
			TickRate:       30,
			MapSeed:        mapSeed,
			JoinToken:      pending[i].Token,
		}
		l.send(sess, proto.LobbyMatchStarted, ms)
	}
	l.broadcastRoomList()
}

func (l *Lobby) handleDisconnect(v ctlDisconnect) {
	s, ok := l.sessions[v.SessionID]
	if !ok {
		return
	}
	// §7.1: leaving the lobby WS during a match is allowed; the room
	// stays alive (closes on MatchOver via ctlMatchEnded). Only run
	// room cleanup if the room is in a pre-match state.
	if s.roomID != "" {
		if r, ok := l.rooms[s.roomID]; ok {
			if r.State == RoomOpen {
				l.removeFromRoom(s, r)
			}
			// For STARTING / IN_MATCH / CLOSED, leave the room
			// untouched; the match lifecycle owns the teardown.
		}
	}
	delete(l.sessions, v.SessionID)
	s.closeOnce.Do(func() { close(s.closed); close(s.out) })
}

func (l *Lobby) handleMatchEnded(v ctlMatchEnded) {
	for _, r := range l.rooms {
		if r.MatchID == v.MatchID {
			r.State = RoomClosed
			delete(l.rooms, r.ID)
			for _, pid := range r.Members {
				if s, ok := l.sessions[SessionID(pid)]; ok {
					s.roomID = ""
				}
			}
			break
		}
	}
	l.broadcastRoomList()
}

func (l *Lobby) expireTokens() {
	now := l.clock()
	for k, p := range l.tokens {
		if now.Sub(p.IssuedAt) > l.cfg.TokenTTL {
			delete(l.tokens, k)
		}
	}
}

func (l *Lobby) expireIdleSessions() {
	now := l.clock()
	for sid, s := range l.sessions {
		if now.Sub(s.lastSeen) > l.cfg.IdleTimeout {
			// Route through handleDisconnect so room cleanup is
			// consistent with explicit close paths.
			l.handleDisconnect(ctlDisconnect{SessionID: sid})
		}
	}
}

func (l *Lobby) nickInUse(nick string, except SessionID) bool {
	for sid, s := range l.sessions {
		if sid == except {
			continue
		}
		if s.Nick == nick {
			return true
		}
	}
	return false
}

func (l *Lobby) buildRoomList() proto.RoomList {
	out := proto.RoomList{Rooms: make([]proto.RoomDescriptor, 0, len(l.rooms))}
	for _, r := range l.rooms {
		out.Rooms = append(out.Rooms, r.describe())
	}
	return out
}

func (l *Lobby) broadcastRoomList() {
	rl := l.buildRoomList()
	for _, s := range l.sessions {
		l.send(s, proto.LobbyRoomList, rl)
	}
}

// flushRoomsDirty emits an authoritative room-list resync if a session was
// force-dropped from an OPEN room during the current actor step (see
// roomsDirty). Deferred to here so the resync is the LAST frame clients
// receive — it never nests inside an in-flight broadcast fan-out, so it
// cannot be overwritten by an already-built stale delta. The loop converges
// because the resync runs outside any fan-out and each additional drop it
// triggers removes a session, shrinking l.sessions.
func (l *Lobby) flushRoomsDirty() {
	for l.roomsDirty {
		l.roomsDirty = false
		l.broadcastRoomList()
	}
}

// send pushes an Outbound to the session. Non-blocking; drops on
// queue full (closes the session).
func (l *Lobby) send(s *Session, t string, payload any) {
	select {
	case s.out <- Outbound{Type: t, Payload: payload}:
	default:
		// Queue full → drop the session.
		l.dropSession(s, "queue full")
	}
}

func (l *Lobby) sendError(s *Session, code, message string) {
	l.send(s, proto.LobbyTagError, proto.LobbyError{Code: code, Message: message})
}

func (l *Lobby) sendErrorAndClose(s *Session, code, message string, closeCode int) {
	select {
	case s.out <- Outbound{Type: proto.LobbyTagError,
		Payload:   proto.LobbyError{Code: code, Message: message},
		CloseCode: closeCode}:
	default:
	}
	l.dropSession(s, code)
}

func (l *Lobby) dropSession(s *Session, _ string) {
	s.closeOnce.Do(func() {
		close(s.closed)
		// Close the outbound channel; the WS writer goroutine will
		// observe the close and shut down its loop.
		close(s.out)
	})
	delete(l.sessions, s.ID)
	// §7.1 parity with handleDisconnect: a force-dropped client (queue
	// overflow in send, or sendErrorAndClose) that was in a pre-match
	// (OPEN) room must free its slot — otherwise a phantom member keeps
	// the room un-GC-able (sweepEmptyRooms only reaps len(Members)==0) and
	// the slot stays occupied forever. STARTING/IN_MATCH/CLOSED rooms are
	// owned by the match lifecycle and left untouched, exactly as
	// handleDisconnect does. We delete from l.sessions first so the
	// broadcast below skips the now-closed session.
	if s.roomID != "" {
		if r, ok := l.rooms[s.roomID]; ok && r.State == RoomOpen {
			l.removeFromRoomMembership(s, r)
			// Defer the resync to flushRoomsDirty (end of the actor step):
			// see roomsDirty's doc. The membership mutation above already
			// frees the slot and makes the room GC-able regardless.
			l.roomsDirty = true
		}
	}
}

// Helpers.

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// newSessionID returns a fresh UUIDv7-style identifier. Phase 2 uses
// a simple 22-char base64 of random bytes — sufficient for uniqueness
// across the lifetime of a process.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawStdEncoding.EncodeToString(b[:])
}

// newRoomID returns a 6-char base32 (RFC 4648, no padding) suitable
// for deep-linking per §6.3.
func newRoomID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	id := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	// 4 bytes = ~6.4 base32 chars; take first 6.
	if len(id) > 6 {
		id = id[:6]
	}
	return id
}

// newMatchID returns a 22-char base64 (url-safe, no padding).
func newMatchID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func randomSeed() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func sanitizeNick(n string) string {
	n = strings.TrimSpace(n)
	if len(n) == 0 || len(n) > 16 {
		return ""
	}
	for _, r := range n {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return ""
		}
	}
	return n
}
