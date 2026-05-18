package proto

// FrameHeader is the §6.1 fixed-size header that prefixes every
// binary message on the match WS.
type FrameHeader struct {
	Type  MsgType
	Flags uint8
	Seq   uint16
	Ack   uint16
	Len   uint16
}

// AckNone is the "haven't received anything yet" sentinel for the
// first frame on a connection (§6.1).
const AckNone uint16 = 0xFFFF

// putLE16 / putLE32 are inline little-endian writers. We avoid
// encoding/binary to keep the import graph stdlib-only and to match
// the convention from internal/sim/fingerprint.go.
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

func leUint16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }

func leUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// EncodeFrame writes a frame header plus payload to dst, returning
// dst extended with the bytes. The caller must have already encoded
// the payload separately and passed it as `payload`; len(payload)
// is automatically stamped into the header's Len field.
//
// Returns ErrTooLong if len(payload) > MaxFrameLen.
func EncodeFrame(dst []byte, hdr FrameHeader, payload []byte) ([]byte, error) {
	if len(payload) > MaxFrameLen {
		return nil, ErrTooLong
	}
	var hbuf [FrameHeaderLen]byte
	hbuf[0] = byte(hdr.Type)
	hbuf[1] = hdr.Flags
	putLE16(hbuf[2:4], hdr.Seq)
	putLE16(hbuf[4:6], hdr.Ack)
	putLE16(hbuf[6:8], uint16(len(payload)))
	dst = append(dst, hbuf[:]...)
	dst = append(dst, payload...)
	return dst, nil
}

// DecodeFrame parses one binary frame from a WS message. The caller
// passes the entire WS message bytes; the function enforces PHASE2 §6.1
// "one WS message = one app frame" by rejecting trailing bytes.
//
// Returns the header, the payload slice (which aliases src — copy if
// you need to retain it past src's lifetime), and the number of bytes
// consumed (always == len(src) on success).
//
// ErrTruncated: src is shorter than FrameHeaderLen, or shorter than
// FrameHeaderLen + header.Len.
// ErrMalformed: trailing bytes beyond FrameHeaderLen + header.Len.
func DecodeFrame(src []byte) (FrameHeader, []byte, int, error) {
	if len(src) < FrameHeaderLen {
		return FrameHeader{}, nil, 0, ErrTruncated
	}
	hdr := FrameHeader{
		Type:  MsgType(src[0]),
		Flags: src[1],
		Seq:   leUint16(src[2:4]),
		Ack:   leUint16(src[4:6]),
		Len:   leUint16(src[6:8]),
	}
	end := FrameHeaderLen + int(hdr.Len)
	if len(src) < end {
		return FrameHeader{}, nil, 0, ErrTruncated
	}
	if len(src) > end {
		return FrameHeader{}, nil, 0, ErrMalformed
	}
	return hdr, src[FrameHeaderLen:end], end, nil
}
