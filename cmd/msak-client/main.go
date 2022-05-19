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
	"github.com/robertodauria/msak/internal"
)

// TODO: implement wss :)
const protocol = "ws"
const maxStreams = 10

var (
	flagServer  = flag.String("server", "localhost:8080", "Server address")
	flagStreams = flag.Int("streams", 0, "Number of streams")
	flagCC      = flag.String("cc", "default", "Congestion control algorithm to use")
)

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

	if *flagStreams != 0 {
		// Single run with custom number of streams
		downloadAggrRate := multi(internal.Download, *flagStreams)
		uploadAggrRate := multi(internal.Upload, *flagStreams)
		fmt.Printf("Download: %f\n", downloadAggrRate)
		fmt.Printf("Upload: %f\n", uploadAggrRate)
		return
	}

	rates := []float64{}
	streams := 1
	previousRate := 0.0
	for streams <= maxStreams {
		rate := multi(internal.Download, streams)
		fmt.Printf("Aggregated rate (%d streams): %f\n", streams, rate)
		rates = append(rates, rate)

		if rate <= previousRate*1.05 {
			break
		}
		streams++
		previousRate = rate
	}

	fmt.Println("Aggregated rate by number of streams")
	for i, rate := range rates {
		fmt.Printf("\t%d: %f\n", i+1, rate)
	}
}

func multi(test internal.Test, streams int) float64 {
	uploadURL := protocol + "://" + *flagServer + internal.UploadPath
	downloadURL := protocol + "://" + *flagServer + internal.DownloadPath

	wg := sync.WaitGroup{}

	// Start N streams and let them run for 5 seconds.
	timeout, _ := context.WithTimeout(context.Background(), 5*time.Second)
	rates := make([]internal.Rate, streams)
	for i := 0; i < streams; i++ {
		wg.Add(2)
		ratesCh := make(chan internal.Rate)
		// Read from the rates channel and store rates in the slice.
		idx := i
		go func() {
			defer wg.Done()
			for rate := range ratesCh {
				rates[idx] = rate
			}
		}()
		switch test {
		case internal.Upload:
			go upload(&wg, timeout, ratesCh, uploadURL)
		case internal.Download:
			go download(&wg, timeout, ratesCh, downloadURL)
		default:
			panic("invalid test type")
		}

	}
	wg.Wait()
	fmt.Printf("Completed (%d streams):\n", streams)
	fmt.Println("Rates:")
	for i, rate := range rates {
		fmt.Printf("\t%d: %f\n", i, rate)
	}
	sum := 0.0
	for _, bps := range rates {
		sum += float64(bps)
	}
	return sum
}

func download(wg *sync.WaitGroup, ctx context.Context, rates chan internal.Rate, url string) {
	defer wg.Done()
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = dialer(ctx, url); err != nil {
		rtx.Must(err, "download dialer")
	}
	if err := internal.Receiver(ctx, rates, conn); err != nil {
		rtx.Must(err, "download")
	}
}

func upload(wg *sync.WaitGroup, ctx context.Context, rates chan internal.Rate, url string) {
	defer wg.Done()
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = dialer(ctx, url); err != nil {
		rtx.Must(err, "upload dialer")
	}
	if err = internal.Sender(ctx, conn, rates, *flagCC); err != nil {
		rtx.Must(err, "upload")
	}
}
