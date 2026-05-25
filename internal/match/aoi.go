package match

import (
	"sort"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// Phase 5 §11 — AOI selection bands (in tiles). MAZE_REVAMP.md §3.3 scales
// these tile-denominated radii ×2 with the finer 120×80 grid so the on-screen
// view covers the same relative area.
const (
	AOIInnerProj          = 20 // §5.3.1 priority 4
	AOIInnerSnipeGen      = 40 // §5.3.1 priorities 5 & 6
	AOIHysteresisProj     = 24 // outer band for hysteresis
	AOIHysteresisSnipeGen = 44 // outer band for hysteresis
	AOIMaxEntries         = 64 // §5.3
	subtilesPerTile       = 256
)

// chebyshevTiles returns max(|dx|, |dy|) in tile units between two
// subtile-space points.
func chebyshevTiles(ax, ay, bx, by int32) int {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		dx = dy
	}
	return int(dx) / subtilesPerTile
}

// buildPriorityAOISnapshotFor emits a §5.3.1 priority-ordered, cap-64,
// hysteresis-aware snapshot for the given (non-dead-cam) recipient.
// Phase 5 §11.
func (m *Match) buildPriorityAOISnapshotFor(recipient sim.EntityID) proto.Snapshot {
	entities := m.sim.Entities()
	// Recipient position. If recipient has no slab entry (eliminated but
	// somehow not in dead-cam yet — transient race), fall back to map
	// centre so we still emit a sensible snapshot.
	var rx, ry int32
	hasSelf := false
	for _, e := range entities {
		if e.ID == recipient && e.Flags&sim.FlagDead == 0 {
			rx, ry = e.X, e.Y
			hasSelf = true
			break
		}
	}
	if !hasSelf {
		rx = int32(m.sim.Width()) * subtilesPerTile / 2
		ry = int32(m.sim.Height()) * subtilesPerTile / 2
	}

	prev := m.aoiPrev[recipient]
	included := make(map[sim.EntityID]struct{}, AOIMaxEntries)
	out := make([]proto.Entity, 0, AOIMaxEntries)
	add := func(e sim.Entity) bool {
		if _, ok := included[e.ID]; ok {
			return true
		}
		if len(out) >= AOIMaxEntries {
			return false
		}
		flags := e.Flags & (sim.FlagDead | sim.FlagSpawnInvuln | sim.FlagTurbo)
		out = append(out, proto.Entity{
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
		included[e.ID] = struct{}{}
		return true
	}

	// Priority 1: self.
	for _, e := range entities {
		if e.ID == recipient && e.Flags&sim.FlagDead == 0 {
			add(e)
			break
		}
	}

	// Priority 2: other live players (all, distance-asc, ID tiebreak).
	priorityKind(entities, sim.KindPlayer, recipient, rx, ry, -1, add)

	// Priority 3: recipient's own projectiles (all, no distance gate).
	for _, e := range entities {
		if e.Kind != sim.KindProjectile || e.Flags&sim.FlagDead != 0 {
			continue
		}
		if m.projectileShooter(e.ID) == recipient {
			if !add(e) {
				goto pack
			}
		}
	}

	// Priority 4: projectiles within 10/12 tiles.
	priorityKindHyst(entities, sim.KindProjectile, recipient, rx, ry,
		AOIInnerProj, AOIHysteresisProj, prev, add)

	// Priority 5: generators within 20/22 tiles.
	priorityKindHyst(entities, sim.KindGenerator, recipient, rx, ry,
		AOIInnerSnipeGen, AOIHysteresisSnipeGen, prev, add)

	// Priority 6: snipes within 20/22 tiles.
	priorityKindHyst(entities, sim.KindSnipe, recipient, rx, ry,
		AOIInnerSnipeGen, AOIHysteresisSnipeGen, prev, add)

	// Priority 7: filler. Any remaining entity not already included,
	// distance-ascending, ID tiebreak, until cap.
	priorityFiller(entities, included, rx, ry, add)

pack:
	// Wire requires strictly-ascending EntityID per §6.3.3.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	// Bookkeeping for next-tick hysteresis.
	if m.aoiPrev == nil {
		m.aoiPrev = make(map[sim.EntityID]map[sim.EntityID]struct{}, len(m.slots))
	}
	m.aoiPrev[recipient] = included

	var yourID uint32
	if hasSelf {
		yourID = uint32(recipient)
	}
	return proto.Snapshot{
		ServerTick:        m.sim.ServerTick(),
		YourLastInputTick: m.sim.LastInputTick(recipient),
		YourEntityID:      yourID,
		Entities:          out,
	}
}

// priorityKind adds every live entity of `kind` (other than `self`)
// in distance-ascending / ID-tiebreak order, with no distance gate
// (used by priority 2: all other players). maxDist < 0 disables the
// distance filter entirely.
func priorityKind(entities []sim.Entity, kind sim.EntityKind, self sim.EntityID,
	rx, ry int32, maxDist int, add func(sim.Entity) bool,
) {
	type ranked struct {
		e    sim.Entity
		dist int
	}
	cands := make([]ranked, 0, 8)
	for _, e := range entities {
		if e.ID == self || e.Kind != kind || e.Flags&sim.FlagDead != 0 {
			continue
		}
		d := chebyshevTiles(rx, ry, e.X, e.Y)
		if maxDist >= 0 && d > maxDist {
			continue
		}
		cands = append(cands, ranked{e: e, dist: d})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].e.ID < cands[j].e.ID
	})
	for _, c := range cands {
		if !add(c.e) {
			return
		}
	}
}

