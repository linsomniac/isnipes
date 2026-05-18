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
	// KindSnipe = 4 reserved for Phase 3.
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

// Config governs NewSim. See §5.
type Config struct {
	Seed         uint32
	Width        int // 30..120
	Height       int // 20..80
	PlayerIDs    []EntityID
	NoRespawn    bool
	NoGenerators bool
}

// §7.0 phase-1 fixed parameters.
const (
	subtilePerTile = 256

	defaultWidth  = 60
	defaultHeight = 40

	maxInFlightProjectiles = 64
	playerHP               = 1
	generatorHP            = 3
	playerSpeed            = 16
	playerTurboSpeed       = 32
	projectileSpeed        = 32
	projectileLifetime     = 90
	fireCooldownTicks      = 6
	respawnTimerTicks      = 90

	playerHalfExt     = 96
	generatorHalfExt  = 112
	projectileHalfExt = 24

	maxEntities = 256

	minMapWidth  = 30
	maxMapWidth  = 120
	minMapHeight = 20
	maxMapHeight = 80

	maxPlayers = 8
)

// AIDEV-NOTE: build-time hitbox-fits-corridor assertion (§15 risk). A
// future bump above 112 would no longer fit a 1-tile corridor.
func init() {
	const corridorClearancePerSide = subtilePerTile/2 - playerHalfExt
	if corridorClearancePerSide < 0 {
		panic("isnipes/sim: playerHalfExt exceeds tile half-width")
	}
}

// §5 typed errors.
var (
	ErrNoPlayers          = errors.New("isnipes/sim: Config.PlayerIDs is empty")
	ErrTooManyPlayers     = errors.New("isnipes/sim: Config.PlayerIDs exceeds Phase 1 cap (8)")
	ErrZeroPlayerID       = errors.New("isnipes/sim: Config.PlayerIDs contains 0 (reserved sentinel)")
	ErrDuplicatePlayerID  = errors.New("isnipes/sim: Config.PlayerIDs contains duplicates")
	ErrPlayerIDNoHeadroom = errors.New("isnipes/sim: Config.PlayerIDs exceeds the §8 headroom bound")
	ErrInvalidMapSize     = errors.New("isnipes/sim: Config.Width/Height outside [30..120] × [20..80]")
)

// ErrIDExhausted indicates EntityID space exhaustion (§8).
var ErrIDExhausted = errors.New("isnipes/sim: EntityID space exhausted")
