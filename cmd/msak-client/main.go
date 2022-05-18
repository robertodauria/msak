package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/m-lab/msak/internal"
)

// TODO: implement wss :)
const protocol = "ws"

var (
	flagServer   = flag.String("server", "localhost:8080", "Server address")
	flagDownload = flag.Bool("download", false, "Download URL")
	flagUpload   = flag.Bool("upload", false, "Upload URL")
	flagStreams  = flag.Int("streams", 1, "Number of streams")
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
	// downloadURL := protocol + "://" + *flagServer + internal.DownloadPath
	uploadURL := protocol + "://" + *flagServer + internal.UploadPath

	// previousRate := 0.0
	streams := 5
	wg := sync.WaitGroup{}
	// Start N streams and let them run for 5 seconds.
	timeout, _ := context.WithTimeout(context.Background(), 5*time.Second)
	allRates := map[int]internal.BitsPerSecond{}
	for i := 0; i < streams; i++ {
		wg.Add(1)
		rates := make(chan internal.BitsPerSecond)
		// Read from the rates channel and store rates in the map.
		idx := i
		go func() {
			for rate := range rates {
				allRates[idx] = rate
			}
		}()
		go upload(&wg, timeout, rates, uploadURL)
	}
	wg.Wait()
	fmt.Printf("Completed (%d streams):\n", streams)
	fmt.Println("Rates:")
	for i, rate := range allRates {
		fmt.Printf("\t%d: %f\n", i, rate)
	}
	sum := 0.0
	for _, bps := range allRates {
		sum += float64(bps)
	}
	fmt.Printf("Aggregated rate: %v\n", sum)
}

func download(wg *sync.WaitGroup, ctx context.Context, rate <-chan internal.BitsPerSecond, url string) {
	defer wg.Done()
	fmt.Println("Started download stream")
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = dialer(ctx, url); err != nil {
		rtx.Must(err, "download dialer")
	}
	rates := make(chan internal.BitsPerSecond)
	if err := internal.Receiver(ctx, rates, conn); err != nil {
		rtx.Must(err, "download")
	}
}

func upload(wg *sync.WaitGroup, ctx context.Context, rates chan internal.BitsPerSecond, url string) {
	defer wg.Done()
	fmt.Println("Started upload stream")
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = dialer(ctx, url); err != nil {
		rtx.Must(err, "upload dialer")
	}
	if err = internal.Sender(ctx, rates, conn, false); err != nil {
		rtx.Must(err, "upload")
	}
}
