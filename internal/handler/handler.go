package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/access/controller"
	"github.com/m-lab/go/prometheusx"
	"github.com/m-lab/go/warnonerror"
	"github.com/m-lab/uuid"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
)

var ErrNoMeasurementID = errors.New("no measurement ID specified in the request")

// Handler handles the msak subtests.
type Handler struct {
	dataDir string
}

// writeBadRequest sends a Bad Request response to the client using writer.
func writeBadRequest(writer http.ResponseWriter) {
	writer.Header().Set("Connection", "Close")
	writer.WriteHeader(http.StatusBadRequest)
}

// New creates a new Handler.
func New(dataDir string) *Handler {
	return &Handler{
		dataDir: dataDir,
	}
}

// Download handles the download subtest.
func (h *Handler) Download(rw http.ResponseWriter, req *http.Request) {
	h.runMeasurement(spec.SubtestDownload, rw, req)
}

// Upload handles the upload subtest.
func (h *Handler) Upload(rw http.ResponseWriter, req *http.Request) {
	h.runMeasurement(spec.SubtestUpload, rw, req)
}

func (h *Handler) runMeasurement(kind spec.SubtestKind, rw http.ResponseWriter,
	req *http.Request) {
	// Does the request include a measurement id? If not, return.
	mid, err := getMIDFromRequest(req)
	if err != nil {
		zap.L().Sugar().Infow("Received request without measurement id",
			"url", req.URL.String(),
			"client", req.RemoteAddr,
			"error", err)
		writeBadRequest(rw)
		return
	}

	zap.L().Sugar().Debug("mid: ", mid)

	// Does the request include a custom cc? If not, use BBR.
	requestCC := req.URL.Query().Get("cc")
	if requestCC == "" {
		requestCC = "bbr"
	}

	// Upgrade connection to websocket.
	zap.L().Sugar().Debugw("Upgrading connection to websocket",
		"url", req.URL.String(),
		"headers", req.Header,
	)
	conn, err := ndtm.Upgrade(rw, req)
	if err != nil {
		// TODO: increase a prometheus counter here.
		zap.L().Sugar().Warn("Websocket upgrade failed", err)
		writeBadRequest(rw)
		return
	}

	// Make sure the connection is closed after (at most) MaxRuntime.
	ctx, cancel := context.WithTimeout(req.Context(), spec.MaxRuntime)
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Set congestion control algorithm for this connection.
	fp, err := netx.GetFile(conn.UnderlyingConn())
	if err != nil {
		zap.L().Sugar().Error("Cannot get the connection's fp: %s", err)
	}
	err = congestion.Set(fp, requestCC)
	if err != nil {
		zap.L().Sugar().Errorf("Cannot enable cc %s: %v", requestCC, err)
		// In case of failure, we still want to continue the measurement with
		// the current cc as long as we know what it is -- see below.
	}

	// Get the cc algorithm from the socket. This makes sure we set it
	// correctly in the result struct. A failure here prevents the measurement
	// from starting.
	connInfo, err := getConnInfo(conn)
	if err != nil {
		zap.L().Sugar().Error("Cannot get connection info: ", err)
	}
	zap.L().Sugar().Debug("cc: ", connInfo.CC)

	// Create measurement archival data.
	data, err := createResult(connInfo.UUID)
	if err != nil {
		// TODO: increase a prometheus counter.
		zap.L().Sugar().Warn("Cannot create result", err)
		return
	}
	// TODO: increase a prometheus counter here.

	data.StartTime = time.Now().UTC()
	defer func() {
		data.EndTime = time.Now().UTC()
		h.writeResult(data.UUID, kind, data)
	}()
	data.SubType = string(kind)
	data.CongestionControl = connInfo.CC
	data.MeasurementID = mid

	// Run measurement.
	measurements := make(chan results.Measurement, 64)

	// Drain the measurement channel and append the measurement to the correct
	// field in the result struct according to the origin.
	go func() {
		for m := range measurements {
			// The measurement protocol has a sender and a receiver. The
			// result struct has a server and a client. We need to append the
			// measurement to the right slice here.
			switch kind {
			case spec.SubtestDownload:
				if m.Origin == "sender" {
					data.ServerMeasurements = append(data.ServerMeasurements, m)
				} else {
					data.ClientMeasurements = append(data.ClientMeasurements, m)
				}
			case spec.SubtestUpload:
				if m.Origin == "receiver" {
					data.ServerMeasurements = append(data.ServerMeasurements, m)
				} else {
					data.ClientMeasurements = append(data.ClientMeasurements, m)
				}
			}

			zap.L().Sugar().Debugw("Measurement received",
				"origin", m.Origin)
		}
		zap.L().Sugar().Debug("Done receiving from measurement channel")
	}()

	// Start the sender or the receiver according to the subtest kind.
	if kind == spec.SubtestDownload {
		ndtm.Sender(ctx, conn, connInfo, measurements)
	} else {
		ndtm.Receiver(ctx, conn, connInfo, measurements)
	}
}

func createResult(connUUID string) (*results.NDTMResult, error) {
	return &results.NDTMResult{
		GitShortCommit: prometheusx.GitShortCommit,
		Version:        "0", // XXX
		UUID:           connUUID,
	}, nil
}

func (h Handler) writeResult(uuid string, kind spec.SubtestKind, result *results.NDTMResult) {
	fp, err := persistence.New(h.dataDir, string(kind), uuid)
	if err != nil {
		zap.L().Sugar().Error("results.NewFile failed", err)
		return
	}
	if err := fp.Write(result); err != nil {
		zap.L().Sugar().Error("failed to write result", err)
	}
	warnonerror.Close(fp, string(kind)+": ignoring fp.Close error")
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

// getMIDFromRequest extracts the measurement id ("mid") from a given HTTP
// request, if present.
//
// A measurement ID can be specified in two ways: via a "mid" querystring
// parameter (when access tokens are not required) or via the ID field
// in the JWT access token.
func getMIDFromRequest(req *http.Request) (string, error) {

	// If the request includes a valid JWT token, the claim and the ID are in
	// the request's context already.
	claims := controller.GetClaim(req.Context())
	if claims != nil {
		return claims.ID, nil
	}

	// Otherwise, get the mid from the querystring.
	if mid := req.URL.Query().Get("mid"); mid != "" {
		return mid, nil
	}

	return "", errors.New("no valid JWT token or mid")
}
