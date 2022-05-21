package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
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

	results := make([][]Result, streams)

	// Start N streams and set the timeout.
	timeout, _ := context.WithTimeout(context.Background(), *flagLength+time.Duration(streams)**flagDelay)
	rates := make([]internal.Rate, streams)
	for i := 0; i < streams; i++ {
		wg.Add(2)
		ratesCh := make(chan internal.Rate)
		// Read from the rates channel and store rates in the slice.
		idx := i
		go func() {
			start := time.Now()
			defer wg.Done()
			for rate := range ratesCh {
				result := Result{
					Stream:  idx,
					Elapsed: time.Since(start).Seconds(),
					Rate:    float64(rate),
				}
				results[idx] = append(results[idx], result)
				b, err := json.Marshal(result)
				rtx.Must(err, "marshal")
				fmt.Println(string(b))
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
		time.Sleep(*flagDelay)
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

	resultJSON, err := json.Marshal(results)
	rtx.Must(err, "marshal")
	os.WriteFile(*flagOutputPrefix+"_"+fmt.Sprint(streams)+".json",
		[]byte(resultJSON), 0644)
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
