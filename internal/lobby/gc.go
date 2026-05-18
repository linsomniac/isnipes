package lobby

import (
	"time"

	"github.com/jafo/isnipes/internal/match"
)

// gcThreshold returns the configured inactivity threshold or the
// default 30s. Single-source-of-truth for both sweepers.
func (l *Lobby) gcThreshold() time.Duration {
	if l.cfg.GCInactivityThreshold > 0 {
		return l.cfg.GCInactivityThreshold
	}
	return 30 * time.Second
}

// sweepEmptyRooms — Phase 6 §10.1. Walks l.rooms; for every room with
// zero members AND EmptySince older than gcThreshold, removes it from
// the map and emits a `room_removed` delta. A room that just emptied
// gets its EmptySince stamped here (the actor's removeFromRoom path
// doesn't itself stamp — keeping the bookkeeping local to one place).
func (l *Lobby) sweepEmptyRooms() {
	now := l.clock()
	threshold := l.gcThreshold()
	for rid, room := range l.rooms {
		if len(room.Members) == 0 {
			if room.EmptySince.IsZero() {
				room.EmptySince = now
				continue
			}
			if now.Sub(room.EmptySince) >= threshold {
				delete(l.rooms, rid)
				l.broadcastRoomRemoved(rid)
			}
		} else if !room.EmptySince.IsZero() {
			// Rejoined within the window.
			room.EmptySince = time.Time{}
		}
	}
}

// matchTracker tracks per-match zero-player duration. Populated by
// handleStartMatch and reaped on match end or zero-player abandonment.
type matchTracker struct {
	matchID       string
	zeroPlayersAt time.Time // first observed zero-joined-count
}

// sweepZeroPlayerMatches — Phase 6 §10.2. For every tracked match,
// query JoinedCount() through the registry. If zero for ≥ gcThreshold,
// call match.Abort which terminates the actor and emits MatchOver.
// The matchTracker entry is reaped on next sweep after state==ENDED.
func (l *Lobby) sweepZeroPlayerMatches() {
	if l.cfg.Registry == nil {
		return
	}
	now := l.clock()
	threshold := l.gcThreshold()
	for id, mt := range l.matchTrackers {
		m, ok := l.cfg.Registry.Lookup(id)
		if !ok || m == nil {
			delete(l.matchTrackers, id)
			continue
		}
		if m.State() == match.StateEnded {
			delete(l.matchTrackers, id)
			continue
		}
		if m.JoinedCount() == 0 {
			if mt.zeroPlayersAt.IsZero() {
				mt.zeroPlayersAt = now
				continue
			}
			if now.Sub(mt.zeroPlayersAt) >= threshold {
				m.Abort("zero-players-30s")
			}
		} else if !mt.zeroPlayersAt.IsZero() {
			mt.zeroPlayersAt = time.Time{}
		}
	}
}
