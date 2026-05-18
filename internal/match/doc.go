// Package match implements the Phase 2 match actor and registry.
//
// See PHASE2.md §9 for the actor lifecycle, §6.4 for event-emission
// rules, §13.5 for the test contract.
//
// All match state is owned by Match.Run's single goroutine; producers
// (WS readers, timers, the lobby) send messages on the Match.In
// channel and never touch state directly.
package match
