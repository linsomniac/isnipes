package wsnet

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// ServerConfig governs the Server.
type ServerConfig struct {
	Lobby             *lobby.Lobby
	MatchRegistry     *match.Registry
	StaticFS          fs.FS
	HandshakeTimeout  time.Duration // default 5s
	IdleTimeout       time.Duration // default 5s
	PingInterval      time.Duration // Phase 4 §4.3.3: default 500ms (match keepalive)
	LobbyPingInterval time.Duration // lobby WS keepalive; default lobbyPingInterval
	ServerVersion     string

	// Phase 8 § transport-security — WebSocket origin policy. Default
	// (both zero) enforces nhooyr's SAME-HOST check: the browser's Origin
	// host must equal the request Host, blocking cross-site WebSocket
	// hijacking from another site. (The check is host-based, not
	// scheme-based — which is deliberately what you want behind a
	// TLS-terminating proxy, where the browser Origin is https:// but the
	// backend sees http://; a scheme-strict check would false-reject that
	// standard deployment. There are no ambient cookies/credentials —
	// auth is the per-match joinToken — so host-based matching is the
	// right defence here.) AllowedOrigins widens to extra hosts (nhooyr
	// OriginPatterns, e.g. "localhost:5173" for a separate dev front-end;
	// note "*" matches any host and is equivalent to InsecureOrigin).
	// InsecureOrigin disables the check entirely — DEV ONLY; never in prod.
	AllowedOrigins []string
	InsecureOrigin bool

	// Phase 8 §6.2 — optional, nil-safe match-traffic byte counters wired
	// to the observ registry's bytes_in/out_total series. Count the raw
	// WebSocket message bytes on the match path (the bandwidth-relevant
	// traffic).
	OnBytesIn  func(n int)
	OnBytesOut func(n int)
}

// Server is the Phase 2 transport surface.
type Server struct {
	cfg ServerConfig
}

// NewServer wires routes.
func NewServer(cfg ServerConfig) *Server {
	if cfg.HandshakeTimeout == 0 {
		cfg.HandshakeTimeout = 5 * time.Second
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 5 * time.Second
	}
	if cfg.PingInterval == 0 {
		cfg.PingInterval = 500 * time.Millisecond
	}
	if cfg.LobbyPingInterval == 0 {
		cfg.LobbyPingInterval = lobbyPingInterval
	}
	if cfg.ServerVersion == "" {
		cfg.ServerVersion = "v1.0.1"
	}
	return &Server{cfg: cfg}
}

// acceptOptions builds the WebSocket accept options from the configured
// origin policy. With InsecureOrigin=false and no AllowedOrigins, nhooyr
// enforces same-origin (the secure default).
func (s *Server) acceptOptions() *websocket.AcceptOptions {
	return &websocket.AcceptOptions{
		InsecureSkipVerify: s.cfg.InsecureOrigin,
		OriginPatterns:     s.cfg.AllowedOrigins,
	}
}

// Handler returns an http.Handler with all routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.healthz)
	mux.HandleFunc("/version", s.version)
	mux.HandleFunc("/ws/lobby", s.handleLobby)
	mux.HandleFunc("/ws/match/", s.handleMatch)
	if s.cfg.StaticFS != nil {
		mux.Handle("/", http.FileServer(http.FS(s.cfg.StaticFS)))
	}
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"server":         s.cfg.ServerVersion,
		"schemaChecksum": fmt.Sprintf("0x%08X", proto.SchemaChecksum()),
	})
}

// Lobby keepalive: unlike the match path (where the client streams input +
// pongs at 30Hz so reads never go idle), the lobby client is silent between
// user actions — a player who simply reads the lobby screen sends nothing.
// So the reader CANNOT impose a short idle deadline (it would drop an
// idle-but-alive player; the browser auto-replies to WS pings as control
// frames, which nhooyr handles internally and never surface from c.Read, so
// a per-read deadline would still fire). Instead the writer sends a periodic
// WebSocket ping and treats a missing pong as a dead peer; the reader blocks
// on the connection context, which that failure cancels.
// AIDEV-NOTE: do NOT reintroduce a per-read idle timeout on the lobby socket
// — it silently closes idle lobbies, surfacing client-side as "WebSocket is
// already in a closing or closed state" on the next user action.
const (
	lobbyPingInterval = 20 * time.Second
	lobbyPongTimeout  = 10 * time.Second
)

