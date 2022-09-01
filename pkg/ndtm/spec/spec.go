package spec

import "time"

const (
	MinMessageSize  = 1 << 10
	MaxMessageSize  = 1 << 20
	MeasureInterval = 250 * time.Millisecond
	ScalingFraction = 16

	DownloadPath = "/ndt/v7/download"
	UploadPath   = "/ndt/v7/upload"

	MaxRuntime = 15 * time.Second
)

// SubtestKind indicates the subtest kind
type SubtestKind string

const (
	// SubtestDownload is a download subtest
	SubtestDownload = SubtestKind("download")

	// SubtestUpload is a upload subtest
	SubtestUpload = SubtestKind("upload")
)
