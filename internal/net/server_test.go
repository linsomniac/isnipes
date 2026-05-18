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
