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

type BitsPerSecond float64

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

// Receiver handles an upload measurement over the given websocket.Conn.
func Receiver(ctx context.Context, rates chan<- BitsPerSecond, conn *websocket.Conn) error {
	defer close(rates)
	appInfo := AppInfo{}
	start := time.Now()
	conn.SetReadLimit(MaxMessageSize)
	ticker := time.NewTicker(MeasureInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		kind, reader, err := conn.NextReader()
		if err != nil {
			return err
		}
		if kind == websocket.TextMessage {
			data, err := ioutil.ReadAll(reader)
			if err != nil {
				return err
			}
			appInfo.NumBytes += int64(len(data))
			continue
		}
		n, err := io.Copy(ioutil.Discard, reader)
		if err != nil {
			return err
		}
		appInfo.NumBytes += int64(n)
		select {
		case <-ticker.C:
			appInfo.ElapsedTime = int64(time.Since(start) / time.Microsecond)
			emitAppInfo(&appInfo, "upload")
			// Send counterflow message
			conn.WriteJSON(Measurement{AppInfo: appInfo})
			// Send measurement back to the caller
			fmt.Println(appInfo)
			rates <- BitsPerSecond(float64(appInfo.NumBytes) * 8 / float64(appInfo.ElapsedTime))

		default:
			// NOTHING
		}
	}
	return nil
}

// readcounterflow reads counter flow message and reports rates.
// Errors are reported via errCh.
func readcounterflow(ctx context.Context, conn *websocket.Conn, ch chan<- BitsPerSecond, errCh chan<- error) {
	conn.SetReadLimit(MaxMessageSize)
	for ctx.Err() == nil {
		mtype, mdata, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				fmt.Println("unexpected error")
				errCh <- err
			}
			close(errCh)
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
		ch <- BitsPerSecond(float64(m.AppInfo.NumBytes) * 8 / float64(m.AppInfo.ElapsedTime))
	}
	// Signal we've finished reading counterflow messages.
	errCh <- nil
}

func Sender(ctx context.Context, rates chan<- BitsPerSecond, conn *websocket.Conn, bbr bool) error {
	defer close(rates)
	if bbr {
		ci := netx.ToConnInfo(conn.UnderlyingConn())
		err := ci.EnableBBR()
		rtx.Must(err, "cannot enable BBR", err)
	}
	appInfo := AppInfo{}
	start := time.Now()
	size := MinMessageSize
	message, err := makePreparedMessage(size)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(MeasureInterval)
	defer ticker.Stop()

	// Process counterflow messages and write rates to the channel.
	errch := make(chan error)
	go readcounterflow(ctx, conn, rates, errch)

	for ctx.Err() == nil {
		if err := conn.WritePreparedMessage(message); err != nil {
			return err
		}
		appInfo.NumBytes += int64(size)
		select {
		case <-ticker.C:
			appInfo.ElapsedTime = int64(time.Since(start) / time.Microsecond)
			emitAppInfo(&appInfo, "download")
			// Send measurement message
			conn.WriteJSON(Measurement{AppInfo: appInfo})
		default:
			// NOTHING
		}
		if int64(size) >= MaxMessageSize || int64(size) >= (appInfo.NumBytes/ScalingFraction) {
			continue
		}
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			return err
		}
	}

	conn.Close()

	// Read any errors from the readcounterflow goroutine.
	err = <-errch
	if err != nil {
		return err
	}
	return nil
}

func emitAppInfo(appInfo *AppInfo, testname string) {
	fmt.Printf(`{"AppInfo":{"NumBytes":%d,"ElapsedTime":%d},"Test":"%s"}`+"\n\n",
		appInfo.NumBytes, appInfo.ElapsedTime, testname)
}
