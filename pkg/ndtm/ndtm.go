package ndtm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/memoryless"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/internal/tcpinfox"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

var errNonTextMessage = errors.New("not a text message")

func makePreparedMessage(size int) (*websocket.PreparedMessage, error) {
	data := make([]byte, size)
	_, err := rand.Read(data)
	if err != nil {
		return nil, err
	}
	return websocket.NewPreparedMessage(websocket.BinaryMessage, data)
}

// Upgrade upgrades the HTTP connection to WebSockets.
// Returns the upgraded websocket.Conn.
func Upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	if r.Header.Get("Sec-WebSocket-Protocol") != spec.SecWebSocketProtocol {
		w.WriteHeader(http.StatusBadRequest)
		return nil, errors.New("missing Sec-WebSocket-Protocol header")
	}
	h := http.Header{}
	h.Add("Sec-WebSocket-Protocol", spec.SecWebSocketProtocol)
	u := websocket.Upgrader{
		// Allow cross-origin resource sharing.
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		// Set r/w buffers to the maximum expected message size.
		ReadBufferSize:  spec.MaxScaledMessageSize,
		WriteBufferSize: spec.MaxScaledMessageSize,
	}
	return u.Upgrade(w, r, h)
}

// Receiver receives data over the provided websocket.Conn.
//
// Measurements are read at semi-random intervals and sent over mchannel.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
func Receiver(ctx context.Context, conn *websocket.Conn, connInfo *results.ConnectionInfo, mchannel chan<- results.Measurement) error {
	errch := make(chan error, 1)
	defer conn.Close()
	go receiver(ctx, conn, connInfo, mchannel, errch)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errch:
		if websocket.IsUnexpectedCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway) {
			return err
		}
		return nil
	}
}

func receiver(ctx context.Context, conn *websocket.Conn, connInfo *results.ConnectionInfo, mchannel chan<- results.Measurement,
	errch chan<- error) {
	defer close(mchannel)
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		errch <- err
		return
	}
	numBytes := int64(0)
	start := time.Now()
	conn.SetReadLimit(spec.MaxScaledMessageSize)
	ticker, err := memoryless.NewTicker(ctx, memoryless.Config{
		Expected: spec.AvgMeasureInterval,
		Min:      100 * time.Millisecond,
		Max:      400 * time.Millisecond,
	})
	if err != nil {
		errch <- err
		return
	}
	defer ticker.Stop()
	for {
		kind, reader, err := conn.NextReader()
		if err != nil {
			errch <- err
			return
		}
		if kind == websocket.TextMessage {
			// Text messages are sender-side measurements: read as JSON and
			// send them over the mchannel channel.
			data, err := ioutil.ReadAll(reader)
			if err != nil {
				errch <- err
				return
			}
			// Make sure the message bytes are counted.
			numBytes += int64(len(data))

			// Unmarshal and send over mchannel.
			var m results.Measurement
			if err := json.Unmarshal(data, &m); err != nil {
				errch <- err
				return
			}
			mchannel <- m
			continue
		}

		// Binary message: count bytes and discard.
		n, err := io.Copy(ioutil.Discard, reader)
		if err != nil {
			errch <- err
			return
		}
		numBytes += int64(n)

		// Is it time to collect a measurement?
		select {
		case <-ticker.C:
			appInfo := &results.AppInfo{
				NumBytes:    int64(numBytes),
				ElapsedTime: time.Since(start).Microseconds(),
			}

			// Get TCPInfo data.
			tcpInfo, err := tcpinfox.GetTCPInfo(fp)
			if err != nil && !errors.Is(err, tcpinfox.ErrNoSupport) {
				errch <- err
				return
			}

			// Send counterflow message.
			m := results.Measurement{
				AppInfo:        appInfo,
				TCPInfo:        &results.TCPInfo{LinuxTCPInfo: *tcpInfo},
				ConnectionInfo: connInfo,
				Origin:         "receiver",
			}

			conn.WriteJSON(m)
			// Send measurement over the mchannel channel.
			mchannel <- m
		default:
			// NOTHING
		}
	}
}

// readcounterflow reads counterflow measurement messages received on the
// provided websocket.Conn and sends them over mchannel.
//
// Counterflow messages are messages going against the direction of the
// measurement (i.e. sent by the receiver).
//
// Errors are reported via errCh.
func readcounterflow(wg *sync.WaitGroup, conn *websocket.Conn, mchannel chan<- results.Measurement,
	errCh chan<- error) {
	// Notify the WaitGroup that this goroutine has completed.
	defer wg.Done()

	// Messages over MaxScaledMessageSize are ignored.
	conn.SetReadLimit(spec.MaxScaledMessageSize)

	for {
		mtype, mdata, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		// Ignore non-text messages from the receiver. This should never happen
		// and is probably a sign of a bug in the client.
		// TODO(roberto): add a Prometheus metric.
		if mtype != websocket.TextMessage {
			errCh <- errNonTextMessage
			return
		}

		// Unmarshal the Measurement and sent it over mchannel, if possible.
		var m results.Measurement
		if err := json.Unmarshal(mdata, &m); err != nil {
			errCh <- err
			return
		}
		select {
		case mchannel <- m:
		default:
			// discard message as documented
		}
	}
}

