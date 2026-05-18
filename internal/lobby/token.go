package lobby

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/jafo/isnipes/internal/sim"
)

// TokenTTL is the validity window for an issued joinToken (§7.3).
const TokenTTL = 60 * time.Second

// pendingMatchJoin is the lobby-side state for one issued joinToken.
type pendingMatchJoin struct {
	Token     string
	MatchID   string
	SessionID SessionID
	PlayerID  sim.EntityID
	Nick      string
	IssuedAt  time.Time
}

// generateToken produces a 24-char base64 of 16 random bytes
// (RFC 4648, standard alphabet with padding). matches §7.3.
func generateToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}
