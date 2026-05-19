package match

import (
	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// Phase 5 §12.1 — scoreboard rate limit: at most one delta-driven
// Scoreboard frame every 6 sim ticks (= 5 Hz at 30 Hz). Mandatory
// frames (initial MatchJoin, reconnect resync) bypass the gate.
const ScoreboardMinIntervalTicks = 6

// scoreSnapshot captures every player's (Lives, Score) at the end of
// a tick. The detector compares against the previous snapshot to
// decide whether a Scoreboard broadcast is needed.
type scoreSnapshot struct {
	score map[sim.EntityID]int32
	lives map[sim.EntityID]uint8
}

func newScoreSnapshot() scoreSnapshot {
	return scoreSnapshot{
		score: make(map[sim.EntityID]int32),
		lives: make(map[sim.EntityID]uint8),
	}
}

// captureScores reads sim.Scores() and writes the result into s.
// Caller is responsible for ensuring this is invoked from the actor
// goroutine.
func (s *scoreSnapshot) capture(sm *sim.Sim) {
	if sm == nil {
		return
	}
	cur := sm.Scores()
	for id, ps := range cur {
		s.score[id] = ps.Score
		s.lives[id] = ps.Lives
	}
}

// diffs reports whether any (Lives, Score) value in `cur` differs from
// `prev`. Newly-added IDs count as a change. Removed IDs are NOT a
// change (a removed player's slab entry is GC'd; their score lives
// on in playerState and is reported in cur unchanged).
func (s scoreSnapshot) diffs(prev scoreSnapshot) bool {
	if len(s.score) != len(prev.score) {
		return true
	}
	// Codex P5/iter5: distinguish "missing key" from "zero value". A
	// same-cardinality keyset swap (id 1 removed, id 2 added with
	// zero score) would otherwise read as no change.
	for id, v := range s.score {
		pv, ok := prev.score[id]
		if !ok || pv != v {
			return true
		}
	}
	for id, v := range s.lives {
		pv, ok := prev.lives[id]
		if !ok || pv != v {
			return true
		}
	}
	return false
}

// detectScoreChange consults sim.Scores() and reports whether any
// player's (Lives, Score) has drifted since the last broadcast. Side-
// effect free; the caller updates m.lastScores once the broadcast
// fires.
func (m *Match) detectScoreChange() bool {
	if m.sim == nil {
		return false
	}
	var cur scoreSnapshot
	cur = newScoreSnapshot()
	cur.capture(m.sim)
	return cur.diffs(m.lastScores)
}

// maybeBroadcastScoreboard implements the §12.1 cap + emit cycle:
//   - if no change → no-op.
//   - if change but cooldown not elapsed → mark pendingScoreboard
//     so the next eligible tick fires.
//   - if change AND cooldown elapsed → broadcast Scoreboard to every
//     joined slot (DC slots skipped via slot.out == nil), update
//     lastScores + lastScoreboardTick.
//
// Called once at end-of-tick.
func (m *Match) maybeBroadcastScoreboard() {
	if m.sim == nil {
		return
	}
	changed := m.detectScoreChange()
	if changed {
		m.pendingScoreboard = true
	}
	if !m.pendingScoreboard {
		return
	}
	now := m.sim.ServerTick()
	if now < m.lastScoreboardTick+ScoreboardMinIntervalTicks {
		// Cooldown not yet elapsed — defer to the next tick.
		return
	}
	m.broadcastScoreboard()
	m.lastScoreboardTick = now
	m.lastScores = newScoreSnapshot()
	m.lastScores.capture(m.sim)
	m.pendingScoreboard = false
}

// broadcastScoreboard sends a fresh Scoreboard frame to every joined,
// non-DC slot. Mandatory-frame callers (initial MatchJoin in §6.2,
// reconnect resync in §9.5) continue to use sendScoreboardTo directly.
func (m *Match) broadcastScoreboard() {
	sb := proto.Scoreboard{
		ServerTick: m.sim.ServerTick(),
		Entries:    m.buildScoreboardEntries(),
	}
	for _, slot := range m.slots {
		if !slot.Joined || slot.out == nil {
			continue
		}
		m.sendFrameTo(slot, proto.MsgScoreboard, sb)
	}
}
