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
	// Connect each mock session with a LARGE outbound buffer. The lobby's
	// send() is non-blocking and force-drops a session whose out channel is
	// full (dropSession), which also frees its OPEN room. Under the create
	// storm below — 200 creates each fanning a room_added delta to all 200
	// sessions — the default cap-16 channel overflows whenever a reader
	// goroutine is briefly starved, so a host gets dropped and ITS room
	// vanishes: that is the real cause of the historical "got 199 rooms"
	// flake (not slowness). A buffer wider than the worst-case burst
	// (≈N deltas + a few resyncs) means no session is ever dropped, so all
	// 200 rooms survive deterministically regardless of scheduling.
	const sessBuf = 4096
	sessions := make([]*Session, N)
	outs := make([]chan Outbound, N)
	for i := 0; i < N; i++ {
		out := make(chan Outbound, sessBuf)
		sessions[i] = l.Connect(out)
		outs[i] = out
		helloAndDrain(t, l, sessions[i], outs[i], fmt.Sprintf("U%d", i))
	}

	// Fire 200 createRooms in parallel AND keep draining each session so the
	// buffers (already burst-sized) never approach full even under load.
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

	// Read the room count DETERMINISTICALLY rather than polling on a wall clock.
	// PostMessage is a synchronous enqueue, so after wg.Wait() all 200 createRoom
	// messages are queued ahead of anything we send next. A fresh probe's Hello is
	// therefore FIFO-ordered AFTER every create, so the RoomList the actor sends in
	// reply is the full 200-room snapshot — no timing race, no poll loop. The probe
	// gets a large buffer too so its reply is never dropped.
	probeOut := make(chan Outbound, sessBuf)
	probe := l.Connect(probeOut)
	sendEnvelope(t, l, probe.ID, proto.LobbyHello, proto.Hello{
		Nick: "CountProbe", SchemaChecksum: proto.SchemaChecksum(),
	})
	if _, ok := drainUntil(t, probeOut, proto.LobbyWelcome, 30*time.Second); !ok {
		t.Fatal("probe never received Welcome")
	}
	o, ok := drainUntil(t, probeOut, proto.LobbyRoomList, 30*time.Second)
	if !ok {
		t.Fatal("probe never received RoomList snapshot")
	}
	rl := o.Payload.(proto.RoomList)
	rooms := make(map[string]proto.RoomDescriptor, len(rl.Rooms))
	for _, r := range rl.Rooms {
		rooms[r.ID] = r
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
