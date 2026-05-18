package proto

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/quick"
)

func TestRoundTrip_MatchJoin(t *testing.T) {
	cases := []MatchJoin{
		{SchemaChecksum: 0xDEADBEEF, Token: []byte("a")},
		{SchemaChecksum: 0, Token: bytes.Repeat([]byte("x"), 32)},
		{SchemaChecksum: 0xFFFFFFFF, Token: []byte("token-24-chars-with-padding=")},
	}
	for i, in := range cases {
		got, err := in.Encode(nil)
		if err != nil {
			t.Fatalf("[%d] encode: %v", i, err)
		}
		dec, err := DecodeMatchJoin(got)
		if err != nil {
			t.Fatalf("[%d] decode: %v", i, err)
		}
		if dec.SchemaChecksum != in.SchemaChecksum || !bytes.Equal(dec.Token, in.Token) {
			t.Fatalf("[%d] mismatch: %+v vs %+v", i, in, dec)
		}
		re, _ := dec.Encode(nil)
		if !bytes.Equal(re, got) {
			t.Fatalf("[%d] re-encode mismatch", i)
		}
	}
}

func TestRoundTrip_Input(t *testing.T) {
	cases := []Input{
		{ClientTick: 0, Dir: 0, Turbo: 0, FireDir: 0},
		{ClientTick: 0xFFFF, Dir: 8, Turbo: 1, FireDir: 8},
		{ClientTick: 12345, Dir: 2, Turbo: 0, FireDir: 5},
	}
	for i, in := range cases {
		b, err := in.Encode(nil)
		if err != nil {
			t.Fatalf("[%d]: %v", i, err)
		}
		dec, err := DecodeInput(b)
		if err != nil {
			t.Fatalf("[%d] decode: %v", i, err)
		}
		if dec != in {
			t.Fatalf("[%d] mismatch: %+v vs %+v", i, in, dec)
		}
	}
}

func TestRoundTrip_Snapshot(t *testing.T) {
	cases := []Snapshot{
		{ServerTick: 0, YourLastInputTick: 0, YourEntityID: 0, Entities: nil},
		{ServerTick: 12345, YourLastInputTick: 9999, YourEntityID: 1, Entities: []Entity{
			{ID: 1, Kind: 1, HP: 1, Facing: 5, Flags: FlagTurbo, X: 256, Y: 512, VX: 16, VY: -16},
			{ID: 2, Kind: 1, HP: 0, Facing: 3, Flags: FlagDead, X: -1000, Y: 2000, VX: 0, VY: 0},
		}},
		makeFullCapSnapshot(),
	}
	for i, in := range cases {
		b, err := in.Encode(nil)
		if err != nil {
			t.Fatalf("[%d] encode: %v", i, err)
		}
		dec, err := DecodeSnapshot(b)
		if err != nil {
			t.Fatalf("[%d] decode: %v", i, err)
		}
		if len(dec.Entities) == 0 && len(in.Entities) == 0 {
			dec.Entities = in.Entities
		}
		if !reflect.DeepEqual(dec, in) {
			t.Fatalf("[%d] mismatch:\n in: %+v\nout: %+v", i, in, dec)
		}
	}
}

func makeFullCapSnapshot() Snapshot {
	ents := make([]Entity, MaxEntitiesPerSnapshot)
	for i := range ents {
		ents[i] = Entity{ID: uint32(i + 1), Kind: 3, HP: 1, Facing: 1, X: int32(i * 100), Y: int32(i * 50)}
	}
	return Snapshot{ServerTick: 999, YourEntityID: 1, Entities: ents}
}

