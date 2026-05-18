package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_RoomDelta_AddedOnCreate — every session receives a
// `room_added` delta when a new room is created.
func TestLobby_RoomDelta_AddedOnCreate(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R1", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	// B (not the creator) should receive a room_added envelope.
	got, ok := drainUntil(t, outB, proto.LobbyRoomAdded, 500*time.Millisecond)
	if !ok {
		t.Fatal("B did not receive room_added")
	}
	delta := got.Payload.(proto.LobbyRoomDeltaPayload)
	if delta.Room.Name != "R1" {
		t.Fatalf("delta room name = %q, want R1", delta.Room.Name)
	}
}

// TestLobby_RoomDelta_UpdatedOnJoin — a member joining produces a
// `room_updated` delta to every session.
func TestLobby_RoomDelta_UpdatedOnJoin(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R1", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, _ := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	rid := rl.Rooms[0].ID
	// Drain the room_added for outA.
	drainUntil(t, outA, proto.LobbyRoomAdded, 500*time.Millisecond)
	drainUntil(t, outB, proto.LobbyRoomAdded, 500*time.Millisecond)

	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
	got, ok := drainUntil(t, outA, proto.LobbyRoomUpdated, 500*time.Millisecond)
	if !ok {
		t.Fatal("A did not receive room_updated")
	}
	delta := got.Payload.(proto.LobbyRoomDeltaPayload)
	if delta.Room.Players != 2 {
		t.Fatalf("delta Players = %d, want 2", delta.Room.Players)
	}
}

// TestLobby_RoomDelta_RemovedOnSoloLeave — when the only member of
// a room leaves, the §10.1 GC sweep eventually removes the room via
// a `room_removed` delta. With the default 10s janitor this is slow;
// here we rely on the lazy delete path via host-leave on a 1-member
// room: the room is left empty, and the next sweep removes it. We
// use newFastGCLobby for a 5ms cadence so the assertion fits a unit
// test budget.
func TestLobby_RoomDelta_RemovedOnSoloLeave(t *testing.T) {
	l, done, _ := newFastGCLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, _ := drainRoomListWithCount(t, outA, 1, 200*time.Millisecond)
	rid := rl.Rooms[0].ID
	// Drain the room_added.
	drainUntil(t, outA, proto.LobbyRoomAdded, 200*time.Millisecond)

	// Sole member leaves → room becomes empty; sweep emits room_removed.
	sendEnvelope(t, l, a.ID, proto.LobbyLeaveRoom, proto.LeaveRoom{})
	got, ok := drainUntil(t, outA, proto.LobbyRoomRemoved, 200*time.Millisecond)
	if !ok {
		t.Fatal("no room_removed within 200ms")
	}
	removed := got.Payload.(proto.LobbyRoomRemovedPayload)
	if removed.RoomID != rid {
		t.Fatalf("removed RoomID = %q, want %q", removed.RoomID, rid)
	}
}

// TestLobby_HostLeaveDoesNotImmediatelyRemove — with §7.4 host
// transfer in effect, the host leaving a multi-member room keeps the
// room alive with the next-joined member as the new host.
func TestLobby_HostLeaveDoesNotImmediatelyRemove(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	// Drain any pending deltas from the create+join sequence.
	for i := 0; i < 4; i++ {
		drainUntil(t, outB, proto.LobbyRoomUpdated, 50*time.Millisecond)
	}
	sendEnvelope(t, l, a.ID, proto.LobbyLeaveRoom, proto.LeaveRoom{})
	// After leave: the very next room_updated for B reflects Players=1.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, ok := drainUntil(t, outB, proto.LobbyRoomUpdated, 100*time.Millisecond)
		if !ok {
			break
		}
		delta := got.Payload.(proto.LobbyRoomDeltaPayload)
		if delta.Room.ID == rid && delta.Room.Players == 1 {
			return
		}
	}
	t.Fatal("did not observe room_updated with Players=1 after host leave")
}

// TestLobby_RoomListLiveUpdatesAfter1Tick — DoD #13. A creates a
// room; B receives room_added within ~10 ms.
func TestLobby_RoomListLiveUpdatesAfter1Tick(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")

	start := time.Now()
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "Live", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	if _, ok := drainUntil(t, outB, proto.LobbyRoomAdded, 100*time.Millisecond); !ok {
		t.Fatal("room_added not seen within 100 ms")
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("room_added latency %v > 100 ms", elapsed)
	}
}
