package wsnet

import "nhooyr.io/websocket"

// CloseReason mirrors PHASE2.md §6.1 / §4.3.5 close codes.
type CloseReason int

const (
	CloseOK          CloseReason = 1000
	CloseUnsupported CloseReason = 1003 // for lobby JSON envelope version mismatch
	CloseAuth        CloseReason = 4001
	CloseVersion     CloseReason = 4002
	CloseMalformed   CloseReason = 4003
	CloseFull        CloseReason = 4004
	CloseNotFound    CloseReason = 4005
	CloseServerError CloseReason = 4006
	CloseIdle        CloseReason = 4007
)

// Label returns the SPEC-defined reason string for the close code.
func (r CloseReason) Label() string {
	switch r {
	case CloseOK:
		return "ok"
	case CloseUnsupported:
		return "unsupported"
	case CloseAuth:
		return "AUTH"
	case CloseVersion:
		return "VERSION"
	case CloseMalformed:
		return "MALFORMED"
	case CloseFull:
		return "FULL"
	case CloseNotFound:
		return "NOT_FOUND"
	case CloseServerError:
		return "SERVER_ERROR"
	case CloseIdle:
		return "IDLE"
	}
	return ""
}

// closeWith writes a close frame and closes the underlying conn.
func closeWith(c *websocket.Conn, r CloseReason) error {
	return c.Close(websocket.StatusCode(r), r.Label())
}
