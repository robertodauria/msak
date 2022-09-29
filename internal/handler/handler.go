package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/prometheusx"
	"github.com/m-lab/go/rtx"
	"github.com/m-lab/go/warnonerror"
	"github.com/m-lab/uuid"
	"github.com/robertodauria/msak/internal/congestion"
	"github.com/robertodauria/msak/internal/netx"
	"github.com/robertodauria/msak/internal/persistence"
	"github.com/robertodauria/msak/pkg/ndtm"
	"github.com/robertodauria/msak/pkg/ndtm/results"
	"github.com/robertodauria/msak/pkg/ndtm/spec"
	"go.uber.org/zap"
	"gopkg.in/square/go-jose.v2/jwt"
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
	// Does the request include a mesaurement id? If not, return.
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
		zap.L().Sugar().Errorf("Cannot enable cc %s: %s", requestCC, err)
		// In case of failure, we still want to continue the measurement with
		// the current cc as long as we know what it is -- see below.
	}

	// Get the cc algorithm from the socket. This makes sure we set it
	// correctly in the result struct. A failure here prevents the measurement
	// from starting.
	cc, err := congestion.Get(fp)
	if err != nil {
		// TODO: increase a prometheus counter.
		zap.L().Sugar().Errorf("Cannot get cc from conn: %s", err)
		return
	}
	zap.L().Sugar().Debug("cc: ", cc)

	// Create measurement archival data.
	data, err := createResult(conn)
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
	data.CongestionControl = cc
	data.MeasurementID = mid

	// Run measurement.
	connInfo := getConnInfo(conn)
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

func createResult(conn *websocket.Conn) (*results.NDTMResult, error) {
	info := getConnInfo(conn)
	return &results.NDTMResult{
		GitShortCommit: prometheusx.GitShortCommit,
		Version:        "0", // XXX
		UUID:           info.UUID,
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
func getConnInfo(conn *websocket.Conn) *results.ConnectionInfo {
	fp, err := netx.GetFile(conn.UnderlyingConn())
	rtx.Must(err, "Failed to get fp for the websocket conn")
	// Get UUID for this TCP flow.
	uuid, err := uuid.FromFile(fp)
	rtx.Must(err, "Failed to get UUID for the websocket conn")
	return &results.ConnectionInfo{
		UUID:   uuid,
		Client: conn.RemoteAddr().String(),
		Server: conn.RemoteAddr().String(),
	}
}

func getMIDFromRequest(req *http.Request) (string, error) {
	// A measurement ID can be specified in two way: via a "mid" querystring
	// parameter (when access tokens are not required) or via the ID field
	// in the JWT access token.
	// Get the mid from the querystring if available.
	if mid := req.URL.Query().Get("mid"); mid != "" {
		return mid, nil
	}

	// Get the mid from the access token.
	token := req.URL.Query().Get("access_token")
	if token == "" {
		return "", ErrNoMeasurementID
	}

	// Attempt to parse the access token as a JWT token.
	jwtToken, err := jwt.ParseSigned(token)
	if err != nil {
		return "", err
	}

	// Access tokens' validity is verified by the access middleware, so we
	// don't verify it again here. We only extract the claims to get the ID.
	claims := &jwt.Claims{}
	err = jwtToken.UnsafeClaimsWithoutVerification(claims)
	if err != nil {
		return "", err
	}

	// Return the ID as mid.
	return claims.ID, nil

}
