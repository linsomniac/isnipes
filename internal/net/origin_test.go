package wsnet_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
)

// originServer builds a server with the given origin policy.
func originServer(t *testing.T, allowed []string, insecure bool) (*httptest.Server, func()) {
	t.Helper()
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 2})
	lob := lobby.NewLobby(lobby.Config{Registry: reg})
	done := make(chan struct{})
	go func() { lob.Run(); close(done) }()
	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:          lob,
		MatchRegistry:  reg,
		ServerVersion:  "origin-test",
		AllowedOrigins: allowed,
		InsecureOrigin: insecure,
	})
	ts := httptest.NewServer(srv.Handler())
	return ts, func() { ts.Close(); lob.Stop(); <-done }
}

// dialLobbyOrigin dials /ws/lobby with an optional Origin header; returns
// whether the WebSocket handshake succeeded.
func dialLobbyOrigin(t *testing.T, ts *httptest.Server, origin string) bool {
	t.Helper()
	url := strings.Replace(ts.URL, "http://", "ws://", 1) + "/ws/lobby"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var opts *websocket.DialOptions
	if origin != "" {
		opts = &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {origin}}}
	}
	c, _, err := websocket.Dial(ctx, url, opts)
	if err != nil {
		return false
	}
	c.Close(websocket.StatusNormalClosure, "")
	return true
}

// TestNet_OriginPolicy — same-origin enforced by default; AllowedOrigins
// widens; InsecureOrigin accepts any. (DoD #9b)
func TestNet_OriginPolicy(t *testing.T) {
	t.Run("default rejects cross-origin", func(t *testing.T) {
		ts, cleanup := originServer(t, nil, false)
		defer cleanup()
		if dialLobbyOrigin(t, ts, "http://evil.example") {
			t.Fatal("cross-origin handshake accepted; same-origin must be enforced")
		}
		// Same-origin (Origin == server URL) is accepted.
		if !dialLobbyOrigin(t, ts, ts.URL) {
			t.Fatal("same-origin handshake rejected")
		}
		// No Origin header (non-browser client) is accepted.
		if !dialLobbyOrigin(t, ts, "") {
			t.Fatal("no-Origin handshake rejected")
		}
		// Same host, different scheme is accepted by design (host-based
		// check) — this is what makes a TLS-terminating proxy work, where
		// the browser Origin is https:// but the backend Host is the same
		// host over http. ts.URL is http://127.0.0.1:PORT; an https Origin
		// with the same host:port must still be accepted.
		httpsOrigin := strings.Replace(ts.URL, "http://", "https://", 1)
		if !dialLobbyOrigin(t, ts, httpsOrigin) {
			t.Fatal("same-host https Origin rejected (would break TLS-proxy deploys)")
		}
	})

	t.Run("AllowedOrigins widens", func(t *testing.T) {
		ts, cleanup := originServer(t, []string{"evil.example"}, false)
		defer cleanup()
		if !dialLobbyOrigin(t, ts, "http://evil.example") {
			t.Fatal("allowed origin rejected")
		}
		if dialLobbyOrigin(t, ts, "http://other.example") {
			t.Fatal("non-allowed origin accepted")
		}
	})

	t.Run("InsecureOrigin accepts any", func(t *testing.T) {
		ts, cleanup := originServer(t, nil, true)
		defer cleanup()
		if !dialLobbyOrigin(t, ts, "http://evil.example") {
			t.Fatal("insecure-origin should accept any origin")
		}
	})
}
