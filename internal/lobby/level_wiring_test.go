package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_StartMatchPropagatesRoomLevel is a regression guard for the
// bug where handleStartMatch built the MatchConfig without the room's
// selected level. With LevelLetter left at 0 the match ran as Phase-2
// PvP-only (NoGenerators set in startOrAbort), so no generators — and
// therefore none of the snipes generators emit — ever spawned.
//
// The host picks a distinctive, non-default level (C5) so the assertion
// proves the *selected* level flows all the way into the match, rather
// than merely some non-zero fallback.
func TestLobby_StartMatchPropagatesRoomLevel(t *testing.T) {
	l, _, done, reg := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")

	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "PvE", Max: 4, Level: proto.Level{Letter: "C", Number: 5},
	})
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok || len(rl.Rooms) != 1 {
		t.Fatal("no roomList after create")
	}
	rid := rl.Rooms[0].ID

	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
	drainUntil(t, outA, proto.LobbyRoomList, 500*time.Millisecond)
	drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)

	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	ms, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatal("host never received matchStarted")
	}
	matchID := ms.Payload.(proto.MatchStarted).MatchID

	m, ok := reg.Lookup(matchID)
	if !ok {
		t.Fatalf("match %q not found in registry", matchID)
	}
	if got := m.LevelLetter(); got != 'C' {
		t.Errorf("match LevelLetter = %q, want 'C' — room level was dropped", got)
	}
	if got := m.LevelNumber(); got != 5 {
		t.Errorf("match LevelNumber = %d, want 5 — room level was dropped", got)
	}
}
