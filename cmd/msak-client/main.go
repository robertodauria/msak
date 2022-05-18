package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/m-lab/msak/internal"
)

var (
	flagDownload = flag.String("download", "", "Download URL")
	flagUpload   = flag.String("upload", "", "Upload URL")
)

func dialer(ctx context.Context, URL string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		ReadBufferSize:  internal.MaxMessageSize,
		WriteBufferSize: internal.MaxMessageSize,
	}
	headers := http.Header{}
	headers.Add("Sec-WebSocket-Protocol", "net.measurementlab.ndt.v7")
	conn, _, err := dialer.DialContext(ctx, URL, headers)
	return conn, err
}

func main() {
	flag.Parse()
	ctx := context.Background()
	var (
		conn *websocket.Conn
		err  error
	)

	if *flagDownload != "" {
		if conn, err = dialer(ctx, *flagDownload); err != nil {
			rtx.Must(err, "download dialer")
		}
		if err = internal.Receiver(ctx, conn); err != nil {
			rtx.Must(err, "download")
		}
	}
	if *flagUpload != "" {
		if conn, err = dialer(ctx, *flagUpload); err != nil {
			rtx.Must(err, "upload dialer")
		}
		if err = internal.Sender(ctx, conn, false); err != nil {
			rtx.Must(err, "upload")
		}
	}
}
