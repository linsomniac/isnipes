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
	"embed"
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
	"syscall"
	"time"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/observ"
)

//go:embed all:dist
var embeddedFS embed.FS

const version = "v0.0.0-phase2"

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
		sub, err := fs.Sub(embeddedFS, "dist")
		if err != nil {
			slog.Error("embed fs", "err", err)
			os.Exit(1)
		}
		staticFS = sub
	}

	if *requireTLS && (*tlsCert == "" || *tlsKey == "") {
		slog.Error("--require-tls needs both --tls-cert and --tls-key")
		os.Exit(2)
	}

	// Process-wide metrics; the match actors record tick budget + over-budget
	// snapshot drops into it (PHASE8 §6.4/§6.5).
	metrics := observ.NewRegistry()

	registry := match.NewRegistry(match.RegistryConfig{
		MaxConcurrentMatches: *maxMatches,
		TickSampler:          metrics.TickHistogram(),
		OnTickOverBudget:     metrics.IncTickOverBudget,
		OnSnapshotDrop:       metrics.IncSnapshotDrop,
	})
	lob := lobby.NewLobby(lobby.Config{
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
	// default so neither is exposed to the internet (PHASE8 §6.3).
	var adminSrv *http.Server
	if *adminAddr != "" {
		adminSrv = &http.Server{
			Addr:              *adminAddr,
			Handler:           newAdminMux(metrics, *enablePprof),
			ReadHeaderTimeout: 10 * time.Second,
		}
		slog.Info("admin listening", "addr", *adminAddr, "pprof", *enablePprof)
		go func() {
			if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("admin serve", "err", err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if adminSrv != nil {
		_ = adminSrv.Shutdown(ctx)
	}
	lob.Stop()
	<-lobbyDone
	_ = registry.StopAll(ctx) // graceful, deterministic match drain
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
