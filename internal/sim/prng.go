package sim

import (
	"math/rand/v2"
)

// newMazePCG constructs the maze PRNG per §6.
// cfg.Seed is widened to uint64 and used as the high half; the
// second argument (0xA17ECAFE) is the pinned low half.
func newMazePCG(seed uint32) *rand.PCG {
	return rand.NewPCG(uint64(seed), 0xA17ECAFE)
}

// newEntityPCG constructs a per-entity PRNG per §6.
// cfg.Seed is the high half, the entity ID widened to uint64 is the
// low half.
func newEntityPCG(seed uint32, id EntityID) *rand.PCG {
	return rand.NewPCG(uint64(seed), uint64(id))
}

// entityPRNGs caches one *rand.PCG per EntityID. PCG (not *rand.Rand)
// is cached because only PCG implements encoding.BinaryMarshaler,
// which the fingerprint (§12.1 item 7) requires.
type entityPRNGs struct {
	seed  uint32
	cache map[EntityID]*rand.PCG
}

func newEntityPRNGs(seed uint32) *entityPRNGs {
	return &entityPRNGs{seed: seed, cache: make(map[EntityID]*rand.PCG)}
}

func (p *entityPRNGs) get(id EntityID) *rand.PCG {
	if pcg, ok := p.cache[id]; ok {
		return pcg
	}
	pcg := newEntityPCG(p.seed, id)
	p.cache[id] = pcg
	return pcg
}

// rand returns a *rand.Rand wrapper for the per-entity PCG; used as
// `entityRand(id).IntN(...)` per §6.
func (p *entityPRNGs) rand(id EntityID) *rand.Rand {
	return rand.New(p.get(id))
}
