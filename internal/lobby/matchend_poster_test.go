package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_MatchEndedPoster: the public MatchEnded poster drives the same
// cleanup as a raw ctlMatchEnded — the room is removed and each member's
// roomID cleared — proving the production caller (the registry OnMatchEnded
// hook) reaches handleMatchEnded without touching l.in directly.
func TestLobby_MatchEndedPoster(t *testing.T) {
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

	// Public poster (what the registry hook calls), not l.in <- … directly.
	l.MatchEnded(matchID)

	// snapshotRooms enqueues its hello behind the ctlMatchEnded posted just
	// above on the same buffered l.in channel; FIFO delivery guarantees the
	// MatchEnded is drained (and the room cleaned up) before the probe's
	// roomList is built. This ordering rests on the channel's FIFO semantics,
	// not on any explicit sync primitive.
	if _, exists := snapshotRooms(t, l)[rid]; exists {
		t.Fatalf("room %s still listed after MatchEnded", rid)
	}
	// roomID cleared: A can no longer leave the (gone) room.
	expectError(t, l, a.ID, outA, proto.LobbyLeaveRoom,
		proto.LeaveRoom{}, proto.LobbyErrNoRoom)
}

// TestLobby_MatchEndedAfterStop: MatchEnded on a stopped lobby is a no-op
// (no panic, no blocking send on the drained actor). Time-boxed so a
// regression that made the call block fails cleanly instead of hanging the
// suite.
func TestLobby_MatchEndedAfterStop(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	l.Stop()
	<-done
	returned := make(chan struct{})
	go func() {
		l.MatchEnded("whatever") // must not panic or block
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("MatchEnded blocked after Stop")
	}
}
