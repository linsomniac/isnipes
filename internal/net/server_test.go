package wsnet_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/proto"
)

// newTestServer spins up a Server backed by a real lobby + match registry.
func newTestServer(t *testing.T) (*httptest.Server, *lobby.Lobby, *match.Registry, func()) {
	t.Helper()
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 4})
	l := lobby.NewLobby(lobby.Config{Registry: reg})
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:         l,
		MatchRegistry: reg,
		ServerVersion: "test",
	})
	ts := httptest.NewServer(srv.Handler())
	cleanup := func() {
		ts.Close()
		l.Stop()
		<-done
	}
	return ts, l, reg, cleanup
}

func TestServerServesHealthz(t *testing.T) {
	ts, _, _, cleanup := newTestServer(t)
	defer cleanup()
	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("body = %q", b)
	}
}

func TestServerServesVersion(t *testing.T) {
	ts, _, _, cleanup := newTestServer(t)
	defer cleanup()
	resp, err := ts.Client().Get(ts.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v["server"] != "test" {
		t.Fatalf("server = %v", v["server"])
	}
	if !strings.HasPrefix(v["schemaChecksum"].(string), "0x") {
		t.Fatalf("checksum = %v", v["schemaChecksum"])
	}
}

func TestServerLobbyRoundTrip(t *testing.T) {
	ts, _, _, cleanup := newTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws/lobby"
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	helloBytes, err := proto.EncodeLobbyMessage(proto.LobbyHello, proto.Hello{
		Nick: "Alice", ClientVersion: "v0", SchemaChecksum: proto.SchemaChecksum(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		t.Fatal(err)
	}
	// Read welcome.
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	env, err := proto.DecodeLobbyEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	if env.T != proto.LobbyWelcome {
		t.Fatalf("got tag %s", env.T)
	}
}

// TestNet_LobbyStaysAliveWhileIdle is the regression test for the
// "WebSocket is already in a closing or closed state" bug: a lobby client
// that connects and then sits idle (a player reading the lobby screen, who
// sends no messages) must NOT be dropped. The server's ping keepalive both
// holds the WS open and refreshes the lobby session's idle timer (via
// Lobby.Touch), so a later action (createRoom) still succeeds.
func TestNet_LobbyStaysAliveWhileIdle(t *testing.T) {
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 4})
	// Aggressively short idle window + janitor so the test is fast: without
	// the keepalive, the actor reaps the session within ~200ms.
	l := lobby.NewLobby(lobby.Config{
		Registry:        reg,
		IdleTimeout:     200 * time.Millisecond,
		JanitorInterval: 40 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() { l.Run(); close(done) }()
	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:             l,
		MatchRegistry:     reg,
		LobbyPingInterval: 40 * time.Millisecond,
		ServerVersion:     "test",
	})
	ts := httptest.NewServer(srv.Handler())
	defer func() { ts.Close(); l.Stop(); <-done }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL(ts)+"/ws/lobby", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	// Background reader: nhooyr processes incoming pings (auto-pong) only
	// during a Read, so we must keep reading for the keepalive to work.
	tags := make(chan string, 32)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			if env, err := proto.DecodeLobbyEnvelope(data); err == nil {
				tags <- env.T
			}
		}
	}()

	helloBytes, err := proto.EncodeLobbyMessage(proto.LobbyHello, proto.Hello{
		Nick: "Alice", ClientVersion: "v0", SchemaChecksum: proto.SchemaChecksum(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, helloBytes); err != nil {
		t.Fatal(err)
	}
	waitForTag(t, tags, readErr, proto.LobbyWelcome)

	// Idle far longer than IdleTimeout (≈5×) with no client messages.
	time.Sleep(1 * time.Second)
	select {
	case err := <-readErr:
		t.Fatalf("lobby socket closed while idle (keepalive regressed): %v", err)
	default:
	}

	// The session must still be live: a createRoom now succeeds.
	createBytes, err := proto.EncodeLobbyMessage(proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "r", Max: 2, Level: proto.Level{Letter: "A", Number: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, createBytes); err != nil {
		t.Fatalf("write createRoom after idle: %v", err)
	}
	waitForTag(t, tags, readErr, proto.LobbyRoomAdded)
}

// waitForTag blocks until the wanted lobby tag arrives, failing on a read
// error or a 3s timeout.
func waitForTag(t *testing.T, tags <-chan string, readErr <-chan error, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case tag := <-tags:
			if tag == want {
				return
			}
		case err := <-readErr:
			t.Fatalf("read error while waiting for %q: %v", want, err)
		case <-deadline:
			t.Fatalf("timed out waiting for lobby tag %q", want)
		}
	}
}

func TestServerMatchNotFound(t *testing.T) {
	ts, _, _, cleanup := newTestServer(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws/match/bogus"
	_, resp, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatalf("dial succeeded")
	}
	if resp != nil && resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
