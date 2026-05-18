// Package lobby implements the Phase 2 minimal lobby + room server.
//
// See PHASE2.md §7 (lobby), §6.2 (JSON envelopes), §13.7 (tests).
//
// The Lobby has one actor goroutine that owns all room and session
// state; producers (WS handlers, the token janitor) send messages
// on the inbox channel.
package lobby