// priorityKindHyst adds every live entity of `kind` whose distance is
// within `inner` tiles OR within `outer` tiles AND was in the previous
// snapshot (hysteresis). Distance-asc, ID-tiebreak.
func priorityKindHyst(entities []sim.Entity, kind sim.EntityKind, self sim.EntityID,
	rx, ry int32, inner, outer int,
	prev map[sim.EntityID]struct{}, add func(sim.Entity) bool,
) {
	type ranked struct {
		e    sim.Entity
		dist int
	}
	cands := make([]ranked, 0, 16)
	for _, e := range entities {
		if e.ID == self || e.Kind != kind || e.Flags&sim.FlagDead != 0 {
			continue
		}
		d := chebyshevTiles(rx, ry, e.X, e.Y)
		_, wasIn := prev[e.ID]
		include := d <= inner || (wasIn && d <= outer)
		if !include {
			continue
		}
		cands = append(cands, ranked{e: e, dist: d})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].e.ID < cands[j].e.ID
	})
	for _, c := range cands {
		if !add(c.e) {
			return
		}
	}
}

// priorityFiller adds remaining entities not in `included`, ordered
// by ascending distance from (rx, ry), ID tiebreak, until cap.
func priorityFiller(entities []sim.Entity, included map[sim.EntityID]struct{},
	rx, ry int32, add func(sim.Entity) bool,
) {
	type ranked struct {
		e    sim.Entity
		dist int
	}
	cands := make([]ranked, 0, 16)
	for _, e := range entities {
		if e.Flags&sim.FlagDead != 0 {
			continue
		}
		if _, ok := included[e.ID]; ok {
			continue
		}
		d := chebyshevTiles(rx, ry, e.X, e.Y)
		cands = append(cands, ranked{e: e, dist: d})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].e.ID < cands[j].e.ID
	})
	for _, c := range cands {
		if !add(c.e) {
			return
		}
	}
}

// projectileShooter returns the shooter EntityID for a projectile.
// Falls back to 0 if the slab record is absent. Used by §11.2
// priority 3 to identify "recipient's own projectiles".
func (m *Match) projectileShooter(pid sim.EntityID) sim.EntityID {
	if m.sim == nil {
		return 0
	}
	return m.sim.ProjectileShooter(pid)
}
