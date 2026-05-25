package loadtest

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"github.com/jafo/isnipes/internal/observ"
	"github.com/jafo/isnipes/internal/proto"
)

// clientStats accumulates measurements aggregated across all clients.
type clientStats struct {
	bytesIn     atomic.Uint64
	bytesOut    atomic.Uint64
	errors      atomic.Int64
	matchAborts atomic.Int64
	lat         observ.RecordingSampler // end-to-end input→snapshot latency
}

func (s *clientStats) addIn(n int)                { s.bytesIn.Add(uint64(n)) }
func (s *clientStats) addOut(n int)               { s.bytesOut.Add(uint64(n)) }
func (s *clientStats) addError()                  { s.errors.Add(1) }
func (s *clientStats) addLatency(d time.Duration) { s.lat.Observe(d) }

// latencyWindowTicks bounds the per-client sendAt map. Each input tick
// records a send timestamp; without eviction the map grows for the whole run
// (a soak-killing leak in the harness itself). The server echoes an input via
// snapshot YourLastInputTick within a few ticks, so a window far larger than
// any acceptable echo latency (256 ticks ≈ 8.5s at 30Hz) keeps every real
// measurement while capping the map. ct is uint16, so ct-window wraps cleanly.
// AIDEV-NOTE: do NOT remove this bound — see git history; the soak RSS gate is
// meaningless if the harness leaks faster than the server it measures.
const latencyWindowTicks uint16 = 256

// walkDir returns a deterministic non-zero movement direction (1..8) keyed
// off the input tick so bots actually move (exercising movement, collision,
// AOI and snapshot diffs) without ever firing — firing would kill players in
// a PvP match and shrink the load before the run completes.
func walkDir(tick uint16) uint8 { return uint8(tick%8) + 1 }

// runClient dials a match, performs the MatchJoin handshake, then sends
// Input at inputHz and drains inbound frames until ctx is done or the
// connection drops. All measurements land in stats; any protocol/IO error
// increments the error count and returns cleanly.
func runClient(ctx context.Context, wsURL, token string, inputHz int, stats *clientStats) {
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		stats.addError()
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(int64(proto.MaxFrameLen) + 16)

	if frame, err := encodeMatchJoin(token); err != nil {
		stats.addError()
		return
	} else if err := writeFrame(ctx, c, frame); err != nil {
		stats.addError()
		return
	} else {
		stats.addOut(len(frame))
	}

	var mu sync.Mutex
	sendAt := make(map[uint16]time.Time)

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				// An error while the run is still active means the client
				// dropped unexpectedly (server closed the socket, etc.) — a
				// real failure the gate must see. ctx cancellation at
				// end-of-run is expected and not an error.
				if ctx.Err() == nil {
					stats.addError()
				}
				return
			}
			stats.addIn(len(data))
			hdr, payload, _, err := proto.DecodeFrame(data)
			if err != nil {
				stats.addError() // a corrupt frame is always a failure
				return
			}
			switch hdr.Type {
			case proto.MsgSnapshot:
				snap, err := proto.DecodeSnapshot(payload)
				if err != nil {
					stats.addError() // malformed snapshot is a protocol failure
					continue
				}
				mu.Lock()
				ts, ok := sendAt[snap.YourLastInputTick]
				mu.Unlock()
				if ok {
					stats.addLatency(time.Since(ts))
				}
			case proto.MsgMatchOver:
				// A SERVER_ERROR end is a real abort; a clean StopAll sends
				// no MatchOver, and a TIMER/normal end carries a different
				// reason. (reason is the byte after the u32 final_tick.)
				if len(payload) >= 5 && payload[4] == proto.EndServerError {
					stats.matchAborts.Add(1)
				}
			}
		}
	}()

	ticker := time.NewTicker(time.Second / time.Duration(inputHz))
	defer ticker.Stop()
	var ct uint16
	for {
		select {
		case <-ctx.Done():
			<-readDone
			return
		case <-readDone:
			return
		case <-ticker.C:
			ct++
			frame, err := encodeInput(proto.Input{ClientTick: ct, Dir: walkDir(ct)})
			if err != nil {
				stats.addError()
				<-readDone
				return
			}
			mu.Lock()
			sendAt[ct] = time.Now()
			delete(sendAt, ct-latencyWindowTicks) // evict the entry one window back; bounds the map
			mu.Unlock()
			if err := writeFrame(ctx, c, frame); err != nil {
				// ctx cancellation at end-of-run is expected, not an error.
				if ctx.Err() == nil {
					stats.addError()
				}
				<-readDone
				return
			}
			stats.addOut(len(frame))
		}
	}
}

func encodeMatchJoin(token string) ([]byte, error) {
	payload, err := proto.MatchJoin{SchemaChecksum: proto.SchemaChecksum(), Token: []byte(token)}.Encode(nil)
	if err != nil {
		return nil, err
	}
	return proto.EncodeFrame(nil, proto.FrameHeader{Type: proto.MsgMatchJoin, Ack: proto.AckNone, Len: uint16(len(payload))}, payload)
}

func encodeInput(in proto.Input) ([]byte, error) {
	payload, err := in.Encode(nil)
	if err != nil {
		return nil, err
	}
	return proto.EncodeFrame(nil, proto.FrameHeader{Type: proto.MsgInput, Ack: proto.AckNone, Len: uint16(len(payload))}, payload)
}

func writeFrame(ctx context.Context, c *websocket.Conn, frame []byte) error {
	wctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageBinary, frame)
}
