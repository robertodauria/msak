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
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
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
	res := make([]results.NDTMResult, r.streams)

	timeout, cancel := context.WithTimeout(ctx, r.duration+time.Duration(r.streams)*r.delay)
	defer cancel()

	wg := sync.WaitGroup{}

	avgRates := make([]float64, r.streams)
	aggregateAvgRates := make(map[string]float64)
	aggregateAvgRatesMutex := &sync.Mutex{}

	start := time.Now()
	// Set all the results' start times to the same start time.
	for _, r := range res {
		r.StartTime = start
	}

	for i := 0; i < r.streams; i++ {
		wg.Add(2)
		measurements := make(chan results.Measurement)

		res[i].StartTime = time.Now()
		idx := i

		// Read from the measurements channel and store them in the results
		// struct.
		go func() {
			defer wg.Done()
			for m := range measurements {
				if m.Origin == "receiver" {
					fmt.Printf("Avg rate: %v Mb/s\n", float64(m.AppInfo.NumBytes)/float64(m.AppInfo.ElapsedTime)*8)
					// Sum the throughput for all the streams.
					avgRates[idx] = float64(m.AppInfo.NumBytes) / float64(m.AppInfo.ElapsedTime) * 8
					aggregateTime := time.Since(start).Seconds()
					for j := 0; j < r.streams; j++ {
						aggregateAvgRatesMutex.Lock()
						aggregateAvgRates[fmt.Sprintf("%f", aggregateTime)] += avgRates[j]
						aggregateAvgRatesMutex.Unlock()
					}
					res[idx].ClientMeasurements =
						append(res[idx].ClientMeasurements, m)
				} else {
					res[idx].ServerMeasurements =
						append(res[idx].ServerMeasurements, m)
				}
			}
		}()

		go r.run(&wg, timeout, &res[i], measurements)
		time.Sleep(r.delay)
	}

	wg.Wait()
	end := time.Now()
	// Set all the results' end times to the same end time.
	for _, r := range res {
		r.EndTime = end
	}

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
		aggregateJSON, err := json.Marshal(aggregateAvgRates)
		rtx.Must(err, "Could not marshal aggregate throughput")
		aggregateFile := path.Join(outputFolder, "aggregate.json")
		err = os.WriteFile(aggregateFile, aggregateJSON, 0644)
		rtx.Must(err, "Could not write aggregate throughput to file")

		for i, result := range res {
			filename := path.Join(outputFolder, fmt.Sprintf("%d.json", i))
			resultJSON, err := json.Marshal(result)
			rtx.Must(err, "Failed to marshal result")
			err = os.WriteFile(filename, resultJSON, 0644)
			rtx.Must(err, "Failed to write result to disk")
		}
	}
}

func (r *Client) run(wg *sync.WaitGroup, ctx context.Context, result *results.NDTMResult,
	measurements chan results.Measurement) {
	defer wg.Done()
	var (
		conn *websocket.Conn
		err  error
	)
	if conn, err = r.dialer(ctx, r.url); err != nil {
		rtx.Must(err, "download dialer")
	}

	connInfo := &results.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
	}
	if err := ndtm.Receiver(ctx, conn, connInfo, measurements); err != nil {
		rtx.Must(err, "download")
	}
}
