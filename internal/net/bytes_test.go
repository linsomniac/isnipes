package wsnet_test

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
)

// TestNet_ByteCounters — the match path increments the OnBytesIn/OnBytesOut
// hooks (the production producers behind isnipes_bytes_*_total). (DoD #8
// bytes producer)
func TestNet_ByteCounters(t *testing.T) {
	var in, out atomic.Int64
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 4})
	lob := lobby.NewLobby(lobby.Config{Registry: reg})
	done := make(chan struct{})
	go func() { lob.Run(); close(done) }()
	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:         lob,
		MatchRegistry: reg,
		ServerVersion: "bytes-test",
		OnBytesIn:     func(n int) { in.Add(int64(n)) },
		OnBytesOut:    func(n int) { out.Add(int64(n)) },
	})
	ts := httptest.NewServer(srv.Handler())
	defer func() { ts.Close(); lob.Stop(); <-done }()

	matchID, token, _ := joinMatchSetup(t, reg, lob)
	c, cancel := dialMatch(t, ts, matchID)
	defer cancel()
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	sendMatchJoin(t, c, ctx, token) // inbound bytes
	// Read a couple of server frames (MapInit/Snapshot/...) → outbound bytes.
	for i := 0; i < 2; i++ {
		if _, _, ok := recvFrame(t, c, ctx, 2*time.Second); !ok {
			break
		}
	}

	if in.Load() == 0 {
		t.Fatal("OnBytesIn never fired (inbound match bytes not counted)")
	}
	if out.Load() == 0 {
		t.Fatal("OnBytesOut never fired (outbound match bytes not counted)")
	}
}
