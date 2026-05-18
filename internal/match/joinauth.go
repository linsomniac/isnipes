package match

import "errors"

// ErrAuth is returned by SubmitJoin when the token is unknown,
// expired, already-used, or bound to a different matchId. Callers
// should close the WS with Close{4001 "AUTH"}.
var ErrAuth = errors.New("match: auth")
