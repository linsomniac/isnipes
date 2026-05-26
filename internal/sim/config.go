package sim

import "errors"

// Tile codes match the wire encoding in §4.3.4 of SPEC.md.
type Tile uint8

const (
	TileWall           Tile = 0
	TileFloor          Tile = 1
	TileSpawnPlayer    Tile = 2
	TileSpawnGenerator Tile = 3
)

// EntityKind matches the wire enum in §4.3.2.
type EntityKind uint8

const (
	KindPlayer     EntityKind = 1
	KindGenerator  EntityKind = 2
	KindProjectile EntityKind = 3
	KindSnipe      EntityKind = 4 // Phase 3
)

// Direction codes match the Dir8 enum in §4.3.2.
type Dir uint8

const (
	DirIdle Dir = 0
	DirN    Dir = 1
	DirNE   Dir = 2
	DirE    Dir = 3
	DirSE   Dir = 4
	DirS    Dir = 5
	DirSW   Dir = 6
	DirW    Dir = 7
	DirNW   Dir = 8
)

// EntityID 0 is the reserved "no entity" sentinel (§3.2).
type EntityID uint32

// Entity.Flags bits — match the wire Entity flags in §4.3.2.
const (
	FlagDead        uint8 = 1 << 0
	FlagSpawnInvuln uint8 = 1 << 1 // reserved for Phase 5
	FlagTurbo       uint8 = 1 << 2
)

// Entity is the canonical sim record (§5).
type Entity struct {
	ID     EntityID
	Kind   EntityKind
	HP     uint8
	Facing Dir
	Flags  uint8
	X, Y   int32 // subtile coordinates (§3.1)
	VX, VY int16 // last applied velocity in subtile units/tick
}

// PlayerInput is one tick of intent for one player (§5).
type PlayerInput struct {
	PlayerID   EntityID
	Dir        Dir
	Turbo      bool
	FireDir    Dir
	ClientTick uint16

	// Phase 4 §5.1: per-player lag-compensation options. Zero value
	// (OWTTicks == 0) means present-time hit detection — the existing
	// P1/P3 behaviour. Callers that care about lag-comp populate this
	// with the shooter's per-connection OWT estimate.
	LagComp FireOptions
}

// FireOptions augments the queued fire input with optional lag-comp.
// The zero value yields no rewind. PHASE4.md §5.1.
type FireOptions struct {
	// OWTTicks is the per-shooter one-way-time in sim ticks, clamped
	// to LagCompTicks inside the sim.
	OWTTicks uint8
}

// Event mirrors §4.3.2 Event{kind,actor,target,reason}.
type Event struct {
	Kind   uint8
	Actor  EntityID
	Target EntityID
	Reason uint8
}

// Event.Kind values — match the wire enum in §4.3.2. Phase 1 emits
// only the first four; later constants are reserved here.
const (
	EventEntitySpawn        uint8 = 0x01
	EventEntityHit          uint8 = 0x02
	EventEntityKill         uint8 = 0x03
	EventGeneratorDestroyed uint8 = 0x04
	EventPlayerJoin         uint8 = 0x05
	EventPlayerLeave        uint8 = 0x06
	EventPlayerDC           uint8 = 0x07
	EventPlayerRejoin       uint8 = 0x08
	EventMatchStarting      uint8 = 0x09
	EventMatchStarted       uint8 = 0x0A
	EventMatchEnd           uint8 = 0x0B
	EventChatRelay          uint8 = 0x0C
	EventRespawnPending     uint8 = 0x0D
)

// Config governs NewSim. See §5 of PHASE1.md (Phase 1 fields) and §5
// of PHASE3.md (Phase 3 Level fields).
type Config struct {
	Seed         uint32
	Width        int // 30..120
	Height       int // 20..80
	PlayerIDs    []EntityID
	NoRespawn    bool
	NoGenerators bool

	// LevelLetter is 'A'..'Z' (canonicalised upper). The zero value
	// (byte 0) means "Phase 1 defaults: no snipes, no level-table
	// parameters". Phase 2's PvP-only match passes 0/0.
	LevelLetter byte
	// LevelNumber is 1..9. Must be set whenever LevelLetter is set;
	// mismatches return ErrInvalidLevel from NewSim.
	LevelNumber int
}

// §7.0 phase-1 fixed parameters.
//
// MAZE_REVAMP.md: the maze is a wide-corridor braided maze on a 120×80
// default grid. Corridors are corridorWidth tiles wide separated by 1-tile
// walls, and entity sizes/speeds + tile-denominated distances are scaled so
// the player spans ~2 tiles (and is ~2× a snipe), making a 1-tile wall render
// thin against a wide corridor — the original Snipes look.
const (
	subtilePerTile = 256

	defaultWidth  = 120
	defaultHeight = 80

	// corridorWidth is the floor width (in tiles) of a maze corridor; the
	// coarse-cell pitch is corridorWidth+1 (corridor + one thin wall).
	corridorWidth = 6

	maxInFlightProjectiles = 64
	playerHP               = 1
	generatorHP            = 3
	// Movement speeds (subtile units/tick). 3.5× the post-MAZE_REVAMP base so
	// the player traverses the wide-corridor map at a lively pace. Capped well
	// under subtilePerTile (256): the single-lead-edge wall sweep in
	// moveAndSlide only checks the destination tile, so a per-axis step ≥ 256
	// could tunnel a 1-tile wall. Turbo == projectileSpeed is preserved (SPEC
	// §3.3): a turbo player never outruns their own shot.
	playerSpeed        = 112
	playerTurboSpeed   = 224
	projectileSpeed    = 224
	projectileLifetime = 90
	fireCooldownTicks  = 6
	respawnTimerTicks  = 90

	playerHalfExt     = 256
	generatorHalfExt  = 256
	projectileHalfExt = 48

	maxEntities = 256

	minMapWidth  = 50
	maxMapWidth  = 120
	minMapHeight = 40
	maxMapHeight = 80

	maxPlayers = 8
)

// AIDEV-NOTE: build-time hitbox-fits-corridor assertion. The wide-corridor
// maze (MAZE_REVAMP.md) makes corridors corridorWidth tiles wide; the player
// half-extent must fit within a corridor's half-width with clearance to spare.
func init() {
	const corridorClearancePerSide = corridorWidth*subtilePerTile/2 - playerHalfExt
	if corridorClearancePerSide < 0 {
		panic("isnipes/sim: playerHalfExt exceeds corridor half-width")
	}
}

// §5 typed errors.
var (
	ErrNoPlayers          = errors.New("isnipes/sim: Config.PlayerIDs is empty")
	ErrTooManyPlayers     = errors.New("isnipes/sim: Config.PlayerIDs exceeds Phase 1 cap (8)")
	ErrZeroPlayerID       = errors.New("isnipes/sim: Config.PlayerIDs contains 0 (reserved sentinel)")
	ErrDuplicatePlayerID  = errors.New("isnipes/sim: Config.PlayerIDs contains duplicates")
	ErrPlayerIDNoHeadroom = errors.New("isnipes/sim: Config.PlayerIDs exceeds the §8 headroom bound")
	ErrInvalidMapSize     = errors.New("isnipes/sim: Config.Width/Height outside [50..120] × [40..80]")
	ErrInvalidLevel       = errors.New("isnipes/sim: Config.LevelLetter / LevelNumber out of range or only one set")
)

// ErrIDExhausted indicates EntityID space exhaustion (§8).
var ErrIDExhausted = errors.New("isnipes/sim: EntityID space exhausted")
