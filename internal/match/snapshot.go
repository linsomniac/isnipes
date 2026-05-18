package match

import (
	"sort"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// buildSnapshotFor builds the per-recipient Snapshot. If the recipient
// is in dead-cam (Phase 5 §8) it delegates to buildDeadCamSnapshotFor.
// Otherwise it emits the slab in ascending EntityID order (Phase 2
// behaviour; full AOI priority is Phase 5 §11, which lives in aoi.go
// once that file lands).
func (m *Match) buildSnapshotFor(recipient sim.EntityID) proto.Snapshot {
	if slot, ok := m.slots[recipient]; ok && slot.DeadCam {
		return m.buildDeadCamSnapshotFor(recipient)
	}
	entities := m.sim.Entities()
	wire := make([]proto.Entity, 0, len(entities))
	for _, e := range entities {
		// Phase 5: allow FlagSpawnInvuln through (§4.3.2 bit 1) so the
		// client can render the invuln cue.
		flags := e.Flags & (sim.FlagDead | sim.FlagSpawnInvuln | sim.FlagTurbo)
		wire = append(wire, proto.Entity{
			ID:     uint32(e.ID),
			Kind:   uint8(e.Kind),
			HP:     e.HP,
			Facing: uint8(e.Facing),
			Flags:  flags,
			X:      e.X,
			Y:      e.Y,
			VX:     e.VX,
			VY:     e.VY,
		})
	}
	if len(wire) > proto.MaxEntitiesPerSnapshot {
		wire = wire[:proto.MaxEntitiesPerSnapshot]
	}

	// Determine your_entity_id: 0 if the recipient is dead OR not in the
	// entity list (under NoRespawn=true a dead player has been GC'd).
	var yourID uint32
	alive := false
	for _, e := range entities {
		if e.ID == recipient && e.Flags&sim.FlagDead == 0 {
			alive = true
			break
		}
	}
	if alive {
		yourID = uint32(recipient)
	}

	return proto.Snapshot{
		ServerTick:        m.sim.ServerTick(),
		YourLastInputTick: m.sim.LastInputTick(recipient),
		YourEntityID:      yourID,
		Entities:          wire,
	}
}

// buildDeadCamSnapshotFor emits the §11.6 unfiltered dead-cam snapshot:
// every live entity, truncated at the 64-entity cap by ascending
// Chebyshev distance from the map centre, ID-ascending tiebreak.
// `your_entity_id` is always 0 (the player's body has been GC'd).
func (m *Match) buildDeadCamSnapshotFor(recipient sim.EntityID) proto.Snapshot {
	entities := m.sim.Entities()
	cx := int32(m.sim.Width()) * 256 / 2
	cy := int32(m.sim.Height()) * 256 / 2

	type ranked struct {
		ent  sim.Entity
		dist int32
	}
	cands := make([]ranked, 0, len(entities))
	for _, e := range entities {
		if e.Flags&sim.FlagDead != 0 {
			continue
		}
		dx := e.X - cx
		if dx < 0 {
			dx = -dx
		}
		dy := e.Y - cy
		if dy < 0 {
			dy = -dy
		}
		d := dx
		if dy > d {
			d = dy
		}
		// Convert to tiles so the comparison is in the same units as
		// §5.3.1's AOI bands (cheap; we keep subtile-units for the
		// comparator, but tile-rounding doesn't change ordering).
		cands = append(cands, ranked{ent: e, dist: d})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].ent.ID < cands[j].ent.ID
	})
	if len(cands) > proto.MaxEntitiesPerSnapshot {
		cands = cands[:proto.MaxEntitiesPerSnapshot]
	}
	wire := make([]proto.Entity, 0, len(cands))
	for _, c := range cands {
		e := c.ent
		flags := e.Flags & (sim.FlagDead | sim.FlagSpawnInvuln | sim.FlagTurbo)
		wire = append(wire, proto.Entity{
			ID:     uint32(e.ID),
			Kind:   uint8(e.Kind),
			HP:     e.HP,
			Facing: uint8(e.Facing),
			Flags:  flags,
			X:      e.X,
			Y:      e.Y,
			VX:     e.VX,
			VY:     e.VY,
		})
	}
	return proto.Snapshot{
		ServerTick:        m.sim.ServerTick(),
		YourLastInputTick: m.sim.LastInputTick(recipient),
		YourEntityID:      0,
		Entities:          wire,
	}
}

// buildSharedSnapshotPrefix returns the variable-position portion of
// the snapshot that is identical across recipients. Currently unused
// (we just rebuild per recipient), but kept as a hook for the
// §17 optimisation: precompute shared bytes once per tick, then per
// recipient rewrite only the `your_entity_id` and `your_last_input_tick`
// fields.
func (m *Match) buildSharedSnapshotPrefix() []byte { return nil }

// sortIDs sorts a slice of EntityIDs ascending in place.
func sortIDs(ids []sim.EntityID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}

// sortEntries sorts MatchOver entries by ascending PlayerID for
// deterministic output.
func sortEntries(e []proto.MatchOverEntry) {
	sort.Slice(e, func(i, j int) bool { return e[i].PlayerID < e[j].PlayerID })
}

func sortScoreboardEntries(e []proto.ScoreboardEntry) {
	sort.Slice(e, func(i, j int) bool { return e[i].PlayerID < e[j].PlayerID })
}
