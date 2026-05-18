package match

import (
	"sort"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// buildSnapshotFor builds the Phase 2 Snapshot for one recipient.
// Per §6.3.3, entities are emitted in ascending EntityID order; Phase 2
// has no AOI filtering, so every entity goes out.
func (m *Match) buildSnapshotFor(recipient sim.EntityID) proto.Snapshot {
	entities := m.sim.Entities()
	wire := make([]proto.Entity, 0, len(entities))
	for _, e := range entities {
		// Sanitise flag bits to Phase 2's allowed set.
		flags := e.Flags & (sim.FlagDead | sim.FlagTurbo)
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
