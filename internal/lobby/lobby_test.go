package lobby

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/match"
	"github.com/jafo/isnipes/internal/proto"
)

// fakeClock provides a deterministic time source for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLobby returns a lobby + its run-done channel.
func newTestLobby(t *testing.T) (*Lobby, *fakeClock, chan struct{}, *match.Registry) {
	t.Helper()
	fc := &fakeClock{now: time.Unix(0, 0)}
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 16})
	l := NewLobby(Config{
		Registry: reg,
		Clock:    fc.Now,
	})
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	return l, fc, done, reg
}

// connect simulates a WS open + returns the session and its out channel.
func connect(t *testing.T, l *Lobby) (*Session, chan Outbound) {
	t.Helper()
	out := make(chan Outbound, 16)
	s := l.Connect(out)
	return s, out
}

// sendEnvelope marshals a typed lobby payload and posts it.
func sendEnvelope(t *testing.T, l *Lobby, sid SessionID, tag string, payload any) {
	t.Helper()
	b, err := proto.EncodeLobbyMessage(tag, payload)
	if err != nil {
		t.Fatal(err)
	}
	l.PostMessage(sid, b)
}

// drainOf reads Outbounds matching the predicate within a timeout.
func drainUntil(t *testing.T, ch <-chan Outbound, want string, timeout time.Duration) (Outbound, bool) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case o, ok := <-ch:
			if !ok {
				return Outbound{}, false
			}
			if o.Type == want {
				return o, true
			}
		case <-timer.C:
			return Outbound{}, false
		}
	}
}

// drainRoomListWithCount reads roomLists until one has exactly `want`
// rooms, or times out.
func drainRoomListWithCount(t *testing.T, ch <-chan Outbound, want int, timeout time.Duration) (proto.RoomList, bool) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case o, ok := <-ch:
			if !ok {
				return proto.RoomList{}, false
			}
			if o.Type != proto.LobbyRoomList {
				continue
			}
			rl := o.Payload.(proto.RoomList)
			if len(rl.Rooms) == want {
				return rl, true
			}
		case <-timer.C:
			return proto.RoomList{}, false
		}
	}
}

// helloAndDrain sends hello and consumes welcome + initial empty roomList.
func helloAndDrain(t *testing.T, l *Lobby, s *Session, out chan Outbound, nick string) {
	t.Helper()
	sendEnvelope(t, l, s.ID, proto.LobbyHello, proto.Hello{
		Nick: nick, SchemaChecksum: proto.SchemaChecksum(),
	})
	if _, ok := drainUntil(t, out, proto.LobbyWelcome, 500*time.Millisecond); !ok {
		t.Fatalf("no welcome for %s", nick)
	}
	// Welcome is followed by an initial roomList; drain it.
	drainUntil(t, out, proto.LobbyRoomList, 500*time.Millisecond)
}

func TestLobbyHelloWelcome(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	s, out := connect(t, l)
	sendEnvelope(t, l, s.ID, proto.LobbyHello, proto.Hello{
		Nick:           "Alice",
		ClientVersion:  "v0.0.0",
		SchemaChecksum: proto.SchemaChecksum(),
	})
	o, ok := drainUntil(t, out, proto.LobbyWelcome, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no welcome received")
	}
	w, ok := o.Payload.(proto.Welcome)
	if !ok {
		t.Fatalf("payload type: %T", o.Payload)
	}
	if w.Nick != "Alice" {
		t.Fatalf("nick: %q", w.Nick)
	}
	if w.SchemaChecksum != proto.SchemaChecksum() {
		t.Fatalf("checksum mismatch")
	}
}

func TestLobbyHelloRejectsBadChecksum(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	s, out := connect(t, l)
	sendEnvelope(t, l, s.ID, proto.LobbyHello, proto.Hello{
		Nick: "X", SchemaChecksum: 0xDEADBEEF,
	})
	o, ok := drainUntil(t, out, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no error received")
	}
	e := o.Payload.(proto.LobbyError)
	if e.Code != proto.LobbyErrVersion {
		t.Fatalf("code=%s, want VERSION", e.Code)
	}
}

