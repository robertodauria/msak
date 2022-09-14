package ndtm

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/internal/tcpinfox"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

var errNonTextMessage = errors.New("not a text message")

type Rate float64

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
	const proto = "net.measurementlab.ndt.v7"
	if r.Header.Get("Sec-WebSocket-Protocol") != proto {
		w.WriteHeader(http.StatusBadRequest)
		return nil, errors.New("missing Sec-WebSocket-Protocol header")
	}
	h := http.Header{}
	h.Add("Sec-WebSocket-Protocol", proto)
	u := websocket.Upgrader{
		// Allow cross-origin resource sharing.
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		// Set r/w buffers to the maximum allowed message size.
		ReadBufferSize:  spec.MaxMessageSize,
		WriteBufferSize: spec.MaxMessageSize,
	}
	return u.Upgrade(w, r, h)
}

// Receiver receives data over the provided websocket.Conn.
//
// The computed rate is sent to the rates channel.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
func Receiver(ctx context.Context, conn *websocket.Conn, connInfo *results.ConnectionInfo, mchannel chan<- results.Measurement) error {
	errch := make(chan error, 1)
	defer conn.Close()
	go receiver(conn, connInfo, mchannel, errch)
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

func receiver(conn *websocket.Conn, connInfo *results.ConnectionInfo, mchannel chan<- results.Measurement,
	errch chan<- error) {
	defer close(mchannel)
	tcpconn := conn.UnderlyingConn().(*net.TCPConn)
	fp, err := tcpconn.File()
	if err != nil {
		errch <- err
		return
	}
	numBytes := int64(0)
	start := time.Now()
	conn.SetReadLimit(spec.MaxMessageSize)
	ticker := time.NewTicker(spec.MeasureInterval)
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
			// Make sure the message's byte are counted.
			numBytes += int64(len(data))
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

			emit(&m, "receiver")
			conn.WriteJSON(m)
			// Send measurement over the mchannel channel.
			mchannel <- m
		default:
			// NOTHING
		}
	}
}

// readcounterflow reads counter flow message and reports rates.
// Errors are reported via errCh.
func readcounterflow(wg *sync.WaitGroup, conn *websocket.Conn, mchannel chan<- results.Measurement,
	errCh chan<- error) {
	defer wg.Done()
	conn.SetReadLimit(spec.MaxMessageSize)
	for {
		mtype, mdata, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if mtype != websocket.TextMessage {
			errCh <- errNonTextMessage
			return
		}
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

// Sender sends ndt7 data over the provided websocket.Conn and reads
// counterflow messages.
//
// Measurements are sent to the mchannel channel. You SHOULD pass
// to this function a channel with a reasonably large buffer (e.g.,
// 64 slots) because the emitter will not block on sending.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
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
	go sender(wg, conn, connInfo, mchannel, errch)
	zap.L().Sugar().Debug("started sender")
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

func sender(wg *sync.WaitGroup, conn *websocket.Conn, connInfo *results.ConnectionInfo,
	mchannel chan<- results.Measurement, errch chan<- error) {
	defer wg.Done()
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		errch <- err
		return
	}

	appInfo := &results.AppInfo{}

	start := time.Now()
	size := spec.MinMessageSize

	message, err := makePreparedMessage(size)
	if err != nil {
		errch <- err
		return
	}

	ticker := time.NewTicker(spec.MeasureInterval)
	defer ticker.Stop()

	for {
		if err := conn.WritePreparedMessage(message); err != nil {
			errch <- err
			return
		}

		appInfo.NumBytes += int64(size)
		select {
		case <-ticker.C:
			appInfo.ElapsedTime = int64(time.Since(start) / time.Microsecond)
			tcpInfo, err := tcpinfox.GetTCPInfo(fp)
			if err != nil {
				errch <- err
				return
			}
			// Get BBRInfo data, if available.
			bbrInfo, _ := congestion.GetBBRInfo(fp)
			// Send measurement message
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
		if int64(size) >= spec.MaxMessageSize || int64(size) >= (appInfo.NumBytes/spec.ScalingFraction) {
			continue
		}
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			errch <- err
			return
		}
	}
}

func emit(m *results.Measurement, testname string) {
	b, err := json.Marshal(*m)
	rtx.Must(err, "marshal measurement")
	fmt.Printf("%s: %s\n", testname, string(b))
}
