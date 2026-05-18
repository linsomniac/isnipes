package sim

import "math"

// playerState carries per-player unexported fields. Indexed by EntityID
// in entityStore.
type playerState struct {
	fireCooldown  uint8
	lastDir       Dir
	lastInputTick uint16
	respawnAt     uint32 // server tick on which respawn fires; 0 = none
	deathX        int32
	deathY        int32
	hasRespawnAt  bool
}

// projectileState carries per-projectile unexported fields.
type projectileState struct {
	lifetime  uint16
	shooterID EntityID
}

// entityStore holds the fixed-capacity slab.
//
// AIDEV-NOTE: the slab is a flat slice (§8). Slots with ID==0 are free.
// Two histories that arrive at the same live-entity set MUST iterate
// in ID-sorted order for sim correctness — see §12 determinism rules.
type entityStore struct {
	slots [maxEntities]Entity
	// Per-kind sidecar maps, keyed by EntityID. We use maps rather than
	// embedding inside Entity to keep Entity layout matching the spec.
	// The sim never iterates these maps for state-affecting work, only
	// look-up by key (§12 determinism rules).
	players     map[EntityID]*playerState
	projectiles map[EntityID]*projectileState
	nextID      EntityID
}

func newEntityStore(initialNextID EntityID) *entityStore {
	return &entityStore{
		players:     make(map[EntityID]*playerState),
		projectiles: make(map[EntityID]*projectileState),
		nextID:      initialNextID,
	}
}

// alloc finds the first free slot and returns its index. The caller
// must populate slots[i] with a non-zero ID. Returns -1 if no slot is
// free.
func (es *entityStore) alloc() int {
	for i := 0; i < maxEntities; i++ {
		if es.slots[i].ID == 0 {
			return i
		}
	}
	return -1
}

// allocID returns the next monotonic EntityID. On exhaustion (would
// cross math.MaxUint32) returns (0, false). nextID itself is NEVER
// allowed to wrap; the sentinel test is "if issuing this ID would
// force nextID to cross math.MaxUint32, refuse" (§8).
func (es *entityStore) allocID() (EntityID, bool) {
	if es.nextID == 0 || es.nextID == math.MaxUint32 {
		return 0, false
	}
	id := es.nextID
	es.nextID++
	return id, true
}

// findByID returns the slot index for id, or -1.
func (es *entityStore) findByID(id EntityID) int {
	for i := 0; i < maxEntities; i++ {
		if es.slots[i].ID == id {
			return i
		}
	}
	return -1
}

// remove clears the slot for id and tears down sidecar maps.
func (es *entityStore) remove(id EntityID) {
	idx := es.findByID(id)
	if idx < 0 {
		return
	}
	delete(es.players, id)
	delete(es.projectiles, id)
	es.slots[idx] = Entity{}
}

// liveIDsSorted returns every occupied slot's ID, ascending. The
// returned slice is freshly allocated.
func (es *entityStore) liveIDsSorted() []EntityID {
	ids := make([]EntityID, 0, maxEntities)
	for i := 0; i < maxEntities; i++ {
		if es.slots[i].ID != 0 {
			ids = append(ids, es.slots[i].ID)
		}
	}
	sortEntityIDs(ids)
	return ids
}

func sortEntityIDs(ids []EntityID) {
	// Insertion sort: maxEntities is 256, but typical len is far
	// smaller, and insertion sort is allocation-free.
	for i := 1; i < len(ids); i++ {
		v := ids[i]
		j := i - 1
		for j >= 0 && ids[j] > v {
			ids[j+1] = ids[j]
			j--
		}
		ids[j+1] = v
	}
}