// handleLobby upgrades a lobby WS and forwards messages.
func (s *Server) handleLobby(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return
	}
	out := make(chan lobby.Outbound, 64)
	sess := s.cfg.Lobby.Connect(out)
	if sess == nil {
		// The lobby actor stopped mid-handshake — a graceful shutdown races
		// a freshly-accepted WS that http.Server.Shutdown can't close because
		// it is hijacked. Connect returns nil and has already closed `out`,
		// so do NOT close it again (double-close panics). Without this guard
		// the reader loop nil-derefs sess.ID.
		_ = c.Close(websocket.StatusGoingAway, "shutting down")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer goroutine: drains outbound lobby messages and keeps the
	// connection alive with periodic pings (see the keepalive note above).
	go func() {
		ticker := time.NewTicker(s.cfg.LobbyPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = c.Close(websocket.StatusNormalClosure, "ok")
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, lobbyPongTimeout)
				err := c.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
				// Pong received → peer is alive; refresh the lobby idle
				// timer so the actor's sweep doesn't reap a quiet session.
				s.cfg.Lobby.Touch(sess.ID)
			case o, ok := <-out:
				if !ok {
					_ = c.Close(websocket.StatusNormalClosure, "ok")
					return
				}
				b, err := proto.EncodeLobbyMessage(o.Type, o.Payload)
				if err != nil {
					continue
				}
				ctxW, ctxWCancel := context.WithTimeout(ctx, 2*time.Second)
				err = c.Write(ctxW, websocket.MessageText, b)
				ctxWCancel()
				if err != nil {
					cancel()
					return
				}
				if o.CloseCode != 0 {
					_ = c.Close(websocket.StatusCode(o.CloseCode), "")
					cancel()
					return
				}
			}
		}
	}()

	// Reader loop. Bound only by the connection context: the writer's ping
	// keepalive (not a read deadline) is what detects a dead peer.
	c.SetReadLimit(64 * 1024)
	for {
		mtype, data, err := c.Read(ctx)
		if err != nil {
			break
		}
		if mtype != websocket.MessageText {
			_ = c.Close(websocket.StatusUnsupportedData, "MALFORMED")
			break
		}
		s.cfg.Lobby.PostMessage(sess.ID, data)
	}
	s.cfg.Lobby.Disconnect(sess.ID)
}

