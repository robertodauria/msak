package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/ndt7"
)

type dialerFunc func(ctx context.Context, url string) (*websocket.Conn, error)

type Client struct {
	dialer     func(ctx context.Context, url string) (*websocket.Conn, error)
	url        string
	duration   time.Duration
	delay      time.Duration
	streams    int
	outputPath string
}

const (
	DefaultDuration = 5 * time.Second
	DefaultDelay    = 0
)

func New(dialer dialerFunc, url string, streams int) *Client {
	return NewWithConfig(dialer, url, DefaultDuration, DefaultDelay, streams, "")
}

func NewWithConfig(dialer dialerFunc, url string, duration time.Duration,
	delay time.Duration, streams int, outputPath string) *Client {
	return &Client{
		dialer:     dialer,
		url:        url,
		duration:   duration,
		delay:      delay,
		streams:    streams,
		outputPath: outputPath,
	}
}

// Receive starts nStreams streams to receive measurement data from the server.
func (r *Client) Receive(ctx context.Context) {
	results := make([]persistence.NDT7Result, r.streams)

	timeout, cancel := context.WithTimeout(ctx, r.duration+time.Duration(r.streams)*r.delay)
	defer cancel()

	wg := sync.WaitGroup{}

	for i := 0; i < r.streams; i++ {
		wg.Add(2)
		measurements := make(chan persistence.Measurement)

		results[i].Download = &persistence.ArchivalData{}
		idx := i

		// Read from the measurements channel and store them in the results
		// struct.
		go func() {
			defer wg.Done()
			for m := range measurements {
				results[idx].Download.ClientMeasurements =
					append(results[idx].Download.ClientMeasurements, m)
				fmt.Printf("Rate: %v Mb/s\n", float64(m.AppInfo.NumBytes)/float64(m.AppInfo.ElapsedTime)*8)
			}
		}()

		go r.run(&wg, timeout, measurements)
		time.Sleep(r.delay)
	}

	wg.Wait()
	fmt.Printf("Completed (%d streams):\n", r.streams)

	// Write each stream's result to outputPath, one per file.
	// Create outputPath if it doesn't exist.
	if _, err := os.Stat(r.outputPath); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(r.outputPath, os.ModePerm)
		rtx.Must(err, "Could not create output directory")
	}
	for i, result := range results {
		if r.outputPath != "" {
			filename := path.Join(r.outputPath, fmt.Sprintf("%d.json", i))
			resultJSON, err := json.Marshal(result)
			rtx.Must(err, "Failed to marshal result")
			err = os.WriteFile(filename, resultJSON, 0644)
			rtx.Must(err, "Failed to write result to disk")
		}
	}
}

func (r *Client) run(wg *sync.WaitGroup, ctx context.Context, measurements chan persistence.Measurement) {
	defer wg.Done()
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = r.dialer(ctx, r.url); err != nil {
		rtx.Must(err, "download dialer")
	}
	if err := ndt7.Receiver(ctx, measurements, conn); err != nil {
		rtx.Must(err, "download")
	}
}
