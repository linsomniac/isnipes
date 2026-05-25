package match

import (
	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// handleDC processes a ctlDC: marks the slot as DC, freezes the entity
// in the sim, stamps the 30-second grace deadline, records the
// reconnect token, and broadcasts player_dc to every other slot.
// Idempotent: a second ctlDC for the same slot is silently ignored.
// PHASE5.md §9.1.
func (m *Match) handleDC(v ctlDC) {
	slot, ok := m.slots[v.PlayerID]
	if !ok || !slot.Joined {
		return
	}
	if slot.DC {
		return
	}
	slot.DC = true
	graceTicks := DCGraceTicks
	if m.cfg.DCGraceTicksOverride != 0 {
		graceTicks = m.cfg.DCGraceTicksOverride
	}
	if m.sim != nil {
		slot.DCDeadlineTick = m.sim.ServerTick() + graceTicks
		m.sim.FreezePlayer(v.PlayerID)
	}
	// Find the slot's original join token so the reconnect path can
	// validate the same token. The token is the bySession key (if it
	// survived the join-consume) OR pulled from cfg.PlayerSlots.
	tok := m.tokenForPlayer(v.PlayerID)
	if tok != "" {
		m.dcTokensMu.Lock()
		m.dcTokens[tok] = v.PlayerID
		m.dcTokensMu.Unlock()
	}
	// Close the writer side of the dropped connection. The slot keeps
	// Joined=true so DC-grace counts; subsequent sends skip it via
	// slot.out == nil. We don't call closeSlot here because that
	// closes slot.closed which other observers may be waiting on for
	// permanent termination.
	if slot.out != nil {
		// Drain unread frames so the writer goroutine doesn't block.
		// The writer is the one that owns slot.out; we just stop
		// referencing it. The net layer's reader/writer goroutines
		// for the dropped WS terminate independently.
		slot.out = nil
	}
	m.broadcastEvent(proto.Event{
		Kind:   uint8(proto.EventPlayerDC),
		Target: uint32(v.PlayerID),
	})
}

// handleReconnect processes a ctlReconnect: validates the token
// against dcTokens and the slot's DC state, then re-binds the slot's
// writer and emits the §9.5 resync sequence
// (Resync → MapInit → Snapshot → Scoreboard). PHASE5.md §9.3.
func (m *Match) handleReconnect(v ctlReconnect) {
	m.dcTokensMu.RLock()
	pid, ok := m.dcTokens[v.Token]
	m.dcTokensMu.RUnlock()
	if !ok {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	slot, ok := m.slots[pid]
	if !ok || !slot.DC {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	if m.sim != nil && m.sim.ServerTick() >= slot.DCDeadlineTick {
		v.Reply <- joinResult{Err: ErrAuth}
		return
	}
	// Accept: re-bind writer, clear DC, mark token consumed (single-use
	// for this reconnect — a second drop will re-register a fresh entry).
	slot.DC = false
	slot.DCDeadlineTick = 0
	slot.out = v.Out
	m.dcTokensMu.Lock()
	delete(m.dcTokens, v.Token)
	m.dcTokensMu.Unlock()
	// Resync → MapInit → Snapshot → Scoreboard.
	m.sendFrameTo(slot, proto.MsgResync, proto.Resync{ServerTick: m.sim.ServerTick()})
	m.sendMapInitTo(slot)
	m.sendSnapshotTo(slot)
	m.sendScoreboardTo(slot)
	m.broadcastEvent(proto.Event{
		Kind:   uint8(proto.EventPlayerRejoin),
		Target: uint32(pid),
	})
	v.Reply <- joinResult{PlayerID: pid}
}

// dropDCSlot terminates a slot whose DC-grace deadline has passed.
// Broadcasts player_leave{reason=timeout}, removes the sim entity,
// invalidates the reconnect token, and removes the slot from m.slots.
// PHASE5.md §9.2.
func (m *Match) dropDCSlot(pid sim.EntityID, slot *Slot) {
	m.broadcastEvent(proto.Event{
		Kind:   uint8(proto.EventPlayerLeave),
		Target: uint32(pid),
		Reason: 1, // timeout
	})
	if m.sim != nil {
		m.sim.RemovePlayer(pid)
	}
	// Invalidate any DC token referencing this slot.
	m.dcTokensMu.Lock()
	for tok, owner := range m.dcTokens {
		if owner == pid {
			delete(m.dcTokens, tok)
		}
	}
	m.dcTokensMu.Unlock()
	// closeOnce ensures slot.closed is signalled once.
	slot.closeOnce.Do(func() { close(slot.closed) })
	m.markUnjoined(slot) // release the gauge if still joined
	delete(m.slots, pid)
}

// tokenForPlayer recovers the original joinToken for a slot. After
// the §4.7 single-use consumption in handleJoin, bySession no longer
// has the entry; we fall back to cfg.PlayerSlots which is immutable.
func (m *Match) tokenForPlayer(pid sim.EntityID) string {
	for _, p := range m.cfg.PlayerSlots {
		if p.PlayerID == pid {
			return p.Token
		}
	}
	return ""
}
