package proto

// MatchJoin §6.3.1.
type MatchJoin struct {
	SchemaChecksum uint32
	Token          []byte // len ∈ [1, 32]
}

func (m MatchJoin) Encode(dst []byte) ([]byte, error) {
	if n := len(m.Token); n == 0 || n > 32 {
		return nil, ErrMalformed
	}
	var prefix [5]byte
	putLE32(prefix[:4], m.SchemaChecksum)
	prefix[4] = byte(len(m.Token))
	dst = append(dst, prefix[:]...)
	dst = append(dst, m.Token...)
	return dst, nil
}

func DecodeMatchJoin(payload []byte) (MatchJoin, error) {
	if len(payload) < 5 {
		return MatchJoin{}, ErrMalformed
	}
	sc := leUint32(payload[0:4])
	tlen := int(payload[4])
	if tlen == 0 || tlen > 32 {
		return MatchJoin{}, ErrMalformed
	}
	if len(payload) != 5+tlen {
		return MatchJoin{}, ErrMalformed
	}
	tok := make([]byte, tlen)
	copy(tok, payload[5:5+tlen])
	return MatchJoin{SchemaChecksum: sc, Token: tok}, nil
}

// Input §6.3.2.
type Input struct {
	ClientTick uint16
	Dir        uint8
	Turbo      uint8
	FireDir    uint8
}

func (m Input) Encode(dst []byte) ([]byte, error) {
	if m.Dir > 8 || m.FireDir > 8 || m.Turbo > 1 {
		return nil, ErrMalformed
	}
	var buf [5]byte
	putLE16(buf[0:2], m.ClientTick)
	buf[2] = m.Dir
	buf[3] = m.Turbo
	buf[4] = m.FireDir
	return append(dst, buf[:]...), nil
}

func DecodeInput(payload []byte) (Input, error) {
	if len(payload) != 5 {
		return Input{}, ErrMalformed
	}
	in := Input{
		ClientTick: leUint16(payload[0:2]),
		Dir:        payload[2],
		Turbo:      payload[3],
		FireDir:    payload[4],
	}
	if in.Dir > 8 || in.FireDir > 8 || in.Turbo > 1 {
		return Input{}, ErrMalformed
	}
	return in, nil
}

// Snapshot §6.3.3.
type Snapshot struct {
	ServerTick        uint32
	YourLastInputTick uint16
	YourEntityID      uint32
	Entities          []Entity
}

const snapshotPrefixLen = 4 + 2 + 4 + 1 // = 11

// allowedSnapshotFlagBits enumerates the Flag bits a server may emit
// on a Snapshot entity. Per SPEC §4.3.2's wire row: bit 0 = DEAD,
// bit 1 = SPAWN_INVULN (Phase 5), bit 2 = TURBO; bits 3–7 are reserved.
const allowedSnapshotFlagBits = FlagDead | FlagSpawnInvuln | FlagTurbo

func (m Snapshot) Encode(dst []byte) ([]byte, error) {
	if len(m.Entities) > MaxEntitiesPerSnapshot {
		return nil, ErrMalformed
	}
	// §6.3.3 invariants: ascending EntityID, only DEAD|SPAWN_INVULN|TURBO.
	for i, e := range m.Entities {
		if i > 0 && e.ID <= m.Entities[i-1].ID {
			return nil, ErrMalformed
		}
		if e.Flags&^allowedSnapshotFlagBits != 0 {
			return nil, ErrMalformed
		}
	}
	var prefix [snapshotPrefixLen]byte
	putLE32(prefix[0:4], m.ServerTick)
	putLE16(prefix[4:6], m.YourLastInputTick)
	putLE32(prefix[6:10], m.YourEntityID)
	prefix[10] = byte(len(m.Entities))
	dst = append(dst, prefix[:]...)
	var ebuf [EntityWireLen]byte
	for _, e := range m.Entities {
		putLE32(ebuf[0:4], e.ID)
		ebuf[4] = e.Kind
		ebuf[5] = e.HP
		ebuf[6] = e.Facing
		ebuf[7] = e.Flags
		putLE32(ebuf[8:12], uint32(e.X))
		putLE32(ebuf[12:16], uint32(e.Y))
		putLE16(ebuf[16:18], uint16(e.VX))
		putLE16(ebuf[18:20], uint16(e.VY))
		dst = append(dst, ebuf[:]...)
	}
	return dst, nil
}

