package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/m-lab/ndt-server/netx"
)

var errNonTextMessage = errors.New("not a text message")

type Rate float64

type AppInfo struct {
	NumBytes    int64
	ElapsedTime int64
}

type Measurement struct {
	AppInfo AppInfo `json:"AppInfo"`
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
		ReadBufferSize:  MaxMessageSize,
		WriteBufferSize: MaxMessageSize,
	}
	return u.Upgrade(w, r, h)
}

// Receiver receives data over the provided websocket.Conn.
//
// The computed rate is sent to the rates channel.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
func Receiver(ctx context.Context, rates chan<- Rate, conn *websocket.Conn) error {
	errch := make(chan error, 1)
	defer close(rates)
	defer conn.Close()
	go receiver(conn, rates, errch)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errch:
		return err
	}
}

func receiver(conn *websocket.Conn, rates chan<- Rate, errch chan<- error) {
	appInfo := AppInfo{}
	start := time.Now()
	conn.SetReadLimit(MaxMessageSize)
	ticker := time.NewTicker(MeasureInterval)
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
			appInfo.NumBytes += int64(len(data))
			continue
		}
		n, err := io.Copy(ioutil.Discard, reader)
		if err != nil {
			errch <- err
			return
		}
		appInfo.NumBytes += int64(n)
		select {
		case <-ticker.C:
			appInfo.ElapsedTime = int64(time.Since(start) / time.Microsecond)
			// emitAppInfo(&appInfo, "upload")
			// Send counterflow message
			conn.WriteJSON(Measurement{AppInfo: appInfo})
			// Send measurement back to the caller
			rates <- Rate(float64(appInfo.NumBytes) * 8 / float64(appInfo.ElapsedTime))
		default:
			// NOTHING
		}
	}
}

// readcounterflow reads counter flow message and reports rates.
// Errors are reported via errCh.
func readcounterflow(conn *websocket.Conn, ch chan<- Rate, errCh chan<- error) {
	conn.SetReadLimit(MaxMessageSize)
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
		var m Measurement
		if err := json.Unmarshal(mdata, &m); err != nil {
			errCh <- err
			return
		}
		ch <- Rate(float64(m.AppInfo.NumBytes) * 8 / float64(m.AppInfo.ElapsedTime))
	}
}

// Sender sends ndt7 data over the provided websocket.Conn and reads
// counterflow messages.
//
// The computed rate is sent to the rates channel.
//
// The context drives how long the connection lasts. If the context is canceled
// or there is an error, the connection and the rates channel are closed.
func Sender(ctx context.Context, conn *websocket.Conn, rates chan<- Rate, enableBBR bool) error {
	errch := make(chan error, 2)
	defer close(rates)
	defer conn.Close()
	go sender(conn, rates, errch, enableBBR)
	select {
	case <-ctx.Done():
		return nil
	case err := <-errch:
		return err
	}
}

func sender(conn *websocket.Conn, rates chan<- Rate, errch chan<- error, enableBBR bool) {
	if enableBBR {
		ci := netx.ToConnInfo(conn.UnderlyingConn())
		err := ci.EnableBBR()
		rtx.Must(err, "cannot enable BBR", err)
	}

	appInfo := AppInfo{}

	start := time.Now()
	size := MinMessageSize

	message, err := makePreparedMessage(size)
	if err != nil {
		errch <- err
		return
	}

	ticker := time.NewTicker(MeasureInterval)
	defer ticker.Stop()

	// Process counterflow messages and write rates to the channel.
	go readcounterflow(conn, rates, errch)

	for {
		if err := conn.WritePreparedMessage(message); err != nil {
			errch <- err
			return
		}

		appInfo.NumBytes += int64(size)
		select {
		case <-ticker.C:
			appInfo.ElapsedTime = int64(time.Since(start) / time.Microsecond)
			// emitAppInfo(&appInfo, "download")
			// Send measurement message
			err := conn.WriteJSON(Measurement{AppInfo: appInfo})
			if err != nil {
				errch <- err
				return
			}
		default:
			// NOTHING
		}
		if int64(size) >= MaxMessageSize || int64(size) >= (appInfo.NumBytes/ScalingFraction) {
			continue
		}
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			errch <- err
			return
		}
	}
}

func emitAppInfo(appInfo *AppInfo, testname string) {
	fmt.Printf(`{"AppInfo":{"NumBytes":%d,"ElapsedTime":%d},"Test":"%s"}`+"\n\n",
		appInfo.NumBytes, appInfo.ElapsedTime, testname)
}
