package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// expectError posts an envelope and asserts the next error frame carries
// the wanted code.
func expectError(t *testing.T, l *Lobby, sid SessionID, out chan Outbound, tag string, payload any, wantCode string) {
	t.Helper()
	sendEnvelope(t, l, sid, tag, payload)
	o, ok := drainUntil(t, out, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatalf("%s: no error frame", tag)
	}
	e, ok := o.Payload.(proto.LobbyError)
	if !ok {
		t.Fatalf("%s: error payload type %T", tag, o.Payload)
	}
	if e.Code != wantCode {
		t.Fatalf("%s: error code = %q, want %q", tag, e.Code, wantCode)
	}
}

// TestLobby_JoinRoomRejections exercises every guard in handleJoinRoom.
func TestLobby_JoinRoomRejections(t *testing.T) {
	t.Run("hello first", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		s, out := connect(t, l)
		expectError(t, l, s.ID, out, proto.LobbyJoinRoom,
			proto.JoinRoom{RoomID: "x"}, proto.LobbyErrBadRequest)
	})

	t.Run("already in a room", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
			Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
		})
		drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
		expectError(t, l, a.ID, outA, proto.LobbyJoinRoom,
			proto.JoinRoom{RoomID: "anything"}, proto.LobbyErrAlreadyInRoom)
	})

	t.Run("no such room", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		expectError(t, l, a.ID, outA, proto.LobbyJoinRoom,
			proto.JoinRoom{RoomID: "ghost"}, proto.LobbyErrRoomGone)
	})

	t.Run("room full", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		b, outB := connect(t, l)
		helloAndDrain(t, l, b, outB, "Bob")
		c, outC := connect(t, l)
		helloAndDrain(t, l, c, outC, "Carol")
		// Max=2 → host A + B fills it; C is rejected.
		sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
			Name: "R", Max: 2, Level: proto.Level{Letter: "A", Number: 1},
		})
		rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
		if !ok || len(rl.Rooms) == 0 {
			t.Fatal("no roomList after create")
		}
		rid := rl.Rooms[0].ID
		sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
		drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)
		expectError(t, l, c.ID, outC, proto.LobbyJoinRoom,
			proto.JoinRoom{RoomID: rid}, proto.LobbyErrRoomFull)
	})

	t.Run("room not accepting joins", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		b, outB := connect(t, l)
		helloAndDrain(t, l, b, outB, "Bob")
		c, outC := connect(t, l)
		helloAndDrain(t, l, c, outC, "Carol")
		rid := createAndJoinRoom(t, l, a, b, outA, outB)
		// Host starts → room transitions to STARTING; a late joiner is
		// refused with ROOM_GONE ("not accepting joins").
		sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
		if _, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond); !ok {
			t.Fatal("host never received matchStarted; room never reached STARTING")
		}
		expectError(t, l, c.ID, outC, proto.LobbyJoinRoom,
			proto.JoinRoom{RoomID: rid}, proto.LobbyErrRoomGone)
	})
}

// TestLobby_LeaveRoomNotInRoom covers handleLeaveRoom's no-room guard.
func TestLobby_LeaveRoomNotInRoom(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	expectError(t, l, a.ID, outA, proto.LobbyLeaveRoom,
		proto.LeaveRoom{}, proto.LobbyErrNoRoom)
}

// TestLobby_StartMatchRejections exercises handleStartMatch's guards.
func TestLobby_StartMatchRejections(t *testing.T) {
	t.Run("not in a room", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		expectError(t, l, a.ID, outA, proto.LobbyStartMatch,
			proto.StartMatch{RoomID: "x"}, proto.LobbyErrNoRoom)
	})

	t.Run("not host", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		b, outB := connect(t, l)
		helloAndDrain(t, l, b, outB, "Bob")
		rid := createAndJoinRoom(t, l, a, b, outA, outB)
		expectError(t, l, b.ID, outB, proto.LobbyStartMatch,
			proto.StartMatch{RoomID: rid}, proto.LobbyErrNotHost)
	})

	t.Run("too few players", func(t *testing.T) {
		l, _, done, _ := newTestLobby(t)
		defer func() { l.Stop(); <-done }()
		a, outA := connect(t, l)
		helloAndDrain(t, l, a, outA, "Alice")
		sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
			Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
		})
		rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
		if !ok || len(rl.Rooms) == 0 {
			t.Fatal("no roomList after create")
		}
		rid := rl.Rooms[0].ID
		expectError(t, l, a.ID, outA, proto.LobbyStartMatch,
			proto.StartMatch{RoomID: rid}, proto.LobbyErrBadRequest)
	})
}

// TestLobby_MatchEndedClosesRoom drives the ctlMatchEnded control path:
// once a started match reports its end, handleMatchEnded closes the room,
// removes it from the listing, and clears each member's roomID.
func TestLobby_MatchEndedClosesRoom(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	ms, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatal("no matchStarted")
	}
	matchID := ms.Payload.(proto.MatchStarted).MatchID

	// Deliver the match-ended control message through the actor.
	l.in <- ctlMatchEnded{MatchID: matchID}

	if _, exists := snapshotRooms(t, l)[rid]; exists {
		t.Fatalf("room %s still listed after match ended", rid)
	}
	// The members' roomID is cleared: A can no longer leave the (gone) room.
	expectError(t, l, a.ID, outA, proto.LobbyLeaveRoom,
		proto.LeaveRoom{}, proto.LobbyErrNoRoom)
}