func DecodeSnapshot(payload []byte) (Snapshot, error) {
	if len(payload) < snapshotPrefixLen {
		return Snapshot{}, ErrMalformed
	}
	s := Snapshot{
		ServerTick:        leUint32(payload[0:4]),
		YourLastInputTick: leUint16(payload[4:6]),
		YourEntityID:      leUint32(payload[6:10]),
	}
	n := int(payload[10])
	if n > MaxEntitiesPerSnapshot {
		return Snapshot{}, ErrMalformed
	}
	if len(payload) != snapshotPrefixLen+n*EntityWireLen {
		return Snapshot{}, ErrMalformed
	}
	s.Entities = make([]Entity, n)
	off := snapshotPrefixLen
	for i := 0; i < n; i++ {
		e := Entity{
			ID:     leUint32(payload[off+0 : off+4]),
			Kind:   payload[off+4],
			HP:     payload[off+5],
			Facing: payload[off+6],
			Flags:  payload[off+7],
			X:      int32(leUint32(payload[off+8 : off+12])),
			Y:      int32(leUint32(payload[off+12 : off+16])),
			VX:     int16(leUint16(payload[off+16 : off+18])),
			VY:     int16(leUint16(payload[off+18 : off+20])),
		}
		s.Entities[i] = e
		off += EntityWireLen
	}
	return s, nil
}

// Event §6.3.4.
type Event struct {
	Kind   uint8
	Actor  uint32
	Target uint32
	Reason uint8
}

const eventPayloadLen = 1 + 4 + 4 + 1 // = 10

func (m Event) Encode(dst []byte) ([]byte, error) {
	var buf [eventPayloadLen]byte
	buf[0] = m.Kind
	putLE32(buf[1:5], m.Actor)
	putLE32(buf[5:9], m.Target)
	buf[9] = m.Reason
	return append(dst, buf[:]...), nil
}

func DecodeEvent(payload []byte) (Event, error) {
	if len(payload) != eventPayloadLen {
		return Event{}, ErrMalformed
	}
	return Event{
		Kind:   payload[0],
		Actor:  leUint32(payload[1:5]),
		Target: leUint32(payload[5:9]),
		Reason: payload[9],
	}, nil
}

// Ping §6.3.5.
type Ping struct{ TsOrigin uint32 }

func (m Ping) Encode(dst []byte) ([]byte, error) {
	var buf [4]byte
	putLE32(buf[:], m.TsOrigin)
	return append(dst, buf[:]...), nil
}

func DecodePing(payload []byte) (Ping, error) {
	if len(payload) != 4 {
		return Ping{}, ErrMalformed
	}
	return Ping{TsOrigin: leUint32(payload)}, nil
}

// Pong §6.3.5.
type Pong struct{ TsOrigin, TsResponder uint32 }

func (m Pong) Encode(dst []byte) ([]byte, error) {
	var buf [8]byte
	putLE32(buf[0:4], m.TsOrigin)
	putLE32(buf[4:8], m.TsResponder)
	return append(dst, buf[:]...), nil
}

func DecodePong(payload []byte) (Pong, error) {
	if len(payload) != 8 {
		return Pong{}, ErrMalformed
	}
	return Pong{
		TsOrigin:    leUint32(payload[0:4]),
		TsResponder: leUint32(payload[4:8]),
	}, nil
}

// MatchOver §6.3.6.
type MatchOverEntry struct {
	PlayerID       uint32
	Score          int32
	LivesRemaining uint8
}

type MatchOver struct {
	FinalTick   uint32
	Reason      uint8
	WinnerIDOr0 uint32
	Entries     []MatchOverEntry
}

const matchOverPrefixLen = 4 + 1 + 4 + 1 // = 10
const matchOverEntryLen = 4 + 4 + 1      // = 9

func (m MatchOver) Encode(dst []byte) ([]byte, error) {
	if len(m.Entries) > 255 {
		return nil, ErrMalformed
	}
	var prefix [matchOverPrefixLen]byte
	putLE32(prefix[0:4], m.FinalTick)
	prefix[4] = m.Reason
	putLE32(prefix[5:9], m.WinnerIDOr0)
	prefix[9] = byte(len(m.Entries))
	dst = append(dst, prefix[:]...)
	var ebuf [matchOverEntryLen]byte
	for _, e := range m.Entries {
		putLE32(ebuf[0:4], e.PlayerID)
		putLE32(ebuf[4:8], uint32(e.Score))
		ebuf[8] = e.LivesRemaining
		dst = append(dst, ebuf[:]...)
	}
	return dst, nil
}

