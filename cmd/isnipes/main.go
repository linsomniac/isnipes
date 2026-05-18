// Command isnipes is the Phase 2 server binary.
//
// Flags:
//
//	--addr           HTTP listen address (default :8080)
//	--max-matches    Maximum concurrent matches (default 64)
//	--motd           Message of the day shown in lobby welcome
//	--log-level      slog level (debug|info|warn|error)
//	--enable-pprof   Mount /debug/pprof/*
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
)

//go:embed all:dist
var embeddedFS embed.FS

const version = "v0.0.0-phase2"

func main() {
	var (
		addr        = flag.String("addr", ":8080", "HTTP listen address")
		maxMatches  = flag.Int("max-matches", 64, "max concurrent matches")
		motd        = flag.String("motd", "Welcome to isnipes.", "lobby message of the day")
		logLevel    = flag.String("log-level", "info", "log level: debug|info|warn|error")
		enablePprof = flag.Bool("enable-pprof", false, "mount /debug/pprof/*")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLogLevel(*logLevel)})))

	staticFS, err := fs.Sub(embeddedFS, "dist")
	if err != nil {
		slog.Error("embed fs", "err", err)
		os.Exit(1)
	}

	registry := match.NewRegistry(match.RegistryConfig{MaxConcurrentMatches: *maxMatches})
	lob := lobby.NewLobby(lobby.Config{
		Registry:      registry,
		MOTD:          *motd,
		ServerVersion: version,
	})
	lobbyDone := make(chan struct{})
	go func() { lob.Run(); close(lobbyDone) }()

	srv := wsnet.NewServer(wsnet.ServerConfig{
		Lobby:         lob,
		MatchRegistry: registry,
		StaticFS:      staticFS,
		ServerVersion: version,
	})

	mux := srv.Handler().(*http.ServeMux)
	_ = enablePprof // pprof mount is a v0.0.1 add-on; documented in PHASE2.md

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("listening", "addr", *addr, "version", version)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listenAndServe", "err", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	lob.Stop()
	<-lobbyDone
	_ = registry.Close(ctx)
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