func TestLobbyNickCollisionAppendsSuffix(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	a, outA := connect(t, l)
	sendEnvelope(t, l, a.ID, proto.LobbyHello, proto.Hello{Nick: "Same", SchemaChecksum: proto.SchemaChecksum()})
	drainUntil(t, outA, proto.LobbyWelcome, 500*time.Millisecond)

	b, outB := connect(t, l)
	sendEnvelope(t, l, b.ID, proto.LobbyHello, proto.Hello{Nick: "Same", SchemaChecksum: proto.SchemaChecksum()})
	o, ok := drainUntil(t, outB, proto.LobbyWelcome, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no welcome")
	}
	w := o.Payload.(proto.Welcome)
	if w.Nick == "Same" {
		t.Fatalf("nick was not deduped: %q", w.Nick)
	}
	if w.Nick != "Same#001" {
		t.Fatalf("nick = %q, want Same#001", w.Nick)
	}
}

func TestLobbyCreateAndJoin(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	a, outA := connect(t, l)
	b, outB := connect(t, l)
	helloAndDrain(t, l, a, outA, "A")
	helloAndDrain(t, l, b, outB, "B")

	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "Test", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no roomList with 1 room for A")
	}
	roomID := rl.Rooms[0].ID

	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: roomID})
	rl, ok = drainRoomListWithCount(t, outB, 1, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no roomList for B")
	}
	if rl.Rooms[0].Players != 2 {
		t.Fatalf("players = %d, want 2", rl.Rooms[0].Players)
	}
}

func TestLobbyJoinRoomFullRejects(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "A")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{Name: "R", Max: 2, Level: proto.Level{Letter: "A", Number: 1}})
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no roomList for A")
	}
	roomID := rl.Rooms[0].ID

	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "B")
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: roomID})
	drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)

	c, outC := connect(t, l)
	helloAndDrain(t, l, c, outC, "C")
	sendEnvelope(t, l, c.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: roomID})
	o, ok := drainUntil(t, outC, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no error for full room")
	}
	if o.Payload.(proto.LobbyError).Code != proto.LobbyErrRoomFull {
		t.Fatalf("code = %s", o.Payload.(proto.LobbyError).Code)
	}
}

func TestLobbyStartMatchHostOnly(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	b, outB := connect(t, l)
	helloAndDrain(t, l, a, outA, "A")
	helloAndDrain(t, l, b, outB, "B")

	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1}})
	rl, _ := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	roomID := rl.Rooms[0].ID
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: roomID})
	drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)

	sendEnvelope(t, l, b.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: roomID})
	o, ok := drainUntil(t, outB, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no error for non-host startMatch")
	}
	if o.Payload.(proto.LobbyError).Code != proto.LobbyErrNotHost {
		t.Fatalf("code = %s", o.Payload.(proto.LobbyError).Code)
	}
}

func TestLobbyStartMatchEmitsPerMemberToken(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	b, outB := connect(t, l)
	helloAndDrain(t, l, a, outA, "A")
	helloAndDrain(t, l, b, outB, "B")

	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1}})
	rl, _ := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	roomID := rl.Rooms[0].ID
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: roomID})
	drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)

	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: roomID})
	mA, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no matchStarted for A")
	}
	mB, ok := drainUntil(t, outB, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no matchStarted for B")
	}
	pa := mA.Payload.(proto.MatchStarted)
	pb := mB.Payload.(proto.MatchStarted)
	if pa.MatchID != pb.MatchID {
		t.Fatalf("matchId differs: %s vs %s", pa.MatchID, pb.MatchID)
	}
	if pa.JoinToken == pb.JoinToken {
		t.Fatalf("joinToken not unique per player")
	}
	if pa.TickRate != 30 {
		t.Fatalf("tickRate = %d", pa.TickRate)
	}
}

func TestLobbyAlreadyInRoomRejects(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "A")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1}})
	drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{Name: "R2", Max: 4, Level: proto.Level{Letter: "A", Number: 1}})
	o, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no error")
	}
	if o.Payload.(proto.LobbyError).Code != proto.LobbyErrAlreadyInRoom {
		t.Fatalf("code = %s", o.Payload.(proto.LobbyError).Code)
	}
}

func TestLobbyEnvelopeRejectsWrongVersion(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	s, out := connect(t, l)
	// Hand-build a v=2 envelope.
	b, _ := json.Marshal(proto.Envelope{T: "hello", V: 2, D: json.RawMessage(`{}`)})
	l.PostMessage(s.ID, b)
	o, ok := drainUntil(t, out, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("no error")
	}
	_ = o
}
