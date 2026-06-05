// Command isnipes is the server binary.
//
// Flags:
//
//	--addr             public HTTP/WS listen address (default :8080)
//	--admin-addr       admin listener for /metrics + pprof (default
//	                   127.0.0.1:6060; empty disables) — never the public port
//	--max-matches      maximum concurrent matches (default 64)
//	--motd             message of the day shown in lobby welcome
//	--log-level        slog level (debug|info|warn|error)
//	--enable-pprof     mount /debug/pprof/* on the admin listener
//	--require-tls      terminate TLS directly (with --tls-cert/--tls-key)
//	--tls-cert         TLS certificate PEM (when --require-tls)
//	--tls-key          TLS private-key PEM (when --require-tls)
//	--allowed-origins  comma-separated extra WebSocket origins (host[:port])
//	--insecure-origin  DEV ONLY: accept any WebSocket Origin
//	--web-dist         serve the client from this directory instead of the
//	                   embedded build (used by the Playwright e2e harness)
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/observ"
)

const version = "v1.0.0"

func main() {
	var (
		addr           = flag.String("addr", ":8080", "public HTTP/WS listen address")
		adminAddr      = flag.String("admin-addr", "127.0.0.1:6060", "admin listener (/metrics + pprof); empty disables")
		maxMatches     = flag.Int("max-matches", 64, "max concurrent matches")
		motd           = flag.String("motd", "Welcome to isnipes.", "lobby message of the day")
		logLevel       = flag.String("log-level", "info", "log level: debug|info|warn|error")
		enablePprof    = flag.Bool("enable-pprof", false, "mount /debug/pprof/* on the admin listener")
		showVersion    = flag.Bool("version", false, "print version and exit")
		webDist        = flag.String("web-dist", "", "serve the client from this directory instead of the embedded build")
		requireTLS     = flag.Bool("require-tls", false, "terminate TLS directly (needs --tls-cert/--tls-key)")
		tlsCert        = flag.String("tls-cert", "", "TLS certificate file (PEM) when --require-tls")
		tlsKey         = flag.String("tls-key", "", "TLS private key file (PEM) when --require-tls")
		allowedOrigins = flag.String("allowed-origins", "", "comma-separated extra WebSocket origins (host[:port])")
		insecureOrigin = flag.Bool("insecure-origin", false, "DEV ONLY: accept any WebSocket Origin")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(*logLevel)})))

	var staticFS fs.FS
	if *webDist != "" {
		staticFS = os.DirFS(*webDist)
	} else {
		staticFS = embeddedStatic()
	}

	if *requireTLS && (*tlsCert == "" || *tlsKey == "") {
		slog.Error("--require-tls needs both --tls-cert and --tls-key")
		os.Exit(2)
	}

	// Process-wide metrics; the match actors record tick budget + over-budget
	// snapshot drops into it (PHASE8 §6.4/§6.5).
	metrics := observ.NewRegistry()

	// lob is referenced by the registry's OnMatchEnded hook below, so it is
	// declared first and the closure captures it by reference. lob is assigned
	// immediately after (before the HTTP server starts), so no match can end —
	// and fire the hook — until lob is non-nil.
	var lob *lobby.Lobby
	registry := match.NewRegistry(match.RegistryConfig{
		MaxConcurrentMatches: *maxMatches,
		TickSampler:          metrics.TickHistogram(),
		OnTickOverBudget:     metrics.IncTickOverBudget,
		OnSnapshotDrop:       metrics.IncSnapshotDrop,
		OnJoinedDelta:        metrics.AddJoinedPlayers,
		OnActiveMatchesDelta: metrics.AddActiveMatches,
		// Match end → lobby: close & remove the hosting room (stale-room fix).
		OnMatchEnded: func(id string) { lob.MatchEnded(id) },
	})
	lob = lobby.NewLobby(lobby.Config{
		Registry:      registry,
		MOTD:          *motd,
		ServerVersion: version,
	})
	lobbyDone := make(chan struct{})
	go func() { lob.Run(); close(lobbyDone) }()

	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:          lob,
		MatchRegistry:  registry,
		StaticFS:       staticFS,
		ServerVersion:  version,
		AllowedOrigins: splitOrigins(*allowedOrigins),
		InsecureOrigin: *insecureOrigin,
		OnBytesIn:      func(n int) { metrics.AddBytesIn(uint64(n)) },
		OnBytesOut:     func(n int) { metrics.AddBytesOut(uint64(n)) },
	})

	// Public listener: client + WS only. Never /metrics or pprof.
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	publicLn, err := net.Listen("tcp", *addr)
	if err != nil {
		slog.Error("listen", "addr", *addr, "err", err)
		os.Exit(1)
	}
	slog.Info("listening", "addr", *addr, "tls", *requireTLS, "version", version)
	go func() {
		if err := serve(httpSrv, publicLn, *requireTLS, *tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
			slog.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	// Admin listener: /metrics always, pprof when enabled. Loopback by
	// default so neither is exposed to the internet (PHASE8 §6.3). Bound
	// synchronously and fatal on failure so we never claim readiness while
	// the configured /metrics endpoint is silently down.
	var adminSrv *http.Server
	if *adminAddr != "" {
		adminLn, err := net.Listen("tcp", *adminAddr)
		if err != nil {
			slog.Error("admin listen", "addr", *adminAddr, "err", err)
			os.Exit(1)
		}
		adminSrv = &http.Server{
			Handler:           newAdminMux(metrics, *enablePprof),
			ReadHeaderTimeout: 10 * time.Second,
		}
		slog.Info("admin listening", "addr", *adminAddr, "pprof", *enablePprof)
		go func() {
			if err := adminSrv.Serve(adminLn); err != nil && err != http.ErrServerClosed {
				slog.Error("admin serve", "err", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	slog.Info("shutting down")

	// Drain HTTP listeners concurrently so a slow in-flight request (e.g. a
	// pprof profile on the admin port) cannot starve the match-drain
	// deadline that follows.
	httpCtx, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTP()
	var wg sync.WaitGroup
	for _, s := range []*http.Server{httpSrv, adminSrv} {
		if s == nil {
			continue
		}
		wg.Add(1)
		go func(s *http.Server) {
			defer wg.Done()
			if err := s.Shutdown(httpCtx); err != nil {
				slog.Warn("http shutdown", "err", err)
			}
		}(s)
	}
	wg.Wait()

	lob.Stop()
	<-lobbyDone

	// Graceful, deterministic match drain on its own deadline.
	stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStop()
	if err := registry.StopAll(stopCtx); err != nil {
		slog.Warn("match drain", "err", err)
	}
}

// serve starts srv on ln, with direct TLS when requireTLS.
func serve(srv *http.Server, ln net.Listener, requireTLS bool, certFile, keyFile string) error {
	if requireTLS {
		return srv.ServeTLS(ln, certFile, keyFile)
	}
	return srv.Serve(ln)
}

// newAdminMux builds the admin listener mux: /metrics always, and the
// net/http/pprof handlers only when enablePprof is set. Kept separate from
// the public mux so profiling/metrics never ride the internet-facing port.
func newAdminMux(metrics *observ.Registry, enablePprof bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", metrics.Handler())
	if enablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	return mux
}

// splitOrigins parses a comma-separated origins list, trimming blanks.
func splitOrigins(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseLogLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
