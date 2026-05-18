package lobby

import (
	"sync"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/match"
	"github.com/jafo/isnipes/internal/proto"
)

// newFastGCLobby returns a lobby with a 5ms janitor cadence and a
// 10ms inactivity threshold so the GC sweepers can fire within a few
// real-time sleeps. Uses time.Now (not fakeClock) so durations
// genuinely elapse.
func newFastGCLobby(t *testing.T) (*Lobby, chan struct{}, *match.Registry) {
	t.Helper()
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 16})
	l := NewLobby(Config{
		Registry:              reg,
		JanitorInterval:       5 * time.Millisecond,
		GCInactivityThreshold: 10 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	return l, done, reg
}

// TestLobby_RoomGCAfterInactivity — DoD #11. A single-member room
// emptied via the host's leaveRoom (with no remaining members to
// transfer host to) lingers with EmptySince stamped; the §10.1 sweep
// GCs it after the inactivity threshold.
func TestLobby_RoomGCAfterInactivity(t *testing.T) {
	l, done, _ := newFastGCLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, _ := drainRoomListWithCount(t, outA, 1, 200*time.Millisecond)
	rid := rl.Rooms[0].ID
	// A leaves the (sole-member) room. The §7.4 host-transfer path is
	// skipped because there are no other members; the room is left
	// empty with EmptySince stamped. The §10.1 sweep GCs after the
	// 10 ms threshold.
	sendEnvelope(t, l, a.ID, proto.LobbyLeaveRoom, proto.LeaveRoom{})
	// Wait long enough for ≥ 2 sweeps after the threshold elapses.
	time.Sleep(80 * time.Millisecond)
	if _, exists := snapshotRooms(t, l)[rid]; exists {
		t.Fatalf("room %s still present after GC", rid)
	}
}

// TestLobby_HostTransferOnLeave — §7.4. Host leaves a room with ≥ 1
// remaining member; the oldest-joined member becomes the new host;
// room remains open.
func TestLobby_HostTransferOnLeave(t *testing.T) {
	l, done, _ := newFastGCLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyLeaveRoom, proto.LeaveRoom{})
	// Drain post-leave envelopes.
	time.Sleep(30 * time.Millisecond)
	rooms := snapshotRooms(t, l)
	r, ok := rooms[rid]
	if !ok {
		t.Fatalf("room %s GC'd; expected host-transfer to keep it alive", rid)
	}
	if r.Players != 1 {
		t.Fatalf("Players = %d, want 1", r.Players)
	}
}

// snapshotRooms reads the lobby's room map via a synthesized hello
// request: we send a fresh hello on a brand-new session and inspect
// the returned roomList. This serialises the read through the actor.
func snapshotRooms(t *testing.T, l *Lobby) map[string]proto.RoomDescriptor {
	t.Helper()
	x, outX := connect(t, l)
	sendEnvelope(t, l, x.ID, proto.LobbyHello, proto.Hello{
		Nick: "Probe", SchemaChecksum: proto.SchemaChecksum(),
	})
	drainUntil(t, outX, proto.LobbyWelcome, 200*time.Millisecond)
	o, ok := drainUntil(t, outX, proto.LobbyRoomList, 200*time.Millisecond)
	if !ok {
		return nil
	}
	rl := o.Payload.(proto.RoomList)
	out := make(map[string]proto.RoomDescriptor, len(rl.Rooms))
	for _, r := range rl.Rooms {
		out[r.ID] = r
	}
	// Disconnect the probe.
	l.Disconnect(x.ID)
	return out
}

// waitedSetup2 connects two helper sessions. Returns the second session's
// outbound channel.
func waitedSetup2(t *testing.T, l *Lobby) (*Session, chan Outbound) {
	t.Helper()
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	return b, outB
}

// TestLobby_MatchEndsWithZeroPlayers — DoD #12. After startMatch
// creates a match, if no player MatchJoins within the threshold the
// match is aborted by sweepZeroPlayerMatches.
func TestLobby_MatchEndsWithZeroPlayers(t *testing.T) {
	l, done, reg := newFastGCLobby(t)
	defer func() { l.Stop(); <-done }()
	_ = reg
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	// Both join a room then host starts. Neither WS-binds to the match.
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "R", Max: 4, Level: proto.Level{Letter: "A", Number: 1},
	})
	rl, _ := drainRoomListWithCount(t, outA, 1, 200*time.Millisecond)
	rid := rl.Rooms[0].ID
	sendEnvelope(t, l, b.ID, proto.LobbyJoinRoom, proto.JoinRoom{RoomID: rid})
	drainUntil(t, outB, proto.LobbyRoomList, 200*time.Millisecond)
	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	mst, _ := drainUntil(t, outA, proto.LobbyMatchStarted, 200*time.Millisecond)
	matchID := mst.Payload.(proto.MatchStarted).MatchID
	mm, ok := reg.Lookup(matchID)
	if !ok {
		t.Fatal("match missing from registry")
	}
	// Wait long enough for sweep to mark + abort.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if mm.State() == match.StateEnded {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("match did not abort within deadline (state=%v)", mm.State())
}

// Suppress unused-import warning when only some tests use sync.
var _ = sync.Mutex{}
