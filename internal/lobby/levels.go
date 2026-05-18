package lobby

import (
	"errors"
	"strings"

	"github.com/jafo/isnipes/internal/proto"
	"github.com/jafo/isnipes/internal/sim"
)

// ErrBadLevel is returned by ParseLevel for any malformed level string.
var ErrBadLevel = errors.New("lobby: bad level string")

// ParseLevel validates a level string of the form `<letter><number>`,
// where letter ∈ [A-Za-z] and number ∈ [1-9]. Returns the canonicalised
// upper-case letter and the integer 1..9.
//
// Phase 6 §8.1. Examples accepted: "A1", "Z9", "f3" (→ ('F', 3)).
// Examples rejected: "", "A", "A0", "A10", "AA1", "@1", "ZZ".
func ParseLevel(s string) (byte, int, error) {
	if len(s) != 2 {
		return 0, 0, ErrBadLevel
	}
	letter := s[0]
	if letter >= 'a' && letter <= 'z' {
		letter -= 32
	}
	if letter < 'A' || letter > 'Z' {
		return 0, 0, ErrBadLevel
	}
	num := s[1]
	if num < '1' || num > '9' {
		return 0, 0, ErrBadLevel
	}
	return letter, int(num - '0'), nil
}

// ValidateLevel accepts a proto.Level (the wire form, with Letter as
// a 1-char string and Number as an int 1..9). Returns the canonical
// uppercase Letter and the integer Number, or ErrBadLevel.
func ValidateLevel(l proto.Level) (byte, int, error) {
	if len(l.Letter) != 1 {
		return 0, 0, ErrBadLevel
	}
	letter := l.Letter[0]
	if letter >= 'a' && letter <= 'z' {
		letter -= 32
	}
	if letter < 'A' || letter > 'Z' {
		return 0, 0, ErrBadLevel
	}
	if l.Number < 1 || l.Number > 9 {
		return 0, 0, ErrBadLevel
	}
	return letter, l.Number, nil
}

// CanonicalLevel returns the upper-cased 2-char form, or empty if the
// input is malformed.
func CanonicalLevel(s string) string {
	letter, num, err := ParseLevel(s)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteByte(letter)
	b.WriteByte('0' + byte(num))
	return b.String()
}

// LevelPreset is the per-(letter, number) preview metadata sent to the
// client in the §8.3 `level_presets` envelope.
type LevelPreset struct {
	Letter      byte   `json:"letter"`
	Number      int    `json:"number"`
	Difficulty  string `json:"difficulty"` // Easy/Medium/Hard/Brutal
	PlayerLives int    `json:"playerLives"`
	Generators  int    `json:"generators"`
	MaxSnipes   int    `json:"maxSnipes"`
	Description string `json:"description"`
}

// BuildLevelPresets returns the full 26 × 9 = 234-entry preview table,
// ordered ascending (letter, number). Computed once at boot; mirrors
// sim.LookupLevel's parameter math without re-deriving it.
func BuildLevelPresets() []LevelPreset {
	out := make([]LevelPreset, 0, 26*9)
	for letter := byte('A'); letter <= 'Z'; letter++ {
		for number := 1; number <= 9; number++ {
			p := sim.LookupLevel(letter, number)
			out = append(out, LevelPreset{
				Letter:      letter,
				Number:      number,
				Difficulty:  difficultyForLetter(letter),
				PlayerLives: p.PlayerLives,
				Generators:  p.InitialGenerators,
				MaxSnipes:   p.MaxSnipesTotal,
				Description: descriptionForLetter(letter),
			})
		}
	}
	return out
}

// difficultyForLetter maps the §3.7 letter buckets to display strings.
func difficultyForLetter(letter byte) string {
	switch {
	case letter >= 'A' && letter <= 'F':
		return "Easy"
	case letter >= 'G' && letter <= 'M':
		return "Medium"
	case letter >= 'N' && letter <= 'S':
		return "Hard"
	default:
		return "Brutal"
	}
}

// descriptionForLetter returns one short sentence describing the
// bucket's behavioural feel. Per PHASE6.md §8.2.
func descriptionForLetter(letter byte) string {
	switch difficultyForLetter(letter) {
	case "Easy":
		return "Snipes patrol slowly and shoot without leading."
	case "Medium":
		return "Snipes lead half a tile; fire every 0.67 s."
	case "Hard":
		return "Snipes lead full tile; line-of-sight 10 tiles."
	default:
		return "Snipes are fast (15 sub/tick) and gens are tougher (5 HP)."
	}
}
