package sim

import (
	"crypto/sha256"
	"encoding/binary"
)

// Fingerprint returns the 32-byte SHA-256 oracle defined in §12.1.
func (s *Sim) Fingerprint() [32]byte {
	h := sha256.New()
	var buf [16]byte

	// 1. serverTick (u32).
	binary.LittleEndian.PutUint32(buf[:4], s.serverTick)
	h.Write(buf[:4])

	// 2. Config.Seed (u32).
	binary.LittleEndian.PutUint32(buf[:4], s.cfg.Seed)
	h.Write(buf[:4])

	// 3. Width (u16), Height (u16).
	binary.LittleEndian.PutUint16(buf[:2], uint16(s.cfg.Width))
	h.Write(buf[:2])
	binary.LittleEndian.PutUint16(buf[:2], uint16(s.cfg.Height))
	h.Write(buf[:2])

	// 4. NoRespawn (u8).
	if s.cfg.NoRespawn {
		buf[0] = 1
	} else {
		buf[0] = 0
	}
	h.Write(buf[:1])

	// 5. nextID (u32) then quiesced (u8).
	binary.LittleEndian.PutUint32(buf[:4], uint32(s.store.nextID))
	h.Write(buf[:4])
	if s.quiesced {
		buf[0] = 1
	} else {
		buf[0] = 0
	}
	h.Write(buf[:1])

	// 6. Per occupied slot, ascending ID.
	ids := s.store.liveIDsSorted()
	for _, id := range ids {
		idx := s.store.findByID(id)
		e := &s.store.slots[idx]
		binary.LittleEndian.PutUint32(buf[:4], uint32(e.ID))
		h.Write(buf[:4])
		buf[0] = uint8(e.Kind)
		h.Write(buf[:1])
		buf[0] = e.HP
		h.Write(buf[:1])
		buf[0] = uint8(e.Facing)
		h.Write(buf[:1])
		buf[0] = e.Flags
		h.Write(buf[:1])
		binary.LittleEndian.PutUint32(buf[:4], uint32(e.X))
		h.Write(buf[:4])
		binary.LittleEndian.PutUint32(buf[:4], uint32(e.Y))
		h.Write(buf[:4])
		binary.LittleEndian.PutUint16(buf[:2], uint16(e.VX))
		h.Write(buf[:2])
		binary.LittleEndian.PutUint16(buf[:2], uint16(e.VY))
		h.Write(buf[:2])

		// Unexported per-entity state.
		if e.Kind == KindPlayer {
			ps := s.store.players[id]
			buf[0] = ps.fireCooldown
			h.Write(buf[:1])
			buf[0] = uint8(ps.lastDir)
			h.Write(buf[:1])
			binary.LittleEndian.PutUint16(buf[:2], ps.lastInputTick)
			h.Write(buf[:2])
			// respawnAt (u32). 0 if hasRespawnAt == false.
			var ra uint32
			if ps.hasRespawnAt {
				ra = ps.respawnAt
			}
			binary.LittleEndian.PutUint32(buf[:4], ra)
			h.Write(buf[:4])
			binary.LittleEndian.PutUint32(buf[:4], uint32(ps.deathX))
			h.Write(buf[:4])
			binary.LittleEndian.PutUint32(buf[:4], uint32(ps.deathY))
			h.Write(buf[:4])
			// projLifetime, projShooterID = 0 (not applicable).
			binary.LittleEndian.PutUint16(buf[:2], 0)
			h.Write(buf[:2])
			binary.LittleEndian.PutUint32(buf[:4], 0)
			h.Write(buf[:4])
		} else if e.Kind == KindProjectile {
			// fireCooldown, lastDir, lastInputTick, respawnAt, deathX,
			// deathY = 0.
			h.Write(make([]byte, 1+1+2+4+4+4))
			pst := s.store.projectiles[id]
			binary.LittleEndian.PutUint16(buf[:2], pst.lifetime)
			h.Write(buf[:2])
			binary.LittleEndian.PutUint32(buf[:4], uint32(pst.shooterID))
			h.Write(buf[:4])
		} else {
			// Generator: all unexported are zero.
			h.Write(make([]byte, 1+1+2+4+4+4+2+4))
		}
	}

	// 7. Per-entity PCG marshalled bytes, ascending EntityID.
	for _, id := range entityPRNGKeysSorted(s.entityPRNGs) {
		pcg := s.entityPRNGs.cache[id]
		blob, err := pcg.MarshalBinary()
		if err != nil {
			continue
		}
		h.Write(blob)
	}

	// 8. Packed map bytes (internal immutable copy).
	h.Write(s.mapBytes)

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func entityPRNGKeysSorted(p *entityPRNGs) []EntityID {
	keys := make([]EntityID, 0, len(p.cache))
	for k := range p.cache {
		keys = append(keys, k)
	}
	sortEntityIDs(keys)
	return keys
}