// handleMatch upgrades a match WS, validates MatchJoin, then runs the
// reader/writer loops bound to the match actor.
func (s *Server) handleMatch(w http.ResponseWriter, r *http.Request) {
	matchID := strings.TrimPrefix(r.URL.Path, "/ws/match/")
	if matchID == "" {
		http.NotFound(w, r)
		return
	}
	m, ok := s.cfg.MatchRegistry.Lookup(matchID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return
	}

	// First-frame timer.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	c.SetReadLimit(int64(proto.MaxFrameLen) + 16)

	readCtx, readCancel := context.WithTimeout(ctx, s.cfg.HandshakeTimeout)
	mtype, data, err := c.Read(readCtx)
	readCancel()
	if err != nil {
		_ = closeWith(c, CloseIdle)
		return
	}
	if s.cfg.OnBytesIn != nil {
		s.cfg.OnBytesIn(len(data))
	}
	if mtype != websocket.MessageBinary {
		_ = closeWith(c, CloseMalformed)
		return
	}
	hdr, payload, _, err := proto.DecodeFrame(data)
	if err != nil {
		_ = closeWith(c, CloseMalformed)
		return
	}
	if hdr.Type != proto.MsgMatchJoin {
		_ = closeWith(c, CloseMalformed)
		return
	}
	mj, err := proto.DecodeMatchJoin(payload)
	if err != nil {
		_ = closeWith(c, CloseMalformed)
		return
	}
	if mj.SchemaChecksum != proto.SchemaChecksum() {
		_ = closeWith(c, CloseVersion)
		return
	}
	out := make(chan match.OutboundFrame, 64)
	// Phase 5 §16.2 (codex P5/iter6 #1): actor-side dispatch.
	// handleJoin internally checks dcTokens and reroutes to the
	// reconnect path; this eliminates the TOCTOU window in the
	// dispatcher and keeps the net layer agnostic of slot state.
	var pid sim.EntityID
	pid, err = m.SubmitJoin(string(mj.Token), out)
	if err != nil {
		_ = closeWith(c, CloseAuth)
		return
	}

	// Writer goroutine. Phase 4 §7.1: integrates the server-initiated
	// Ping ticker so writes are serialised through a single goroutine
	// and `out` ownership stays exclusively with the match actor.
	// Previous design ran the ping ticker as a separate goroutine
	// that wrote into `out`, which could panic if the match actor
	// closed `out` concurrently with a ticker fire (codex P4 #1).
	go func() {
		var seq uint16
		ticker := time.NewTicker(s.cfg.PingInterval)
		defer ticker.Stop()
		writeFrame := func(t proto.MsgType, payload []byte) bool {
			hdr := proto.FrameHeader{Type: t, Seq: seq, Ack: proto.AckNone}
			seq++
			buf, err := proto.EncodeFrame(nil, hdr, payload)
			if err != nil {
				cancel()
				return false
			}
			ctxW, ctxWCancel := context.WithTimeout(ctx, 2*time.Second)
			err = c.Write(ctxW, websocket.MessageBinary, buf)
			ctxWCancel()
			if err != nil {
				cancel()
				return false
			}
			if s.cfg.OnBytesOut != nil {
				s.cfg.OnBytesOut(len(buf))
			}
			return true
		}
		for {
			select {
			case <-ctx.Done():
				_ = c.Close(websocket.StatusNormalClosure, "ok")
				return
			case <-ticker.C:
				ping := proto.Ping{TsOrigin: uint32(time.Now().UnixMilli())}
				b, _ := ping.Encode(nil)
				if !writeFrame(proto.MsgPing, b) {
					return
				}
			case f, ok := <-out:
				if !ok {
					_ = c.Close(websocket.StatusNormalClosure, "ok")
					return
				}
				if !writeFrame(f.Type, f.Payload) {
					return
				}
			}
		}
	}()

	// Reader loop.
	for {
		readCtx, readCancel := context.WithTimeout(ctx, s.cfg.IdleTimeout+5*time.Second)
		mtype, data, err := c.Read(readCtx)
		readCancel()
		if err != nil {
			m.SubmitDC(pid)
			return
		}
		if s.cfg.OnBytesIn != nil {
			s.cfg.OnBytesIn(len(data))
		}
		if mtype != websocket.MessageBinary {
			_ = closeWith(c, CloseMalformed)
			m.SubmitDC(pid)
			return
		}
		hdr, payload, _, err := proto.DecodeFrame(data)
		if err != nil {
			_ = closeWith(c, CloseMalformed)
			m.SubmitDC(pid)
			return
		}
		switch hdr.Type {
		case proto.MsgInput:
			inp, err := proto.DecodeInput(payload)
			if err != nil {
				_ = closeWith(c, CloseMalformed)
				m.SubmitDC(pid)
				return
			}
			m.SubmitInput(pid, inp)
		case proto.MsgPing:
			// Client-initiated ping (§4.3.3): route the Pong through the
			// match actor so `out` keeps a single producer. A direct send
			// here raced the actor's close(out) on teardown — select-with-
			// default does NOT make a send on a closed channel safe (it
			// panics with "send on closed channel").
			p, err := proto.DecodePing(payload)
			if err != nil {
				_ = closeWith(c, CloseMalformed)
				m.SubmitDC(pid) // match every other fatal post-join path
				return
			}
			m.SubmitClientPing(pid, p.TsOrigin)
		case proto.MsgPong:
			// Phase 4 §7.2: Pong reply to a server-issued Ping.
			// Compute RTT = now - ts_origin (server clock domain).
			p, err := proto.DecodePong(payload)
			if err != nil {
				_ = closeWith(c, CloseMalformed)
				m.SubmitDC(pid)
				return
			}
			now := uint32(time.Now().UnixMilli())
			// Unsigned wrap means a forged future ts_origin yields
			// a huge RTT; the OWT estimator clamps via
			// maxObservedRTTMs (PHASE4.md §7 / match/owt.go).
			rttMs := now - p.TsOrigin
			m.SubmitPong(pid, rttMs)
		case proto.MsgChat:
			// Phase 7 §7: in-match chat. Decode the {u8 len, text}
			// payload and hand to the match actor for relay. A malformed
			// payload is dropped (not fatal — chat is best-effort).
			if text, ok := match.DecodeChatText(payload); ok {
				m.SubmitChat(pid, text)
			}
		case proto.MsgMatchJoin:
			// MatchJoin after first frame is malformed.
			_ = closeWith(c, CloseMalformed)
			m.SubmitDC(pid)
			return
		default:
			// Ignore unknown types — mustn't crash.
		}
	}
}
