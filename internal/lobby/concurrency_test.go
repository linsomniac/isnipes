package lobby

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_200ConcurrentRoomsNoPanic — DoD #14. 200 mock sessions
// each issue createRoom simultaneously. Actor drains the inbox
// sequentially; no panic; exactly 200 distinct rooms with unique IDs.
func TestLobby_200ConcurrentRoomsNoPanic(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()

	const N = 200
	sessions := make([]*Session, N)
	outs := make([]chan Outbound, N)
	for i := 0; i < N; i++ {
		sessions[i], outs[i] = connect(t, l)
		helloAndDrain(t, l, sessions[i], outs[i], fmt.Sprintf("U%d", i))
	}

	// Fire 200 createRooms in parallel AND start a session-out drainer
	// per session so the actor's broadcastRoomDelta does not stall on
	// any one session's full out channel. (lobby's send is non-blocking
	// but if probes / readers later run, frames queue up.)
	stopDrain := make(chan struct{})
	for i := 0; i < N; i++ {
		go func(i int) {
			for {
				select {
				case <-stopDrain:
					return
				case <-outs[i]:
				}
			}
		}(i)
	}
	defer close(stopDrain)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sendEnvelope(t, l, sessions[i].ID, proto.LobbyCreateRoom, proto.CreateRoom{
				Name:  fmt.Sprintf("R%d", i),
				Max:   4,
				Level: proto.Level{Letter: "A", Number: 1},
			})
		}(i)
	}
	wg.Wait()

	// The actor drains the inbox sequentially. Per createRoom the
	// actor also broadcasts a room_added to every connected session
	// — that's 200 × 200 = 40 000 send ops. Most session out channels
	// (cap 16) fill and the lobby silently drops frames per its
	// `default:` branch; the rooms themselves stay in l.rooms.
	// We give it generous time then snapshot once via a probe.
	deadline := time.Now().Add(15 * time.Second)
	var rooms map[string]proto.RoomDescriptor
	for time.Now().Before(deadline) {
		rooms = snapshotRooms(t, l)
		if len(rooms) >= N {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(rooms) != N {
		t.Fatalf("got %d rooms, want %d", len(rooms), N)
	}
	// Every ID is unique by construction (map keys).
	// Every ID is 6 chars of base32 per §6.3.
	for id := range rooms {
		if len(id) != 6 {
			t.Errorf("room ID %q is not 6 chars", id)
		}
	}
}
