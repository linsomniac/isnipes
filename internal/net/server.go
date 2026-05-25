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
	Lobby            *lobby.Lobby
	MatchRegistry    *match.Registry
	StaticFS         fs.FS
	HandshakeTimeout time.Duration // default 5s
	IdleTimeout      time.Duration // default 5s
	PingInterval     time.Duration // Phase 4 §4.3.3: default 500ms
	ServerVersion    string

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
	if cfg.ServerVersion == "" {
		cfg.ServerVersion = "v0.0.0-phase2"
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

// handleLobby upgrades a lobby WS and forwards messages.
func (s *Server) handleLobby(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, s.acceptOptions())
	if err != nil {
		return
	}
	out := make(chan lobby.Outbound, 64)
	sess := s.cfg.Lobby.Connect(out)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer goroutine.
	go func() {
		for o := range out {
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
		_ = c.Close(websocket.StatusNormalClosure, "ok")
	}()

	// Reader loop.
	for {
		c.SetReadLimit(64 * 1024)
		ctxR, ctxRCancel := context.WithTimeout(ctx, s.cfg.IdleTimeout+5*time.Second)
		mtype, data, err := c.Read(ctxR)
		ctxRCancel()
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
			hdr := proto.FrameHeader{Type: t, Seq: seq, Ack: proto.AckNone, Len: uint16(len(payload))}
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
			// Echo as Pong (client-initiated ping per §4.3.3).
			p, err := proto.DecodePing(payload)
			if err != nil {
				_ = closeWith(c, CloseMalformed)
				return
			}
			pong := proto.Pong{TsOrigin: p.TsOrigin, TsResponder: uint32(time.Now().UnixMilli())}
			b, _ := pong.Encode(nil)
			select {
			case out <- match.OutboundFrame{Type: proto.MsgPong, Payload: b}:
			default:
			}
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
