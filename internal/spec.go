package internal

import "time"

const (
	MinMessageSize  = 1 << 10
	MaxMessageSize  = 1 << 20
	MeasureInterval = 250 * time.Millisecond
	ScalingFraction = 16

	DownloadPath = "/ndt/v7/download"
	UploadPath   = "/ndt/v7/upload"
)

type Test uint8

const (
	Download Test = iota
	Upload
)
