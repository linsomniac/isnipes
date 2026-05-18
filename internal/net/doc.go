// Package net is the Phase 2 WebSocket transport.
//
// See PHASE2.md §10 (transport), §6.1 (frame header), §8 (match WS
// handshake), §13.8 (tests).
//
// The package exposes a Server that wires HTTP routes, lobby and
// match WS upgraders, /healthz, /version, and per-connection
// reader/writer goroutines.
package wsnet
