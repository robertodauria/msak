package ndt7

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/m-lab/tcp-info/inetdiag"
	"github.com/m-lab/tcp-info/tcp"
	"github.com/robertodauria/msak/internal"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/internal/tcpinfox"
)

var errNonTextMessage = errors.New("not a text message")

type Rate float64

type AppInfo struct {
	NumBytes    int64
	ElapsedTime int64
}

type Measurement struct {
	AppInfo AppInfo          `json:"AppInfo"`
	TCPInfo tcp.LinuxTCPInfo `json:"TCPInfo"`
	BBRInfo inetdiag.BBRInfo `json:"BBRInfo"`
}

func makePreparedMessage(size int) (*websocket.PreparedMessage, error) {
	return websocket.NewPreparedMessage(websocket.BinaryMessage, make([]byte, size))
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
		ReadBufferSize:  internal.MaxMessageSize,
		WriteBufferSize: internal.MaxMessageSize,
	}
	return u.Upgrade(w, r, h)
}

// Receiver receives data over the provided websocket.Conn.
//
// The computed rate is sent to the rates channel.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
func Receiver(ctx context.Context, connInfo *persistence.ConnectionInfo, mchannel chan<- persistence.Measurement,
	conn *websocket.Conn) error {
	errch := make(chan error, 1)
	defer close(mchannel)
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

func receiver(conn *websocket.Conn, connInfo *persistence.ConnectionInfo, mchannel chan<- persistence.Measurement,
	errch chan<- error) {
	tcpconn := conn.UnderlyingConn().(*net.TCPConn)
	fp, err := tcpconn.File()
	if err != nil {
		errch <- err
		return
	}
	numBytes := int64(0)
	start := time.Now()
	conn.SetReadLimit(internal.MaxMessageSize)
	ticker := time.NewTicker(internal.MeasureInterval)
	defer ticker.Stop()
	for {
		kind, reader, err := conn.NextReader()
		if err != nil {
			errch <- err
			return
		}
		if kind == websocket.TextMessage {
			data, err := ioutil.ReadAll(reader)
			if err != nil {
				errch <- err
				return
			}
			numBytes += int64(len(data))
			continue
		}
		n, err := io.Copy(ioutil.Discard, reader)
		if err != nil {
			errch <- err
			return
		}
		numBytes += int64(n)
		select {
		case <-ticker.C:
			appInfo := &persistence.AppInfo{
				NumBytes:    int64(numBytes),
				ElapsedTime: time.Since(start).Microseconds(),
			}

			// Get TCPInfo data.
			tcpInfo, err := tcpinfox.GetTCPInfo(fp)
			if err != nil && !errors.Is(err, tcpinfox.ErrNoSupport) {
				errch <- err
				return
			}

			// Send counterflow message
			m := persistence.Measurement{
				AppInfo:        appInfo,
				TCPInfo:        &persistence.TCPInfo{LinuxTCPInfo: *tcpInfo},
				ConnectionInfo: connInfo,
			}

			emit(&m, "receiver")
			conn.WriteJSON(m)
			// Send measurement back to the caller
			mchannel <- m
		default:
			// NOTHING
		}
	}
}

// readcounterflow reads counter flow message and reports rates.
// Errors are reported via errCh.
func readcounterflow(conn *websocket.Conn, mchannel chan<- persistence.Measurement,
	errCh chan<- error) {
	defer close(mchannel)
	conn.SetReadLimit(internal.MaxMessageSize)
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
		var m persistence.Measurement
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
func Sender(ctx context.Context, conn *websocket.Conn, connInfo *persistence.ConnectionInfo,
	mchannel chan<- persistence.Measurement, cc string) error {
	defer conn.Close() // signal child goroutines it's time to stop
	errch := make(chan error, 2)
	// Process counterflow messages
	go readcounterflow(conn, mchannel, errch)
	go sender(conn, errch, cc)
	var err error
	select {
	case <-ctx.Done():
		return nil
	case err = <-errch:
		if websocket.IsUnexpectedCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway) {
			return err
		}
	}
	return err
}

func sender(conn *websocket.Conn, errch chan<- error, cc string) {
	tcpconn := conn.UnderlyingConn().(*net.TCPConn)
	fp, err := tcpconn.File()
	if err != nil {
		errch <- err
		return
	}
	// Attempt to set the requested congestion control algorithm.
	if cc != "default" {
		fmt.Printf("setting cc %s\n", cc)
		err = congestion.Set(fp, cc)
		if err != nil {
			errch <- err
			return
		}
	}

	appInfo := AppInfo{}

	start := time.Now()
	size := internal.MinMessageSize

	message, err := makePreparedMessage(size)
	if err != nil {
		errch <- err
		return
	}

	ticker := time.NewTicker(internal.MeasureInterval)
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
			err = conn.WriteJSON(Measurement{
				AppInfo: appInfo,
				TCPInfo: *tcpInfo,
				BBRInfo: bbrInfo,
			})

			if err != nil {
				errch <- err
				return
			}
		default:
			// NOTHING
		}
		if int64(size) >= internal.MaxMessageSize || int64(size) >= (appInfo.NumBytes/internal.ScalingFraction) {
			continue
		}
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			errch <- err
			return
		}
	}
}

func emit(m *persistence.Measurement, testname string) {
	b, err := json.Marshal(*m)
	rtx.Must(err, "marshal measurement")
	fmt.Printf("%s: %s\n", testname, string(b))
}
