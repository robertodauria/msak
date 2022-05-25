package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/robertodauria/msak/client"
	"github.com/robertodauria/msak/internal"
)

// TODO: implement wss :)
const protocol = "ws"
const maxStreams = 3

var (
	flagServer       = flag.String("server", "localhost:8080", "Server address")
	flagStreams      = flag.Int("streams", 0, "Number of streams")
	flagCC           = flag.String("cc", "default", "Congestion control algorithm to use")
	flagDelay        = flag.Duration("delay", 0, "Delay between each stream")
	flagLength       = flag.Duration("length", 5*time.Second, "Length of the last stream")
	flagOutputPrefix = flag.String("output", "result", "Output file prefix")
)

type Result struct {
	Stream  int     `json:"stream"`
	Elapsed float64 `json:"elapsed"`
	Rate    float64 `json:"rate"`
}

func dialer(ctx context.Context, URL string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		ReadBufferSize:  internal.MaxMessageSize,
		WriteBufferSize: internal.MaxMessageSize,
	}
	headers := http.Header{}
	headers.Add("Sec-WebSocket-Protocol", "net.measurementlab.ndt.v7")
	// Request custom CC from the server. Note: the server may just hang if
	// the requested CC does not exist.
	if *flagCC != "default" {
		headers.Add("cc", *flagCC)
	}
	conn, _, err := dialer.DialContext(ctx, URL, headers)
	return conn, err
}

func main() {
	flag.Parse()

	downloadURL := protocol + "://" + *flagServer + internal.DownloadPath

	if *flagStreams != 0 {
		// Single run with custom number of streams
		c := client.NewWithConfig(dialer, downloadURL, *flagLength, *flagDelay, *flagStreams, *flagOutputPrefix)
		c.Receive(context.Background())
		return
	}

	streams := 1
	for streams <= maxStreams {
		c := client.NewWithConfig(dialer, downloadURL, *flagLength, *flagDelay, streams, *flagOutputPrefix)
		c.Receive(context.Background())
		streams++
	}
}
