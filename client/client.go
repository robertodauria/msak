package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/warnonerror"
	v2 "github.com/m-lab/locate/api/v2"
	"github.com/m-lab/uuid"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

const (
	// DefaultWebSocketHandshakeTimeout is the default timeout used by the client
	// for the WebSocket handshake.
	DefaultWebSocketHandshakeTimeout = 5 * time.Second

	// DefaultStreams is the default number of streams for a new client.
	DefaultStreams = 5

	libraryName    = "msak-client"
	libraryVersion = "0.0.1"
)

type Locator interface {
	Nearest(ctx context.Context, service string) ([]v2.Target, error)
}

type NDTMClient struct {
	ClientName    string
	ClientVersion string

	Dialer *websocket.Dialer

	Server     string
	ServiceURL *url.URL

	Locate Locator

	Scheme string

	NumStreams        int
	Length            time.Duration
	Delay             time.Duration
	CongestionControl string
	MeasurementID     string

	OutputPath    string
	ResultsByUUID map[string]*results.NDTMResult
}

// makeUserAgent creates the user agent string
func makeUserAgent(clientName, clientVersion string) string {
	return clientName + "/" + clientVersion + " " + libraryName + "/" + libraryVersion
}

func New2(clientName, clientVersion string) *NDTMClient {
	return &NDTMClient{
		ClientName:    clientName,
		ClientVersion: clientVersion,
		Dialer: &websocket.Dialer{
			HandshakeTimeout: DefaultWebSocketHandshakeTimeout,
		},
		ResultsByUUID: make(map[string]*results.NDTMResult),
		Scheme:        "wss",
	}
}

func (c *NDTMClient) connect(ctx context.Context, serviceURL *url.URL) (*websocket.Conn, error) {
	q := serviceURL.Query()
	q.Set("client_arch", runtime.GOARCH)
	q.Set("client_library_name", libraryName)
	q.Set("client_library_version", libraryVersion)
	q.Set("client_os", runtime.GOOS)
	q.Set("client_name", c.ClientName)
	q.Set("client_version", c.ClientVersion)
	serviceURL.RawQuery = q.Encode()
	headers := http.Header{}
	headers.Add("Sec-WebSocket-Protocol", spec.SecWebSocketProtocol)
	headers.Add("User-Agent", makeUserAgent(c.ClientName, c.ClientVersion))
	conn, _, err := c.Dialer.DialContext(ctx, serviceURL.String(), headers)
	return conn, err
}

func (c *NDTMClient) start(ctx context.Context, subtest spec.SubtestKind) error {
	var mURL *url.URL
	// If the server has been provided, use it and use default paths based on
	// the subtest kind (download/upload).
	if c.Server != "" {
		path := getPathForSubtest(subtest)
		mURL = &url.URL{
			Scheme: c.Scheme,
			Host:   c.Server,
			Path:   path,
		}
		q := mURL.Query()
		q.Set("mid", c.MeasurementID)
		mURL.RawQuery = q.Encode()
	}

	// TODO: Use Locate for when the server has not been provided.

	if mURL == nil {
		return errors.New("no server provided")
	}

	wg := &sync.WaitGroup{}
	globalTimeout, cancel := context.WithTimeout(ctx, c.Length)
	defer cancel()

	for i := 0; i < c.NumStreams; i++ {
		wg.Add(2)
		measurements := make(chan results.Measurement)
		result := &results.NDTMResult{
			MeasurementID:      c.MeasurementID,
			SubTest:            string(subtest),
			ServerMeasurements: make([]results.Measurement, 0),
			ClientMeasurements: make([]results.Measurement, 0),
		}

		go func() {
			defer wg.Done()
			zap.L().Sugar().Debug("connecting to ", mURL.String())
			// Connect to mURL.
			conn, err := c.connect(ctx, mURL)
			if err != nil {
				zap.L().Sugar().Error(err)
				return
			}
			// To store measurement results we use a map associating the
			// TCP flow's unique identifier to the corresponding results.
			info, err := getConnInfo(conn)
			if err != nil {
				zap.L().Sugar().Error(err)
				return
			}

			result.UUID = info.UUID
			result.CongestionControl = info.CC
			result.StartTime = time.Now().UTC()
			c.ResultsByUUID[info.UUID] = result

			switch subtest {
			case spec.SubtestDownload:
				err = ndtm.Receiver(globalTimeout, conn, info, measurements)
			case spec.SubtestUpload:
				err = ndtm.Sender(globalTimeout, conn, info, measurements)
			}

			if err != nil {
				zap.L().Sugar().Error(err)
			}

			result.EndTime = time.Now().UTC()
		}()

		go func() {
			defer wg.Done()
			c.measurer(result, measurements)
		}()

		time.Sleep(c.Delay)
	}

	wg.Wait()

	// If an output path was specified, write the results as JSON.
	if c.OutputPath != "" {
		for uuid, v := range c.ResultsByUUID {
			c.writeResult(uuid, subtest, v)
		}
	}

	return nil
}

func (c *NDTMClient) measurer(result *results.NDTMResult, measurements chan results.Measurement) {
	for m := range measurements {
		zap.L().Sugar().Debugw("Measurement received", "origin", m.Origin, "AppInfo", m.AppInfo)
		switch result.SubTest {
		case string(spec.SubtestDownload):
			if m.Origin == "sender" {
				result.ServerMeasurements = append(result.ServerMeasurements, m)
			} else {
				result.ClientMeasurements = append(result.ClientMeasurements, m)
			}
		case string(spec.SubtestUpload):
			if m.Origin == "sender" {
				result.ClientMeasurements = append(result.ClientMeasurements, m)
			} else {
				result.ServerMeasurements = append(result.ServerMeasurements, m)
			}
		}
	}
}

func (c *NDTMClient) Download(ctx context.Context) {
	c.start(ctx, spec.SubtestDownload)
}

func getPathForSubtest(subtest spec.SubtestKind) string {
	switch subtest {
	case spec.SubtestDownload:
		return spec.DownloadPath
	case spec.SubtestUpload:
		return spec.UploadPath
	default:
		return "invalid"
	}
}

// Return a ConnectionInfo struct for the given websocket connection.
func getConnInfo(conn *websocket.Conn) (*results.ConnectionInfo, error) {
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		return nil, err
	}
	cc, err := congestion.Get(fp)
	if err != nil {
		return nil, err
	}
	// Get UUID for this TCP flow.
	uuid, err := uuid.FromFile(fp)
	if err != nil {
		return nil, err
	}
	return &results.ConnectionInfo{
		UUID:   uuid,
		Client: conn.RemoteAddr().String(),
		Server: conn.RemoteAddr().String(),
		CC:     cc,
	}, nil
}

func (c *NDTMClient) writeResult(uuid string, kind spec.SubtestKind, result *results.NDTMResult) {
	fp, err := persistence.New(c.OutputPath, string(kind), uuid)
	if err != nil {
		zap.L().Sugar().Error("results.NewFile failed", err)
		return
	}
	if err := fp.Write(result); err != nil {
		zap.L().Sugar().Error("failed to write result", err)
	}
	warnonerror.Close(fp, string(kind)+": ignoring fp.Close error")
}
