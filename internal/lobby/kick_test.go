package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/match"
	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_KickHost — DoD #8. Host A kicks B; B receives `kicked`
// and is removed from the room; the room shows the freed slot.
func TestLobby_KickHost(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	sendEnvelope(t, l, a.ID, proto.LobbyKick, proto.LobbyKickPayload{
		RoomID: rid, SessionID: string(b.ID),
	})
	got, ok := drainUntil(t, outB, proto.LobbyKicked, 500*time.Millisecond)
	if !ok {
		t.Fatal("B did not receive kicked envelope")
	}
	if got.Payload.(proto.LobbyKickedPayload).RoomID != rid {
		t.Fatalf("kicked payload roomId mismatch: %+v", got.Payload)
	}
	// A's subsequent roomList shows the freed slot (1 player).
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok {
		t.Fatal("no updated roomList after kick")
	}
	if rl.Rooms[0].Players != 1 {
		t.Fatalf("roomList Players=%d, want 1 after kick", rl.Rooms[0].Players)
	}
}

// TestLobby_KickHost_NonHostRejected — DoD #9. Non-host calls kick
// → NOT_HOST.
func TestLobby_KickHost_NonHostRejected(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	// B (non-host) tries to kick A.
	sendEnvelope(t, l, b.ID, proto.LobbyKick, proto.LobbyKickPayload{
		RoomID: rid, SessionID: string(a.ID),
	})
	got, ok := drainUntil(t, outB, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	if got.Payload.(proto.LobbyError).Code != proto.LobbyErrNotHost {
		t.Fatalf("code = %q, want NOT_HOST", got.Payload.(proto.LobbyError).Code)
	}
}

// TestLobby_KickHost_SelfKickRejected — host kicks self → BAD_REQUEST.
func TestLobby_KickHost_SelfKickRejected(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	sendEnvelope(t, l, a.ID, proto.LobbyKick, proto.LobbyKickPayload{
		RoomID: rid, SessionID: string(a.ID),
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	if got.Payload.(proto.LobbyError).Code != proto.LobbyErrBadRequest {
		t.Fatalf("code = %q, want BAD_REQUEST", got.Payload.(proto.LobbyError).Code)
	}
}

// TestLobby_KickInvalidatesJoinToken — Phase 6 §7.3 + codex iter2/3
// finding. After startMatch issues tokens, a kick on a member must
// revoke that member's token at the match actor (not just lobby
// bookkeeping); subsequent SubmitJoin with the revoked token returns
// ErrAuth.
func TestLobby_KickInvalidatesJoinToken(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	// Host starts the match — both members get matchStarted with tokens.
	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	msA, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatal("A no matchStarted")
	}
	msB, ok := drainUntil(t, outB, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatal("B no matchStarted")
	}
	matchID := msA.Payload.(proto.MatchStarted).MatchID
	bToken := msB.Payload.(proto.MatchStarted).JoinToken

	// Host kicks B. Token should be revoked at the match actor.
	sendEnvelope(t, l, a.ID, proto.LobbyKick, proto.LobbyKickPayload{
		RoomID: rid, SessionID: string(b.ID),
	})
	if _, ok := drainUntil(t, outB, proto.LobbyKicked, 500*time.Millisecond); !ok {
		t.Fatal("B no kicked envelope")
	}
	if l.cfg.Registry == nil {
		t.Fatal("lobby has no registry")
	}
	mm, ok := l.cfg.Registry.Lookup(matchID)
	if !ok || mm == nil {
		t.Fatalf("match %s missing from registry", matchID)
	}
	// Some slack so the actor processes ctlRevokeToken before SubmitJoin.
	time.Sleep(50 * time.Millisecond)
	matchOut := make(chan match.OutboundFrame, 16)
	if _, err := mm.SubmitJoin(bToken, matchOut); err == nil {
		t.Fatal("SubmitJoin with revoked token succeeded; want ErrAuth")
	}
}

// TestLobby_KickHost_UnknownTarget — target not in room → NOT_IN_ROOM.
func TestLobby_KickHost_UnknownTarget(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	_ = b
	_ = outB
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, _ := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	rid := rl.Rooms[0].ID
	// A kicks B (not in room).
	sendEnvelope(t, l, a.ID, proto.LobbyKick, proto.LobbyKickPayload{
		RoomID: rid, SessionID: string(b.ID),
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	if got.Payload.(proto.LobbyError).Code != proto.LobbyErrNotInRoom {
		t.Fatalf("code = %q, want NOT_IN_ROOM", got.Payload.(proto.LobbyError).Code)
	}
}
