package sim

// packTiles encodes the maze tile grid into the §4.3.4 packed-tile
// format (2 bits per tile, little-endian within each byte, row-major).
// The returned slice has length ceil(W*H*2/8).
func packTiles(m *maze) []byte {
	n := m.W * m.H
	out := make([]byte, (n*2+7)/8)
	for i := 0; i < n; i++ {
		v := byte(m.tiles[i]) & 0x3
		byteIdx := (i * 2) / 8
		bitIdx := (i * 2) % 8
		out[byteIdx] |= v << bitIdx
	}
	return out
}
