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

	rates := make([]float64, r.streams)
	aggregateRates := make(map[string]float64)
	aggregateRatesMutex := &sync.Mutex{}
	start := time.Now()

	for i := 0; i < r.streams; i++ {
		wg.Add(2)
		measurements := make(chan persistence.Measurement)

		results[i].StartTime = time.Now()
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
				// Sum the throughput for all the streams.
				rates[idx] = float64(m.AppInfo.NumBytes) / float64(m.AppInfo.ElapsedTime) * 8
				aggregateTime := time.Since(start).Seconds()
				for j := 0; j < r.streams; j++ {
					aggregateRatesMutex.Lock()
					aggregateRates[fmt.Sprintf("%f", aggregateTime)] += rates[j]
					aggregateRatesMutex.Unlock()
				}
			}
		}()

		go r.run(&wg, timeout, measurements)
		time.Sleep(r.delay)
	}

	wg.Wait()
	fmt.Printf("Completed (%d streams):\n", r.streams)

	// Write each stream's result to outputPath/<nstreams>/<uuid>.json.
	if r.outputPath != "" {
		outputFolder := path.Join(r.outputPath, fmt.Sprintf("%d", r.streams))
		// Create the full output path if it doesn't exist.
		if _, err := os.Stat(outputFolder); errors.Is(err, os.ErrNotExist) {
			err := os.MkdirAll(outputFolder, os.ModePerm)
			rtx.Must(err, "Could not create output directory")
		}

		// Write aggregate throughput.
		aggregateJSON, err := json.Marshal(aggregateRates)
		rtx.Must(err, "Could not marshal aggregate throughput")
		aggregateFile := path.Join(outputFolder, "aggregate.json")
		err = os.WriteFile(aggregateFile, aggregateJSON, 0644)
		rtx.Must(err, "Could not write aggregate throughput to file")

		for i, result := range results {
			filename := path.Join(outputFolder, fmt.Sprintf("%d.json", i))

			for _, m := range result.Download.ClientMeasurements {
				fmt.Printf("%v\n", m.AppInfo)
			}

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
	connInfo := &persistence.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
	}
	if err := ndt7.Receiver(ctx, connInfo, measurements, conn); err != nil {
		rtx.Must(err, "download")
	}
}
