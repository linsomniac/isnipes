package sim

import (
	"crypto/sha256"
)

// putLE16/32 write little-endian scalars without importing
// encoding/binary, which §4 forbids inside internal/sim.
func putLE16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// Fingerprint returns the 32-byte SHA-256 oracle defined in §12.1.
func (s *Sim) Fingerprint() [32]byte {
	h := sha256.New()
	var buf [16]byte

	// 1. serverTick (u32).
	putLE32(buf[:4], s.serverTick)
	h.Write(buf[:4])

	// 2. Config.Seed (u32).
	putLE32(buf[:4], s.cfg.Seed)
	h.Write(buf[:4])

	// 3. Width (u16), Height (u16).
	putLE16(buf[:2], uint16(s.cfg.Width))
	h.Write(buf[:2])
	putLE16(buf[:2], uint16(s.cfg.Height))
	h.Write(buf[:2])

	// 4. NoRespawn (u8).
	if s.cfg.NoRespawn {
		buf[0] = 1
	} else {
		buf[0] = 0
	}
	h.Write(buf[:1])

	// 5. nextID (u32) then quiesced (u8).
	putLE32(buf[:4], uint32(s.store.nextID))
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
		putLE32(buf[:4], uint32(e.ID))
		h.Write(buf[:4])
		buf[0] = uint8(e.Kind)
		h.Write(buf[:1])
		buf[0] = e.HP
		h.Write(buf[:1])
		buf[0] = uint8(e.Facing)
		h.Write(buf[:1])
		buf[0] = e.Flags
		h.Write(buf[:1])
		putLE32(buf[:4], uint32(e.X))
		h.Write(buf[:4])
		putLE32(buf[:4], uint32(e.Y))
		h.Write(buf[:4])
		putLE16(buf[:2], uint16(e.VX))
		h.Write(buf[:2])
		putLE16(buf[:2], uint16(e.VY))
		h.Write(buf[:2])

		// Unexported per-entity state.
		if e.Kind == KindPlayer {
			ps := s.store.players[id]
			buf[0] = ps.fireCooldown
			h.Write(buf[:1])
			buf[0] = uint8(ps.lastDir)
			h.Write(buf[:1])
			putLE16(buf[:2], ps.lastInputTick)
			h.Write(buf[:2])
			// respawnAt (u32). 0 if hasRespawnAt == false.
			var ra uint32
			if ps.hasRespawnAt {
				ra = ps.respawnAt
			}
			putLE32(buf[:4], ra)
			h.Write(buf[:4])
			putLE32(buf[:4], uint32(ps.deathX))
			h.Write(buf[:4])
			putLE32(buf[:4], uint32(ps.deathY))
			h.Write(buf[:4])
			// projLifetime, projShooterID = 0 (not applicable).
			putLE16(buf[:2], 0)
			h.Write(buf[:2])
			putLE32(buf[:4], 0)
			h.Write(buf[:4])
		} else if e.Kind == KindProjectile {
			// fireCooldown, lastDir, lastInputTick, respawnAt, deathX,
			// deathY = 0.
			h.Write(make([]byte, 1+1+2+4+4+4))
			pst := s.store.projectiles[id]
			putLE16(buf[:2], pst.lifetime)
			h.Write(buf[:2])
			putLE32(buf[:4], uint32(pst.shooterID))
			h.Write(buf[:4])
		} else {
			// Generator / Snipe: pad the P1 player+projectile block.
			h.Write(make([]byte, 1+1+2+4+4+4+2+4))
		}

		// Phase 3 §15.1: per-snipe + per-generator extension bytes.
		// Emit zero-padded blocks for non-applicable kinds so the
		// hash is stable across (a sim that has snipes/gens) and
		// (a sim that doesn't).
		if e.Kind == KindSnipe {
			ss := s.store.snipes[id]
			buf[0] = byte(ss.aiState)
			h.Write(buf[:1])
			putLE16(buf[:2], ss.aiTimer)
			h.Write(buf[:2])
			putLE32(buf[:4], ss.lastBFSTick)
			h.Write(buf[:4])
			pathLen := byte(len(ss.bfsPath))
			if pathLen > 3 {
				pathLen = 3
			}
			buf[0] = pathLen
			h.Write(buf[:1])
			for i := 0; i < int(pathLen); i++ {
				buf[0] = byte(ss.bfsPath[i].X)
				buf[1] = byte(ss.bfsPath[i].Y)
				h.Write(buf[:2])
			}
			// Pad missing path entries with zeros.
			for i := int(pathLen); i < 3; i++ {
				h.Write([]byte{0, 0})
			}
			putLE32(buf[:4], uint32(ss.chaseTarget))
			h.Write(buf[:4])
			putLE16(buf[:2], ss.losLostTicks)
			h.Write(buf[:2])
			putLE16(buf[:2], ss.fireCooldown)
			h.Write(buf[:2])
			putLE32(buf[:4], uint32(ss.parentGen))
			h.Write(buf[:4])
		} else {
			// Pad the snipe block: 1 + 2 + 4 + 1 + 3*2 + 4 + 2 + 2 + 4 = 26 bytes.
			h.Write(make([]byte, 26))
		}
		if e.Kind == KindGenerator {
			gs := s.store.generators[id]
			var ec uint16
			var rot uint8
			if gs != nil {
				ec = gs.emitCooldown
				rot = gs.rotation
			}
			putLE16(buf[:2], ec)
			h.Write(buf[:2])
			buf[0] = rot
			h.Write(buf[:1])
		} else {
			h.Write(make([]byte, 3))
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
