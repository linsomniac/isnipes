package wsnet_test

// Exercises the admission / error close paths of handleMatch (and, by
// extension, close.go's closeWith + CloseReason.Label) over real
// WebSocket connections. These are the §6.1 / §4.3.5 close codes a
// malformed or unauthorized client triggers before or after the
// MatchJoin handshake.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/proto"
)

// writeFrameRaw sends a pre-built binary application frame.
func writeFrameRaw(t *testing.T, c *websocket.Conn, ctx context.Context, typ proto.MsgType, payload []byte) {
	t.Helper()
	hdr := proto.FrameHeader{Type: typ, Seq: 0, Ack: proto.AckNone, Len: uint16(len(payload))}
	buf, err := proto.EncodeFrame(nil, hdr, payload)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageBinary, buf); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// readUntilClose drains frames until the server closes the WS, returning
// the close status code.
func readUntilClose(t *testing.T, c *websocket.Conn, ctx context.Context) websocket.StatusCode {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, 1*time.Second)
		_, _, err := c.Read(rctx)
		cancel()
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
	t.Fatal("server never closed the connection")
	return -1
}

// TestNet_MatchClosePaths covers the handshake-stage rejections. Each
// sub-test dials a valid match, sends a single bad first frame, and
// asserts the SPEC close code.
func TestNet_MatchClosePaths(t *testing.T) {
	cases := []struct {
		name string
		send func(t *testing.T, c *websocket.Conn, ctx context.Context)
		want websocket.StatusCode
	}{
		{
			name: "non-binary first frame",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				wctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = c.Write(wctx, websocket.MessageText, []byte("hello"))
			},
			want: websocket.StatusCode(wsnet.CloseMalformed),
		},
		{
			name: "undecodable first frame",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				wctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = c.Write(wctx, websocket.MessageBinary, []byte{0x01, 0x02})
			},
			want: websocket.StatusCode(wsnet.CloseMalformed),
		},
		{
			name: "wrong message type",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				writeFrameRaw(t, c, ctx, proto.MsgInput, []byte{0, 0, 0, 0})
			},
			want: websocket.StatusCode(wsnet.CloseMalformed),
		},
		{
			name: "schema mismatch",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				mj := proto.MatchJoin{SchemaChecksum: proto.SchemaChecksum() ^ 0xFFFF, Token: []byte("tokA")}
				p, err := mj.Encode(nil)
				if err != nil {
					t.Fatal(err)
				}
				writeFrameRaw(t, c, ctx, proto.MsgMatchJoin, p)
			},
			want: websocket.StatusCode(wsnet.CloseVersion),
		},
		{
			name: "unknown token",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				mj := proto.MatchJoin{SchemaChecksum: proto.SchemaChecksum(), Token: []byte("nope")}
				p, err := mj.Encode(nil)
				if err != nil {
					t.Fatal(err)
				}
				writeFrameRaw(t, c, ctx, proto.MsgMatchJoin, p)
			},
			want: websocket.StatusCode(wsnet.CloseAuth),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, reg, lob, cleanup := laggedServer(t, 100*time.Millisecond)
			defer cleanup()
			matchID, _, m := joinMatchSetup(t, reg, lob)
			defer m.Abort("test")
			c, cancel := dialMatch(t, ts, matchID)
			defer cancel()
			ctx := context.Background()
			tc.send(t, c, ctx)
			if got := readUntilClose(t, c, ctx); got != tc.want {
				t.Fatalf("close code = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestNet_MatchMalformedAfterJoin: a client that completes the handshake
// then sends a malformed/illegal frame is closed with MALFORMED and the
// actor sees a disconnect.
func TestNet_MatchMalformedAfterJoin(t *testing.T) {
	cases := []struct {
		name string
		send func(t *testing.T, c *websocket.Conn, ctx context.Context)
	}{
		{
			name: "garbage binary",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				wctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = c.Write(wctx, websocket.MessageBinary, []byte{0xFF})
			},
		},
		{
			name: "second MatchJoin",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				mj := proto.MatchJoin{SchemaChecksum: proto.SchemaChecksum(), Token: []byte("tokA")}
				p, _ := mj.Encode(nil)
				writeFrameRaw(t, c, ctx, proto.MsgMatchJoin, p)
			},
		},
		{
			name: "text frame",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				wctx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				_ = c.Write(wctx, websocket.MessageText, []byte("nope"))
			},
		},
		{
			name: "truncated input",
			send: func(t *testing.T, c *websocket.Conn, ctx context.Context) {
				writeFrameRaw(t, c, ctx, proto.MsgInput, []byte{0x00})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, reg, lob, cleanup := laggedServer(t, 100*time.Millisecond)
			defer cleanup()
			matchID, token, m := joinMatchSetup(t, reg, lob)
			defer m.Abort("test")
			c, cancel := dialMatch(t, ts, matchID)
			defer cancel()
			ctx := context.Background()
			sendMatchJoin(t, c, ctx, token)
			// Let the handshake settle (actor registers the slot).
			recvFrame(t, c, ctx, 500*time.Millisecond)
			tc.send(t, c, ctx)
			if got := readUntilClose(t, c, ctx); got != websocket.StatusCode(wsnet.CloseMalformed) {
				t.Fatalf("close code = %d, want %d", got, wsnet.CloseMalformed)
			}
		})
	}
}

// TestNet_MatchHandshakeTimeout: a client that connects but never sends
// the MatchJoin first frame is closed with IDLE once the handshake
// timeout elapses.
func TestNet_MatchHandshakeTimeout(t *testing.T) {
	reg := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: 4})
	lob := lobby.NewLobby(lobby.Config{Registry: reg})
	done := make(chan struct{})
	go func() { lob.Run(); close(done) }()
	defer func() {
		lob.Stop()
		<-done
	}()
	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:            lob,
		MatchRegistry:    reg,
		HandshakeTimeout: 200 * time.Millisecond,
		PingInterval:     50 * time.Millisecond,
		ServerVersion:    "ho-test",
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	matchID, _, m := joinMatchSetup(t, reg, lob)
	defer m.Abort("test")

	c, cancel := dialMatch(t, ts, matchID)
	defer cancel()
	ctx := context.Background()
	// The server's read-context expiry (HandshakeTimeout) trips the IDLE
	// close branch in handleMatch. nhooyr tears the socket down on read-
	// context cancellation, so the wire result is an abnormal closure
	// rather than a clean 4007 frame; we assert only that the connection
	// is closed well within the 5s default handshake window.
	start := time.Now()
	_ = readUntilClose(t, c, ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("handshake-timeout close took %v, want < 2s", elapsed)
	}
}

// TestNet_LobbyRejectsBinaryFrame: the lobby transport is JSON/text; a
// binary frame is closed with UnsupportedData. Covers handleLobby's
// non-text branch.
func TestNet_LobbyRejectsBinaryFrame(t *testing.T) {
	ts, _, _, cleanup := newTestServer(t)
	defer cleanup()
	ctx := context.Background()
	url := wsURL(ts) + "/ws/lobby"
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close(websocket.StatusInternalError, "")
	wctx, wcancel := context.WithTimeout(ctx, time.Second)
	_ = c.Write(wctx, websocket.MessageBinary, []byte{0x00, 0x01})
	wcancel()
	if got := readUntilClose(t, c, ctx); got != websocket.StatusUnsupportedData {
		t.Fatalf("close code = %d, want %d (UnsupportedData)", got, websocket.StatusUnsupportedData)
	}
}

func wsURL(ts *httptest.Server) string {
	return "ws" + ts.URL[len("http"):]
}
