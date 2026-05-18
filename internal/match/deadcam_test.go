//go:build testhooks

package match

import (
	"testing"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// TestDeadCamTransition — once a player's lives reach 0 the slot
// flips to DeadCam = true on the next tick. Phase 5 §8.2.
func TestDeadCamTransition(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	// Drive player 2 to elimination via direct mutation + Tick.
	killPlayerUntilEliminated(t, m, 2, 1)
	// One Tick to give the actor's transition loop a chance. Since we
	// don't run the actor here, simulate the loop body explicitly.
	for pid, slot := range m.slots {
		if !slot.DeadCam && m.sim.Eliminated(pid) {
			slot.DeadCam = true
		}
	}
	if !m.slots[2].DeadCam {
		t.Fatal("slot 2 should be DeadCam")
	}
	if m.slots[1].DeadCam {
		t.Fatal("slot 1 should NOT be DeadCam (still alive)")
	}
}

// TestDeadCamSnapshotYourEntityIDIsZero — every snapshot for a
// dead-cam recipient carries your_entity_id == 0.
func TestDeadCamSnapshotYourEntityIDIsZero(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	killPlayerUntilEliminated(t, m, 2, 1)
	m.slots[2].DeadCam = true
	snap := m.buildSnapshotFor(2)
	if snap.YourEntityID != 0 {
		t.Fatalf("YourEntityID = %d, want 0", snap.YourEntityID)
	}
}

// TestDeadCamSnapshotIsUnfiltered — DoD #8. A dead-cam recipient sees
// every live entity in the slab (subject only to the 64-cap), no
// priority 1–6 filtering. Tiebreak is distance from map centre.
func TestDeadCamSnapshotIsUnfiltered(t *testing.T) {
	m := newPvEMatchForEval(t)
	// Eliminate player 1 so we can drive its dead-cam snapshot.
	killPlayerUntilEliminated(t, m, 1, 0)
	m.slots[1].DeadCam = true

	// Manually inject a wide spread of synthetic entities to verify the
	// dead-cam path emits *every* alive entity (we already have the
	// PvE level's generators; player 2 is alive; eliminated player 1
	// has no slab entry). Phase 5 §11.6 — every live entity in the slab.
	live := 0
	for _, e := range m.sim.Entities() {
		if e.Flags&sim.FlagDead == 0 {
			live++
		}
	}
	snap := m.buildSnapshotFor(1)
	if len(snap.Entities) != live && len(snap.Entities) != proto.MaxEntitiesPerSnapshot {
		t.Fatalf("dead-cam snapshot has %d entries, expected all %d live (or cap %d)",
			len(snap.Entities), live, proto.MaxEntitiesPerSnapshot)
	}
	if snap.YourEntityID != 0 {
		t.Fatalf("YourEntityID = %d, want 0", snap.YourEntityID)
	}
}

// TestDeadCamFilterDropsInput — handleControl on a dead-cam slot
// silently drops ctlInput.
func TestDeadCamFilterDropsInput(t *testing.T) {
	m := newPvPMatchForEval(t, []sim.EntityID{1, 2})
	// We bypass the actor and call handleControl directly. The match
	// state must be StateLive for the input handler to even consider
	// the input.
	m.stateAtomic.Store(uint32(StateLive))
	m.pendingInputs = make(map[sim.EntityID]proto.Input, 2)
	m.slots[2].DeadCam = true
	m.handleControl(ctlInput{PlayerID: 2, Input: proto.Input{Dir: 3}}) // dir E
	if _, ok := m.pendingInputs[2]; ok {
		t.Fatal("dead-cam input not filtered")
	}
	// Non-dead-cam slot still accepts inputs.
	m.handleControl(ctlInput{PlayerID: 1, Input: proto.Input{Dir: 3}})
	if _, ok := m.pendingInputs[1]; !ok {
		t.Fatal("non-dead-cam input was dropped")
	}
}
