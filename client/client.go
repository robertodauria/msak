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
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

type dialerFunc func(ctx context.Context, url string) (*websocket.Conn, error)

func (c *Client) defaultDialer(ctx context.Context, url string) (*websocket.Conn, error) {
	c.dialer.ReadBufferSize = spec.MaxMessageSize
	c.dialer.WriteBufferSize = spec.MaxMessageSize
	headers := http.Header{}
	headers.Add("Sec-WebSocket-Protocol", "net.measurementlab.ndt.v7")
	conn, _, err := c.dialer.DialContext(ctx, url, headers)
	return conn, err
}

type Client struct {
	dialer     *websocket.Dialer
	endpoint   string
	outputPath string
	config     *config.ClientConfig
	emitter    emitter.Emitter

	startTime      time.Time
	bytesPerStream []int64
}

const (
	DefaultDuration = 10 * time.Second
	DefaultDelay    = 0
)

func New(endpoint string) *Client {
	return NewWithConfig(endpoint, &config.ClientConfig{
		Scheme:       config.WebSocketSecure,
		Duration:     DefaultDuration,
		StreamsDelay: DefaultDelay,
	})
}

func NewWithConfig(endpoint string, conf *config.ClientConfig) *Client {
	c := &Client{
		dialer:   websocket.DefaultDialer,
		endpoint: endpoint,
		config:   conf,
		emitter:  &emitter.LogEmitter{},
	}
	if conf.Scheme == config.WebSocketSecure {
		c.dialer.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: conf.NoVerify,
		}
	}
	return c
}

func (c *Client) SetEmitter(e emitter.Emitter) {
	c.emitter = e
}

func (c *Client) measurer(ctx context.Context, streamID int, kind spec.SubtestKind,
	measurement chan results.Measurement) {
	// read from the measurement channel, keep track of total bytes
	// sent/received and total elapsed time, display aggregate goodput.
	// Stop when the channel is closed by the sender.
	for m := range measurement {
		if m.Origin != "receiver" {
			continue
		}
		// update stream bytes and runtime.
		c.bytesPerStream[streamID] = m.AppInfo.NumBytes
		zap.L().Sugar().Debugw("measurement", "id", streamID, "NumBytes",
			m.AppInfo.NumBytes, "goodput", float64(m.AppInfo.NumBytes)/float64(m.AppInfo.ElapsedTime)*8)
		// aggregate throughput.
		sum := 0
		for _, b := range c.bytesPerStream {
			sum += int(b)
		}
		zap.L().Sugar().Infof("aggregate goodput: %f", float64(sum)/float64(time.Since(c.startTime).Seconds())*8/1000000)
	}
}

// StartN starts N streams to run the specified subtest.
func (c *Client) StartN(ctx context.Context, kind spec.SubtestKind, n int, mid string) {
	wg := &sync.WaitGroup{}
	c.bytesPerStream = make([]int64, n)
	globalTimeout, cancel := context.WithTimeout(ctx, c.config.Duration)
	defer cancel()
	// set the global startTime for this measurement.
	c.startTime = time.Now()
	for i := 0; i < n; i++ {
		streamID := i
		wg.Add(2)
		// channel to handle measurements from this stream.
		measurements := make(chan results.Measurement)
		go func() {
			defer wg.Done()
			c.emitter.OnStart(kind, streamID)
			err := c.start(globalTimeout, kind, mid, measurements)
			if err != nil {
				c.emitter.OnError(kind, err)
				return
			}
			c.emitter.OnComplete(kind, streamID)
		}()
		go func() {
			defer wg.Done()
			c.measurer(ctx, streamID, kind, measurements)
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

func (c *Client) runDownload(ctx context.Context, mid string, measurements chan results.Measurement) error {
	var conn *websocket.Conn
	mURL, err := url.Parse(string(c.config.Scheme) + "://" + c.endpoint + spec.DownloadPath)
	if err != nil {
		return err
	}
	params := mURL.Query()
	params.Add("mid", mid)
	params.Add("cc", c.config.CongestionControl)
	mURL.RawQuery = params.Encode()
	if conn, err = c.defaultDialer(ctx, mURL.String()); err != nil {
		close(measurements)
		return err
	}
	// This can (and will, when the OS != Linux) fail. Failure to get the cc
	// from the socket should not prevent the measurement from starting. The
	// "CC" field in ConnectionInfo will just be empty in this case.
	cc, err := getCCFromConn(conn)
	if err != nil {
		zap.L().Sugar().Warn("cannot get cc from conn", err)
	}
	connInfo := &results.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
		CC:     cc,
	}
	if err := ndtm.Receiver(ctx, conn, connInfo, measurements); err != nil {
		return err
	}
	return nil
}

func (c *Client) runUpload(ctx context.Context, mid string, measurements chan results.Measurement) error {
	var conn *websocket.Conn
	mURL, err := url.Parse(string(c.config.Scheme) + "://" + c.endpoint + spec.UploadPath)
	if err != nil {
		return err
	}
	params := mURL.Query()
	params.Add("mid", mid)
	params.Add("cc", c.config.CongestionControl)
	mURL.RawQuery = params.Encode()
	if conn, err = c.defaultDialer(ctx, mURL.String()); err != nil {
		close(measurements)
		return err
	}
	// This can (and will, when the OS != Linux) fail. Failure to get the cc
	// from the socket should not prevent the measurement from starting. The
	// "CC" field in ConnectionInfo will just be empty in this case.
	cc, err := getCCFromConn(conn)
	if err != nil {
		zap.L().Sugar().Warn("cannot get cc from conn", err)
	}
	connInfo := &results.ConnectionInfo{
		Server: conn.RemoteAddr().String(),
		Client: conn.LocalAddr().String(),
		CC:     cc,
	}
	zap.L().Sugar().Debug("ConnectionInfo:", connInfo)
	if err := ndtm.Sender(ctx, conn, connInfo, measurements); err != nil {
		return err
	}
	return nil
}

func getCCFromConn(conn *websocket.Conn) (string, error) {
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		return "", err
	}
	var cc string
	if cc, err = congestion.Get(fp); err == nil {
		return cc, nil
	}
	return "", err
}
