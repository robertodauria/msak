package protocol

import "time"

const (
	MinMessageSize      = 1 << 10
	MaxMessageSize      = 1 << 24
	MeasureInterval     = 250 * time.Millisecond
	DefaultReadDeadline = 10 * time.Second
	ScalingFraction     = 16
)