func DecodeMatchOver(payload []byte) (MatchOver, error) {
	if len(payload) < matchOverPrefixLen {
		return MatchOver{}, ErrMalformed
	}
	mo := MatchOver{
		FinalTick:   leUint32(payload[0:4]),
		Reason:      payload[4],
		WinnerIDOr0: leUint32(payload[5:9]),
	}
	n := int(payload[9])
	if len(payload) != matchOverPrefixLen+n*matchOverEntryLen {
		return MatchOver{}, ErrMalformed
	}
	mo.Entries = make([]MatchOverEntry, n)
	off := matchOverPrefixLen
	for i := 0; i < n; i++ {
		mo.Entries[i] = MatchOverEntry{
			PlayerID:       leUint32(payload[off+0 : off+4]),
			Score:          int32(leUint32(payload[off+4 : off+8])),
			LivesRemaining: payload[off+8],
		}
		off += matchOverEntryLen
	}
	return mo, nil
}

// MapInit §6.3.7.
type MapInit struct {
	Seed        uint32
	Width       uint16
	Height      uint16
	Packing     uint8
	PackedTiles []byte
}

const mapInitPrefixLen = 4 + 2 + 2 + 1 // = 9

func (m MapInit) Encode(dst []byte) ([]byte, error) {
	if len(m.PackedTiles)+mapInitPrefixLen > MaxFrameLen {
		return nil, ErrTooLong
	}
	var prefix [mapInitPrefixLen]byte
	putLE32(prefix[0:4], m.Seed)
	putLE16(prefix[4:6], m.Width)
	putLE16(prefix[6:8], m.Height)
	prefix[8] = m.Packing
	dst = append(dst, prefix[:]...)
	dst = append(dst, m.PackedTiles...)
	return dst, nil
}

func DecodeMapInit(payload []byte) (MapInit, error) {
	if len(payload) < mapInitPrefixLen {
		return MapInit{}, ErrMalformed
	}
	mi := MapInit{
		Seed:    leUint32(payload[0:4]),
		Width:   leUint16(payload[4:6]),
		Height:  leUint16(payload[6:8]),
		Packing: payload[8],
	}
	tiles := payload[mapInitPrefixLen:]
	mi.PackedTiles = make([]byte, len(tiles))
	copy(mi.PackedTiles, tiles)
	return mi, nil
}

// Scoreboard §6.3.8.
type ScoreboardEntry struct {
	PlayerID uint32
	Nick     string // utf8, 1..255 bytes
	Lives    uint8
	Score    int32
}

type Scoreboard struct {
	ServerTick uint32
	Entries    []ScoreboardEntry
}

const scoreboardPrefixLen = 4 + 1 // = 5

func (m Scoreboard) Encode(dst []byte) ([]byte, error) {
	if len(m.Entries) > 255 {
		return nil, ErrMalformed
	}
	var prefix [scoreboardPrefixLen]byte
	putLE32(prefix[0:4], m.ServerTick)
	prefix[4] = byte(len(m.Entries))
	dst = append(dst, prefix[:]...)
	for _, e := range m.Entries {
		if n := len(e.Nick); n == 0 || n > 255 {
			return nil, ErrMalformed
		}
		var idbuf [4]byte
		putLE32(idbuf[:], e.PlayerID)
		dst = append(dst, idbuf[:]...)
		dst = append(dst, byte(len(e.Nick)))
		dst = append(dst, e.Nick...)
		dst = append(dst, e.Lives)
		var sbuf [4]byte
		putLE32(sbuf[:], uint32(e.Score))
		dst = append(dst, sbuf[:]...)
	}
	return dst, nil
}

func DecodeScoreboard(payload []byte) (Scoreboard, error) {
	if len(payload) < scoreboardPrefixLen {
		return Scoreboard{}, ErrMalformed
	}
	sb := Scoreboard{ServerTick: leUint32(payload[0:4])}
	n := int(payload[4])
	off := scoreboardPrefixLen
	sb.Entries = make([]ScoreboardEntry, 0, n)
	for i := 0; i < n; i++ {
		if off+4+1 > len(payload) {
			return Scoreboard{}, ErrMalformed
		}
		pid := leUint32(payload[off : off+4])
		nl := int(payload[off+4])
		if nl == 0 {
			return Scoreboard{}, ErrMalformed
		}
		off += 5
		if off+nl+1+4 > len(payload) {
			return Scoreboard{}, ErrMalformed
		}
		nick := string(payload[off : off+nl])
		off += nl
		lives := payload[off]
		off++
		score := int32(leUint32(payload[off : off+4]))
		off += 4
		sb.Entries = append(sb.Entries, ScoreboardEntry{
			PlayerID: pid,
			Nick:     nick,
			Lives:    lives,
			Score:    score,
		})
	}
	if off != len(payload) {
		return Scoreboard{}, ErrMalformed
	}
	return sb, nil
}