// Sender sends ndt-m data over the provided websocket.Conn and spawns a
// goroutine to process incoming counterflow messages.
//
// Measurements are sent to the mchannel channel. You SHOULD pass
// to this function a channel with a reasonably large buffer (e.g.,
// 64 slots) because the emitter will not block on sending.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the measurement channel are closed.
func Sender(ctx context.Context, conn *websocket.Conn, connInfo *results.ConnectionInfo,
	mchannel chan<- results.Measurement) error {
	errch := make(chan error, 2)
	wg := &sync.WaitGroup{}
	wg.Add(2)

	defer func() {
		conn.Close()
		// Make sure both goroutines are done before closing mchannel.
		wg.Wait()
		close(mchannel)
	}()

	// Process counterflow messages
	go readcounterflow(wg, conn, mchannel, errch)
	zap.L().Sugar().Debug("started readcounterflow")

	// Send measurement data.
	go sender(ctx, wg, conn, connInfo, mchannel, errch)
	zap.L().Sugar().Debug("started sender")

	// Termination: either the context is canceled or there is an error on errch.
	// Both cause the connection to be closed, which terminates the sender and
	// readcounterflow goroutines.
	select {
	case <-ctx.Done():
		zap.L().Sugar().Debug("ctx done")
		return nil
	case err := <-errch:
		zap.L().Sugar().Debugf("received err (%s) from errch", err.Error())
		if websocket.IsUnexpectedCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway) {
			return err
		}
		return nil
	}
}

// sender sends binary messages and periodic measurement data over the connection
// and measurement data over mchannel.
func sender(ctx context.Context, wg *sync.WaitGroup, conn *websocket.Conn, connInfo *results.ConnectionInfo,
	mchannel chan<- results.Measurement, errch chan<- error) {
	// Notify the WaitGroup that this goroutine has completed.
	defer wg.Done()

	// Get the socket's file descriptor so measurements can be collected.
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		errch <- err
		return
	}

	numBytes := 0

	start := time.Now()
	size := spec.MinMessageSize

	message, err := makePreparedMessage(size)
	if err != nil {
		errch <- err
		return
	}

	ticker, err := memoryless.NewTicker(ctx, memoryless.Config{
		Min:      spec.MinMeasureInterval,
		Expected: spec.AvgMeasureInterval,
		Max:      spec.MaxMeasureInterval,
	})
	if err != nil {
		errch <- err
		return
	}
	defer ticker.Stop()

	// Main sender loop:
	// - write a prepared message
	// - check if it's time to collect a measurement
	//   - (collect a measurement)
	// - check if it's time to scale the message size
	//   - (scale the message size)
	//
	// Prepared (binary) messages and Measurement messages are written to the
	// same socket. This means the speed at which we can send measurements is
	// limited by how long it takes to send a prepared message, since they
	// can't be written simultaneously.
	for {
		if err := conn.WritePreparedMessage(message); err != nil {
			errch <- err
			return
		}

		numBytes += size

		select {
		case <-ticker.C:
			appInfo := &results.AppInfo{
				ElapsedTime: int64(time.Since(start) / time.Microsecond),
				NumBytes:    int64(numBytes),
			}
			tcpInfo, err := tcpinfox.GetTCPInfo(fp)
			if err != nil {
				errch <- err
				return
			}
			// Get BBRInfo data, if available. Errors are not critical here.
			bbrInfo, _ := congestion.GetBBRInfo(fp)

			// Send measurement message over the network as a JSON.
			m := results.Measurement{
				AppInfo: appInfo,
				TCPInfo: &results.TCPInfo{
					LinuxTCPInfo: *tcpInfo,
					ElapsedTime:  appInfo.ElapsedTime,
				},
				BBRInfo:        &results.BBRInfo{BBRInfo: bbrInfo},
				ConnectionInfo: connInfo,
				Origin:         "sender",
			}
			err = conn.WriteJSON(m)

			// Send the measurement over mchannel if possible. Do not block.
			select {
			case mchannel <- m:
			default:
				// discard message
			}

			if err != nil {
				errch <- err
				return
			}
		default:
			// NOTHING
		}

		// Is it time to scale the message size?
		if int64(size) >= spec.MaxScaledMessageSize || size >= (numBytes/spec.ScalingFraction) {
			continue
		}
		// Double the message size and make a new prepared message.
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			errch <- err
			return
		}
	}
}
