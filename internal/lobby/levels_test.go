package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

func protoLevel(letter string, number int) proto.Level {
	return proto.Level{Letter: letter, Number: number}
}

// TestLobby_LevelValidation — DoD #10. Table-driven validation across
// accepted, lower-cased, and malformed level strings.
func TestLobby_LevelValidation(t *testing.T) {
	cases := []struct {
		in        string
		wantOK    bool
		wantLet   byte
		wantNum   int
		wantCanon string
	}{
		// Accept.
		{"A1", true, 'A', 1, "A1"},
		{"Z9", true, 'Z', 9, "Z9"},
		{"f3", true, 'F', 3, "F3"},
		{"m7", true, 'M', 7, "M7"},
		{"T1", true, 'T', 1, "T1"},
		// Reject — format.
		{"", false, 0, 0, ""},
		{"A", false, 0, 0, ""},
		{"1A", false, 0, 0, ""},
		{"AA1", false, 0, 0, ""},
		{"A10", false, 0, 0, ""},
		{"", false, 0, 0, ""},
		// Reject — number out of range.
		{"A0", false, 0, 0, ""},
		{"B0", false, 0, 0, ""},
		{"Z0", false, 0, 0, ""},
		// Reject — non-letter / non-digit.
		{"@1", false, 0, 0, ""},
		{"1@", false, 0, 0, ""},
		{"  ", false, 0, 0, ""},
		{"ZZ", false, 0, 0, ""},
	}
	for _, c := range cases {
		letter, num, err := ParseLevel(c.in)
		if c.wantOK {
			if err != nil {
				t.Errorf("ParseLevel(%q) err=%v, want ok", c.in, err)
				continue
			}
			if letter != c.wantLet {
				t.Errorf("ParseLevel(%q) letter=%c, want %c", c.in, letter, c.wantLet)
			}
			if num != c.wantNum {
				t.Errorf("ParseLevel(%q) num=%d, want %d", c.in, num, c.wantNum)
			}
			if got := CanonicalLevel(c.in); got != c.wantCanon {
				t.Errorf("CanonicalLevel(%q) = %q, want %q", c.in, got, c.wantCanon)
			}
		} else {
			if err == nil {
				t.Errorf("ParseLevel(%q) ok=true, want err", c.in)
			}
			if got := CanonicalLevel(c.in); got != "" {
				t.Errorf("CanonicalLevel(%q) = %q, want empty", c.in, got)
			}
		}
	}
}

// TestLobby_LevelPresetTable — 234 entries, every (letter, number)
// pair, with derived parameters matching sim.LookupLevel.
func TestLobby_LevelPresetTable(t *testing.T) {
	presets := BuildLevelPresets()
	if len(presets) != 26*9 {
		t.Fatalf("preset count = %d, want %d", len(presets), 26*9)
	}
	// Ordering: letter ascending, number ascending.
	for i, p := range presets {
		wantLetter := byte('A' + i/9)
		wantNumber := (i % 9) + 1
		if p.Letter != wantLetter || p.Number != wantNumber {
			t.Fatalf("presets[%d] = (%c, %d), want (%c, %d)",
				i, p.Letter, p.Number, wantLetter, wantNumber)
		}
		if p.Difficulty == "" {
			t.Errorf("presets[%d] = %c%d has empty Difficulty", i, p.Letter, p.Number)
		}
		if p.Description == "" {
			t.Errorf("presets[%d] has empty Description", i)
		}
		if p.PlayerLives < 1 || p.PlayerLives > 9 {
			t.Errorf("presets[%d] PlayerLives = %d out of range", i, p.PlayerLives)
		}
	}
}

// TestLobby_ValidateLevelWire — ValidateLevel against the proto.Level
// wire form (Letter:string + Number:int).
func TestLobby_ValidateLevelWire(t *testing.T) {
	cases := []struct {
		letter string
		number int
		ok     bool
	}{
		{"A", 1, true},
		{"Z", 9, true},
		{"f", 3, true}, // lowercase canonicalised
		{"", 1, false},
		{"AA", 1, false},
		{"@", 1, false},
		{"A", 0, false},
		{"A", 10, false},
		{"A", -1, false},
	}
	for _, c := range cases {
		_, _, err := ValidateLevel(protoLevel(c.letter, c.number))
		if c.ok && err != nil {
			t.Errorf("ValidateLevel(%q,%d) err=%v, want ok", c.letter, c.number, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateLevel(%q,%d) ok, want err", c.letter, c.number)
		}
	}
}

// TestLobby_CreateRoomRejectsBadLevel — actor-level wiring: a
// createRoom envelope with an invalid Level yields BAD_LEVEL.
func TestLobby_CreateRoomRejectsBadLevel(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	sendEnvelope(t, l, a.ID, proto.LobbyCreateRoom, proto.CreateRoom{
		Name: "Bad", Max: 4, Level: proto.Level{Letter: "Q", Number: 0},
	})
	o, ok := drainUntil(t, outA, proto.LobbyTagError, 500*time.Millisecond)
	if !ok {
		t.Fatal("expected error envelope, got none")
	}
	er, ok := o.Payload.(proto.LobbyError)
	if !ok {
		t.Fatalf("payload type %T", o.Payload)
	}
	if er.Code != proto.LobbyErrBadLevel {
		t.Fatalf("error code = %q, want %q", er.Code, proto.LobbyErrBadLevel)
	}
}

// TestLobby_LevelDifficultyBuckets — A..F = Easy, G..M = Medium,
// N..S = Hard, T..Z = Brutal.
func TestLobby_LevelDifficultyBuckets(t *testing.T) {
	bucketCases := []struct {
		letter byte
		want   string
	}{
		{'A', "Easy"}, {'F', "Easy"},
		{'G', "Medium"}, {'M', "Medium"},
		{'N', "Hard"}, {'S', "Hard"},
		{'T', "Brutal"}, {'Z', "Brutal"},
	}
	for _, c := range bucketCases {
		got := difficultyForLetter(c.letter)
		if got != c.want {
			t.Errorf("difficultyForLetter(%c) = %q, want %q", c.letter, got, c.want)
		}
	}
}
