// Package proto encodes/decodes the isnipes wire protocol.
//
// See PHASE2.md §5 (schema checksum), §6.1 (frame header), §6.3
// (binary message payloads), and §6.2 (lobby JSON envelopes).
//
// This package has no I/O and no goroutines; it is a pure library.
// PHASE2.md §4 forbids non-test files in internal/proto from
// importing net, net/http, nhooyr.io/websocket, or any other I/O
// package. TestProtoImportGraph (§13.4) enforces this.
package proto
