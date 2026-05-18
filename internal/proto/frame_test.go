package proto

import (
	"bytes"
	"errors"
	"testing"
	"testing/quick"
)

func TestFrameHeaderRoundTrip(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}
	if err := quick.Check(func(typ uint8, flags uint8, seq, ack uint16, payloadLen uint16) bool {
		if payloadLen > MaxFrameLen {
			return true // skip impossible
		}
		hdr := FrameHeader{Type: MsgType(typ), Flags: flags, Seq: seq, Ack: ack, Len: payloadLen}
		payload := make([]byte, payloadLen)
		for i := range payload {
			payload[i] = byte(i)
		}
		var buf []byte
		buf, err := EncodeFrame(buf, hdr, payload)
		if err != nil {
			return false
		}
		gotHdr, gotPayload, consumed, err := DecodeFrame(buf)
		if err != nil {
			return false
		}
		if consumed != len(buf) {
			return false
		}
		if gotHdr.Type != hdr.Type || gotHdr.Flags != hdr.Flags ||
			gotHdr.Seq != hdr.Seq || gotHdr.Ack != hdr.Ack ||
			gotHdr.Len != uint16(len(payload)) {
			return false
		}
		return bytes.Equal(gotPayload, payload)
	}, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestFrameHeaderRejectsTruncated(t *testing.T) {
	hdr := FrameHeader{Type: MsgInput, Len: 5}
	var buf []byte
	buf, err := EncodeFrame(buf, hdr, []byte{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	// Truncate to less than the header.
	if _, _, _, err := DecodeFrame(buf[:4]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	// Truncate the payload by 1 byte.
	if _, _, _, err := DecodeFrame(buf[:len(buf)-1]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated for short payload", err)
	}
}

func TestEncodeFrameRejectsTooLong(t *testing.T) {
	payload := make([]byte, MaxFrameLen+1)
	_, err := EncodeFrame(nil, FrameHeader{}, payload)
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
}

func TestFrameHeaderEmptyPayload(t *testing.T) {
	hdr := FrameHeader{Type: MsgPing, Seq: 5, Ack: AckNone, Len: 0}
	buf, err := EncodeFrame(nil, hdr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(buf) != FrameHeaderLen {
		t.Fatalf("len = %d, want %d", len(buf), FrameHeaderLen)
	}
	got, payload, _, err := DecodeFrame(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len != 0 || len(payload) != 0 {
		t.Fatalf("non-empty payload: %v %d", payload, got.Len)
	}
}
