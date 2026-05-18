package sim

// LevelParams is the resolved per-(letter, number) tuning (PHASE3.md §6).
type LevelParams struct {
	SnipeSpeed        int32  // subtile/tick
	SnipeFireCooldown uint16 // ticks
	SnipeLeadFactor   uint8  // 0=no lead, 1=half lead, 2=full lead
	LOSRadius         int    // tiles
	GeneratorHP       uint8
	MaxSnipesTotal    int // global snipe cap
	InitialGenerators int // count at NewSim
	PlayerLives       int // Phase 5 forward-compat
}

// Phase 3 §6 snipe constants.
const (
	snipeHalfExt        int32  = 80
	snipeBaseSpeed      int32  = 12
	snipeBrutalSpeed    int32  = 15
	snipeBaseFireCD     uint16 = 20
	snipeHardFireCD     uint16 = 15
	generatorHPBase     uint8  = 3
	generatorHPBrutal   uint8  = 5
	spawnInvulnTicks    uint16 = 15 // 0.5s @ 30Hz
	snipeChaseLOSLostCD uint16 = 60 // ticks of no-LOS before falling back to PATROL
	bfsRecomputeEvery   uint32 = 15
	snipePatrolMinTimer uint16 = 30
	snipePatrolMaxTimer uint16 = 90
	emitCooldownMin     uint16 = 120 // 4s @ 30Hz
	emitCooldownMax     uint16 = 240 // 8s @ 30Hz
)

// LookupLevel returns the params for (letter, number).
// Caller must validate the inputs are in range; the function panics
// on out-of-range to surface programmer errors loudly.
func LookupLevel(letter byte, number int) LevelParams {
	// Canonicalise letter to upper.
	if letter >= 'a' && letter <= 'z' {
		letter -= 32
	}
	if letter < 'A' || letter > 'Z' {
		panic("sim.LookupLevel: letter out of range")
	}
	if number < 1 || number > 9 {
		panic("sim.LookupLevel: number out of range")
	}

	bucket := letterBucket(letter)
	p := LevelParams{
		SnipeSpeed:        snipeBaseSpeed,
		SnipeFireCooldown: snipeBaseFireCD,
		SnipeLeadFactor:   0,
		GeneratorHP:       generatorHPBase,
	}
	switch bucket {
	case bucketEasy: // A..F
		p.LOSRadius = 6
		p.SnipeLeadFactor = 0
	case bucketMedium: // G..M
		p.LOSRadius = 8
		p.SnipeLeadFactor = 1
	case bucketHard: // N..S
		p.LOSRadius = 10
		p.SnipeLeadFactor = 2
		p.SnipeFireCooldown = snipeHardFireCD
	case bucketBrutal: // T..Z
		p.LOSRadius = 14
		p.SnipeLeadFactor = 2
		p.SnipeFireCooldown = snipeHardFireCD
		p.SnipeSpeed = snipeBrutalSpeed
		p.GeneratorHP = generatorHPBrutal
	}

	// Number scaling (§6.2).
	p.MaxSnipesTotal = number * 6
	p.InitialGenerators = number + 2
	p.PlayerLives = max10(1, 10-number)

	return p
}

type levelBucket uint8

const (
	bucketEasy levelBucket = iota
	bucketMedium
	bucketHard
	bucketBrutal
)

func letterBucket(letter byte) levelBucket {
	switch {
	case letter >= 'A' && letter <= 'F':
		return bucketEasy
	case letter >= 'G' && letter <= 'M':
		return bucketMedium
	case letter >= 'N' && letter <= 'S':
		return bucketHard
	default: // 'T'..'Z'
		return bucketBrutal
	}
}

// validateLevel returns ErrInvalidLevel if the (Letter, Number)
// combination in Config is out of range or only one is set. The zero
// pair (0, 0) is valid and means "Phase 1 defaults — no level table".
func validateLevel(letter byte, number int) error {
	if letter == 0 && number == 0 {
		return nil
	}
	if letter == 0 || number == 0 {
		return ErrInvalidLevel
	}
	if letter >= 'a' && letter <= 'z' {
		letter -= 32
	}
	if letter < 'A' || letter > 'Z' {
		return ErrInvalidLevel
	}
	if number < 1 || number > 9 {
		return ErrInvalidLevel
	}
	return nil
}

func max10(a, b int) int {
	if a > b {
		return a
	}
	return b
}
