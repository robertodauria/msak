package spec

import "time"

const (
	MinMessageSize  = 1 << 10
	MaxMessageSize  = 1 << 20
	MeasureInterval = 250 * time.Millisecond
	ScalingFraction = 16

	DownloadPath = "/msak/ndtm/download"
	UploadPath   = "/msak/ndtm/upload"

	MaxRuntime = 15 * time.Second

	// SecWebSocketProtocol is the value of the Sec-WebSocket-Protocol header.
	SecWebSocketProtocol = "net.measurementlab.ndt.m"
)

// SubtestKind indicates the subtest kind
type SubtestKind string

const (
	// SubtestDownload is a download subtest
	SubtestDownload = SubtestKind("download")

	// SubtestUpload is a upload subtest
	SubtestUpload = SubtestKind("upload")
)
