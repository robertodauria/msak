package client

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/robertodauria/msak/client/config"
	"github.com/robertodauria/msak/client/emitter"
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
)

type dialerFunc func(ctx context.Context, url string) (*websocket.Conn, error)

func defaultDialer(ctx context.Context, url string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{},
		ReadBufferSize:  spec.MaxMessageSize,
		WriteBufferSize: spec.MaxMessageSize,
	}
	headers := http.Header{}
	headers.Add("Sec-WebSocket-Protocol", "net.measurementlab.ndt.v7")
	conn, _, err := dialer.DialContext(ctx, url, headers)
	return conn, err
}

type Client struct {
	dialer     dialerFunc
	endpoint   string
	outputPath string
	config     *config.ClientConfig
	emitter    emitter.Emitter
}

const (
	DefaultDuration = 5 * time.Second
	DefaultDelay    = 0
)

func New(endpoint string) *Client {
	return NewWithConfig(endpoint, config.NewDefault())
}

func NewWithConfig(endpoint string, config *config.ClientConfig) *Client {
	return &Client{
		dialer:   defaultDialer,
		endpoint: endpoint,
		config:   config,
		emitter:  &emitter.LogEmitter{},
	}
}

// StartN starts N streams to run the specified subtest.
func (c *Client) StartN(ctx context.Context, kind spec.SubtestKind, n int, mid string) {
	wg := &sync.WaitGroup{}
	globalTimeout, cancel := context.WithTimeout(ctx, c.config.Duration)
	defer cancel()
	for i := 0; i < n; i++ {
		streamID := i
		wg.Add(2)
		// Make channel to handle measurements from this stream.
		measurements := make(chan results.Measurement)
		go func() {
			defer wg.Done()
			c.emitter.OnStart(kind, streamID)
			c.start(globalTimeout, kind, mid, measurements)
			c.emitter.OnComplete(kind, streamID)
		}()
		go func() {
			defer wg.Done()
			// Read the measurement channel and emit data. Stop when the channel is
			// closed, since there are no more measurements.
			for m := range measurements {
				c.emitter.OnMeasurement(kind, streamID, m)
			}
		}()
		time.Sleep(c.config.StreamsDelay)
	}

	wg.Wait()
}

func (r *Client) start(ctx context.Context, subtest spec.SubtestKind,
	mid string, measurements chan results.Measurement) error {
	switch subtest {
	case spec.SubtestDownload:
		return r.runDownload(ctx, mid, measurements)
	case spec.SubtestUpload:
		return r.runUpload(ctx, mid, measurements)
	default:
		return errors.New("invalid subtest")
	}
}

func (r *Client) runDownload(ctx context.Context, mid string, measurements chan results.Measurement) error {
	var conn *websocket.Conn
	mURL, err := url.Parse(string(r.config.Protocol) + "://" + r.endpoint +
		spec.DownloadPath + "?mid=" + mid)
	if err != nil {
		return err
	}
	if conn, err = r.dialer(ctx, mURL.String()); err != nil {
		return err
	}
	connInfo := &results.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
	}
	if err := ndtm.Receiver(ctx, conn, connInfo, measurements); err != nil {
		return err
	}
	return nil
}

func (r *Client) runUpload(ctx context.Context, mid string, measurements chan results.Measurement) error {
	var conn *websocket.Conn
	// TODO: get the mid from Locate.
	mURL, err := url.Parse(string(r.config.Protocol) + "://" + r.endpoint +
		spec.UploadPath + "?mid=" + mid)
	if err != nil {
		return err
	}
	if conn, err = r.dialer(ctx, mURL.String()); err != nil {
		return err
	}
	connInfo := &results.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
	}
	if err := ndtm.Sender(ctx, conn, connInfo, measurements); err != nil {
		return err
	}
	return nil
}
