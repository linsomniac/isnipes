package lobby

import (
	"strings"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// helper: complete the create-then-join handshake for A as host and
// B as joiner. Returns the room ID. Drains setup frames.
func createAndJoinRoom(t *testing.T, l *Lobby, a, b *Session, outA, outB chan Outbound) string {
	t.Helper()
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "ChatRoom", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, ok := drainRoomListWithCount(t, outA, 1, 500*time.Millisecond)
	if !ok || len(rl.Rooms) != 1 {
		t.Fatal("no roomList after create")
	}
	rid := rl.Rooms[0].ID
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
	// Drain both sides' subsequent roomList updates.
	drainUntil(t, outA, proto.LobbyRoomList, 500*time.Millisecond)
	drainUntil(t, outB, proto.LobbyRoomList, 500*time.Millisecond)
	return rid
}

// TestLobby_ChatBroadcastToRoom — DoD #7. A creates room, B joins, A
// sends `chat`; B receives `chat_relay`. A also receives the echo.
func TestLobby_ChatBroadcastToRoom(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: rid, Text: "hello world",
	})
	// B receives chat_relay.
	got, ok := drainUntil(t, outB, proto.LobbyChatRelay, 500*time.Millisecond)
	if !ok {
		t.Fatal("B did not receive chat_relay")
	}
	relay := got.Payload.(proto.LobbyChatRelayPayload)
	if relay.FromNick != "Alice" || relay.Text != "hello world" || relay.RoomID != rid {
		t.Fatalf("relay = %+v", relay)
	}
	// A also receives the echo.
	if _, ok := drainUntil(t, outA, proto.LobbyChatRelay, 500*time.Millisecond); !ok {
		t.Fatal("A did not receive own echo")
	}
}

// TestLobby_ChatRequiresMembership — sender not in the named room →
// NOT_IN_ROOM.
func TestLobby_ChatRequiresMembership(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	// Alice is in no room; she chats anyway.
	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: "DOESNTEXIST", Text: "hi",
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	er := got.Payload.(proto.LobbyError)
	if er.Code != proto.LobbyErrNotInRoom {
		t.Fatalf("code = %q, want NOT_IN_ROOM", er.Code)
	}
}

// TestLobby_ChatRejectsEmpty — empty after trim → BAD_REQUEST.
func TestLobby_ChatRejectsEmpty(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: rid, Text: "   \t\n  ",
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	if got.Payload.(proto.LobbyError).Code != proto.LobbyErrBadRequest {
		t.Fatalf("code = %q", got.Payload.(proto.LobbyError).Code)
	}
}

// TestLobby_ChatRejectsOversize — 1025-byte text → TEXT_TOO_LONG.
func TestLobby_ChatRejectsOversize(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	big := strings.Repeat("x", 1025)
	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: rid, Text: big,
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	if got.Payload.(proto.LobbyError).Code != proto.LobbyErrTextTooLong {
		t.Fatalf("code = %q", got.Payload.(proto.LobbyError).Code)
	}
}

// TestLobby_ChatRateLimit — 5th message in burst → RATE_LIMITED.
func TestLobby_ChatRateLimit(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)

	// Burst 4 — all accepted.
	for i := 0; i < 4; i++ {
		sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
			RoomID: rid, Text: "msg",
		})
	}
	// 5th — must be rate-limited.
	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: rid, Text: "throttle",
	})
	got, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("no error received")
	}
	er := got.Payload.(proto.LobbyError)
	if er.Code != proto.LobbyErrRateLimited {
		t.Fatalf("code = %q, want RATE_LIMITED", er.Code)
	}
}

// TestLobby_ChatTrimsWhitespace — leading/trailing whitespace removed.
func TestLobby_ChatTrimsWhitespace(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyChat, proto.LobbyChatPayload{
		RoomID: rid, Text: "  trimmed  ",
	})
	got, ok := drainUntil(t, outB, proto.LobbyChatRelay, 500*time.Millisecond)
	if !ok {
		t.Fatal("no chat_relay")
	}
	if got.Payload.(proto.LobbyChatRelayPayload).Text != "trimmed" {
		t.Fatalf("text = %q, want %q", got.Payload.(proto.LobbyChatRelayPayload).Text, "trimmed")
	}
}
