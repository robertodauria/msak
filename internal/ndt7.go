package internal

import (
	"context"
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

// HandleUpload handles an upload measurement over the given websocket.Conn.
func HandleUpload(ctx context.Context, conn *websocket.Conn) error {
	var total int64
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
			total += int64(len(data))
			fmt.Printf("%s\n", string(data))
			continue
		}
		n, err := io.Copy(ioutil.Discard, reader)
		if err != nil {
			return err
		}
		total += int64(n)
		select {
		case <-ticker.C:
			emitAppInfo(start, total, "upload")
		default:
			// NOTHING
		}
	}
	return nil
}

func HandleDownload(ctx context.Context, conn *websocket.Conn) error {
	ci := netx.ToConnInfo(conn.UnderlyingConn())
	err := ci.EnableBBR()
	rtx.Must(err, "cannot enable BBR", err)
	var total int64
	start := time.Now()
	size := MinMessageSize
	message, err := makePreparedMessage(size)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(MeasureInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := conn.WritePreparedMessage(message); err != nil {
			return err
		}
		total += int64(size)
		select {
		case <-ticker.C:
			emitAppInfo(start, total, "download")
		default:
			// NOTHING
		}
		if int64(size) >= MaxMessageSize || int64(size) >= (total/ScalingFraction) {
			continue
		}
		size <<= 1
		if message, err = makePreparedMessage(size); err != nil {
			return err
		}
	}
	return nil
}

func emitAppInfo(start time.Time, total int64, testname string) {
	fmt.Printf(`{"AppInfo":{"NumBytes":%d,"ElapsedTime":%d},"Test":"%s"}`+"\n\n",
		total, time.Since(start)/time.Microsecond, testname)
}