// TestLobby_DisconnectDuringMatchKeepsRoom: a member dropping their lobby
// WS while the room is STARTING leaves the room intact — the match
// lifecycle owns teardown (handleDisconnect's non-open branch).
func TestLobby_DisconnectDuringMatchKeepsRoom(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	if _, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond); !ok {
		t.Fatal("no matchStarted; room never reached STARTING")
	}
	// Confirm the room is in the non-open STARTING state BEFORE the
	// disconnect, so this test genuinely exercises handleDisconnect's
	// non-open branch rather than the open-room removeFromRoom path.
	if st := snapshotRooms(t, l)[rid].State; st != "STARTING" {
		t.Fatalf("room state = %q before disconnect, want STARTING", st)
	}

	// Host drops their lobby connection mid-match.
	l.Disconnect(a.ID)
	// Unknown-session disconnect is a no-op (covers the early return).
	l.Disconnect(SessionID("does-not-exist"))

	if _, exists := snapshotRooms(t, l)[rid]; !exists {
		t.Fatalf("room %s GC'd on mid-match disconnect; expected it to persist", rid)
	}
}

// TestLobby_ConnectAfterStop: Connect on a stopped lobby returns nil and
// closes the provided out channel.
func TestLobby_ConnectAfterStop(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	l.Stop()
	<-done
	out := make(chan Outbound, 1)
	if s := l.Connect(out); s != nil {
		t.Fatalf("Connect after Stop = %v, want nil", s)
	}
	if _, ok := <-out; ok {
		t.Fatal("out channel not closed after Connect-on-stopped")
	}
}

// TestLobby_DropSessionFreesOpenRoomSlot is the regression test for the
// phantom-member bug: a client force-dropped (outbound queue overflow)
// while it is a member of an OPEN room must have its slot freed, exactly
// as an explicit disconnect would. Before the fix, dropSession deleted the
// session from l.sessions but left it in room.Members, so the slot stayed
// occupied and sweepEmptyRooms could never GC the room.
func TestLobby_DropSessionFreesOpenRoomSlot(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	// Host A creates a 4-player room.
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok || len(rl.Rooms) == 0 {
		t.Fatal("no roomList after create")
	}
	rid := rl.Rooms[0].ID

	// B joins → the room now has two members.
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
	if _, ok := drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond); !ok {
		t.Fatal("B never saw its join confirmation")
	}

	// D is a healthy connected observer; its initial roomList shows the
	// room with two members. After B is dropped, D must receive a corrected
	// roomList (the deferred resync), proving the freed slot reaches live
	// clients — not just a later snapshot probe.
	d, outD := connect(t, l)
	helloAndDrain(t, l, d, outD, "Dave")

	if got := snapshotRooms(t, l)[rid].Players; got != 2 {
		t.Fatalf("room players = %d before drop, want 2", got)
	}

	// Quiesce B's queue, then saturate it so the next broadcast to B
	// overflows. snapshotRooms above round-tripped through the actor, so
	// every send queued for B has already landed in outB by now.
	for draining := true; draining; {
		select {
		case <-outB:
		case <-time.After(100 * time.Millisecond):
			draining = false
		}
	}
	for i := 0; i < cap(outB); i++ {
		outB <- Outbound{}
	}

	// A second client creates a room; the room_added delta is broadcast to
	// every session, including the saturated B → send(B) overflows →
	// dropSession(B) runs while B is still a room member.
	c, outC := connect(t, l)
	helloAndDrain(t, l, c, outC, "Carol")
	sendEnvelope(t, l, c.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R2", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})

	select {
	case <-b.closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("B was not dropped on full queue")
	}

	// The healthy observer D receives the corrected room-list resync
	// showing the freed slot (Players == 1).
	deadline := time.Now().Add(500 * time.Millisecond)
	got := -1
	for time.Now().Before(deadline) {
		o, ok := drainUntil(t, outD, proto.LobbyRoomList, 500*time.Millisecond)
		if !ok {
			break
		}
		for _, r := range o.Payload.(proto.RoomList).Rooms {
			if r.ID == rid {
				got = r.Players
			}
		}
		if got == 1 {
			break
		}
	}
	if got != 1 {
		t.Fatalf("observer D saw room players = %d after drop, want 1 (phantom member not removed)", got)
	}

	// And a fresh snapshot agrees: the room is back to a single member.
	if p := snapshotRooms(t, l)[rid].Players; p != 1 {
		t.Fatalf("snapshot room players = %d after drop, want 1", p)
	}
}

// TestLobby_SendDropsOnFullQueue: when a session's outbound queue is full,
// send drops the session (closes closed + out). A 1-slot queue overflows on
// the second frame of the hello reply (welcome, then roomList).
func TestLobby_SendDropsOnFullQueue(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	out := make(chan Outbound, 1)
	s := l.Connect(out)
	if s == nil {
		t.Fatal("Connect returned nil")
	}
	// We never drain `out`; the hello reply is welcome + roomList = 2 sends,
	// so the second send finds the queue full and drops the session.
	sendEnvelope(t, l, s.ID, proto.LobbyHello, proto.Hello{
		Nick: "Alice", SchemaChecksum: proto.SchemaChecksum(),
	})
	select {
	case <-s.closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("session was not dropped on full queue")
	}
}