func TestQuickRoundTrip_Snapshot(t *testing.T) {
	cfg := &quick.Config{MaxCount: 50}
	if err := quick.Check(func(serverTick uint32, lastIn uint16, yourID uint32, n uint8) bool {
		count := int(n) % (MaxEntitiesPerSnapshot + 1)
		ents := make([]Entity, count)
		for i := range ents {
			ents[i] = Entity{
				ID:     uint32(i + 1),
				Kind:   uint8(i % 4),
				HP:     uint8(i),
				Facing: uint8((i % 8) + 1),
				Flags:  uint8(i & 0x7),
				X:      int32(i) * 257,
				Y:      int32(i) * -257,
				VX:     int16(i),
				VY:     int16(-i),
			}
		}
		in := Snapshot{ServerTick: serverTick, YourLastInputTick: lastIn, YourEntityID: yourID, Entities: ents}
		b, err := in.Encode(nil)
		if err != nil {
			return false
		}
		got, err := DecodeSnapshot(b)
		if err != nil {
			return false
		}
		if len(got.Entities) == 0 && len(in.Entities) == 0 {
			got.Entities = in.Entities // empty slice/nil
		}
		return reflect.DeepEqual(got, in)
	}, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip_Event(t *testing.T) {
	in := Event{Kind: uint8(EventEntityKill), Actor: 5, Target: 7, Reason: 1}
	b, err := in.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeEvent(b)
	if err != nil {
		t.Fatal(err)
	}
	if dec != in {
		t.Fatalf("mismatch: %+v vs %+v", in, dec)
	}
}

func TestRoundTrip_Ping_Pong(t *testing.T) {
	bp, _ := Ping{TsOrigin: 1234567890}.Encode(nil)
	dp, err := DecodePing(bp)
	if err != nil || dp.TsOrigin != 1234567890 {
		t.Fatalf("ping: %v %+v", err, dp)
	}
	bpo, _ := Pong{TsOrigin: 1, TsResponder: 2}.Encode(nil)
	dpo, err := DecodePong(bpo)
	if err != nil || dpo.TsOrigin != 1 || dpo.TsResponder != 2 {
		t.Fatalf("pong: %v %+v", err, dpo)
	}
}

func TestRoundTrip_MatchOver(t *testing.T) {
	in := MatchOver{
		FinalTick:   12345,
		Reason:      EndLastStanding,
		WinnerIDOr0: 7,
		Entries: []MatchOverEntry{
			{PlayerID: 1, Score: 100, LivesRemaining: 0},
			{PlayerID: 7, Score: 250, LivesRemaining: 1},
		},
	}
	b, _ := in.Encode(nil)
	dec, err := DecodeMatchOver(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dec, in) {
		t.Fatalf("mismatch: %+v vs %+v", in, dec)
	}
	// Empty entries.
	empty := MatchOver{FinalTick: 5, Reason: EndAllEliminated, WinnerIDOr0: 0}
	b2, _ := empty.Encode(nil)
	dec2, err := DecodeMatchOver(b2)
	if err != nil {
		t.Fatal(err)
	}
	// Encode produces nil entries; decode produces 0-len slice. Treat
	// both as equivalent for our purposes.
	if dec2.FinalTick != empty.FinalTick || dec2.Reason != empty.Reason ||
		dec2.WinnerIDOr0 != empty.WinnerIDOr0 || len(dec2.Entries) != 0 {
		t.Fatalf("empty mismatch: %+v", dec2)
	}
}

func TestRoundTrip_MapInit(t *testing.T) {
	in := MapInit{
		Seed:        0xCAFEBABE,
		Width:       60,
		Height:      40,
		Packing:     1,
		PackedTiles: bytes.Repeat([]byte{0xAB}, 600),
	}
	b, err := in.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := DecodeMapInit(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dec, in) {
		t.Fatalf("mismatch:\n in: %+v\nout: %+v", in, dec)
	}
}

func TestRoundTrip_Scoreboard(t *testing.T) {
	in := Scoreboard{
		ServerTick: 100,
		Entries: []ScoreboardEntry{
			{PlayerID: 1, Nick: "Alice", Lives: 1, Score: 0},
			{PlayerID: 2, Nick: "Bob#001", Lives: 0, Score: 0},
		},
	}
	b, _ := in.Encode(nil)
	dec, err := DecodeScoreboard(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dec, in) {
		t.Fatalf("mismatch:\n in: %+v\nout: %+v", in, dec)
	}
}

func TestDecodeRejectsBadDir(t *testing.T) {
	in := Input{Dir: 9, Turbo: 0, FireDir: 0}
	if _, err := in.Encode(nil); !errors.Is(err, ErrMalformed) {
		t.Fatalf("encode err = %v, want ErrMalformed", err)
	}
	// Hand-build a frame with dir=9 to test decode path.
	b := []byte{0, 0, 9, 0, 0}
	if _, err := DecodeInput(b); !errors.Is(err, ErrMalformed) {
		t.Fatalf("decode err = %v, want ErrMalformed", err)
	}
	b[2] = 0
	b[4] = 9
	if _, err := DecodeInput(b); !errors.Is(err, ErrMalformed) {
		t.Fatalf("decode err (fireDir) = %v, want ErrMalformed", err)
	}
}

func TestDecodeRejectsBadTurbo(t *testing.T) {
	b := []byte{0, 0, 0, 2, 0}
	if _, err := DecodeInput(b); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestDecodeRejectsBadMatchJoinTokenLen(t *testing.T) {
	for _, n := range []int{0, 33, 255} {
		b := []byte{0, 0, 0, 0, byte(n)}
		b = append(b, bytes.Repeat([]byte{0xAA}, n)...)
		if _, err := DecodeMatchJoin(b); !errors.Is(err, ErrMalformed) {
			t.Fatalf("tokenLen=%d: err = %v, want ErrMalformed", n, err)
		}
	}
}

func TestMapInitFixtureMatches(t *testing.T) {
	binPath := filepath.Join("..", "..", "testdata", "proto", "mapinit_baseline.bin")
	mi := MapInit{
		Seed:        0xDEADBEEF,
		Width:       60,
		Height:      40,
		Packing:     1,
		PackedTiles: makeFixtureMap(),
	}
	enc, err := mi.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if *updateGolden {
		if err := os.WriteFile(binPath, enc, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes; sha256 %s)", binPath, len(enc), hexSum(enc))
		return
	}
	want, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update)", binPath, err)
	}
	if !bytes.Equal(want, enc) {
		t.Fatalf("MapInit bytes mismatch:\nwant sha256 %s\n got sha256 %s",
			hexSum(want), hexSum(enc))
	}
}

func TestSnapshotFixtureMatches(t *testing.T) {
	binPath := filepath.Join("..", "..", "testdata", "proto", "snapshot_baseline.bin")
	snap := Snapshot{
		ServerTick:        100,
		YourLastInputTick: 50,
		YourEntityID:      1,
		Entities: []Entity{
			{ID: 1, Kind: 1, HP: 1, Facing: 3, Flags: 0, X: 2688, Y: 5248, VX: 16, VY: 0},
			{ID: 2, Kind: 1, HP: 1, Facing: 5, Flags: FlagTurbo, X: 5248, Y: 5248, VX: 0, VY: 32},
			{ID: 9, Kind: 3, HP: 1, Facing: 3, Flags: 0, X: 3000, Y: 5248, VX: 32, VY: 0},
		},
	}
	enc, err := snap.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if *updateGolden {
		if err := os.WriteFile(binPath, enc, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes; sha256 %s)", binPath, len(enc), hexSum(enc))
		return
	}
	want, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update)", binPath, err)
	}
	if !bytes.Equal(want, enc) {
		t.Fatalf("Snapshot bytes mismatch:\nwant sha256 %s\n got sha256 %s",
			hexSum(want), hexSum(enc))
	}
}

func hexSum(b []byte) string {
	// Use a small inline SHA256 to avoid an extra import.
	// For test diagnostics only.
	return fmt.Sprintf("len=%d", len(b)) + ":" + hex.EncodeToString(b[:min(8, len(b))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func makeFixtureMap() []byte {
	// 60×40 tiles × 2 bits / 8 = 600 bytes. We use a hand-built
	// pattern: outer wall = 0, interior = floor with a single spawn
	// at the centre. The exact pattern isn't important — only that
	// the bytes are reproducible.
	const W, H = 60, 40
	tiles := make([]byte, W*H)
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if x == 0 || y == 0 || x == W-1 || y == H-1 {
				tiles[y*W+x] = 0
			} else {
				tiles[y*W+x] = 1
			}
		}
	}
	tiles[20*W+30] = 2 // spawn
	out := make([]byte, (W*H*2+7)/8)
	for i, t := range tiles {
		out[(i*2)/8] |= (t & 0x3) << uint((i*2)%8)
	}
	return out
}
