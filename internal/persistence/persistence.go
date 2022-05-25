package persistence

import (
	"time"

	"github.com/m-lab/tcp-info/inetdiag"
	"github.com/m-lab/tcp-info/tcp"
)

// NDT7Result is the struct that is serialized as JSON to disk as the archival
// record of an NDT7 test. This is similar to, but independent from, the NDT5Result.
type NDT7Result struct {
	// GitShortCommit is the Git commit (short form) of the running server code.
	GitShortCommit string
	// Version is the symbolic version (if any) of the running server code.
	Version string

	// All data members should all be self-describing. In the event of confusion,
	// rename them to add clarity rather than adding a comment.
	ServerIP   string
	ServerPort int
	ClientIP   string
	ClientPort int

	StartTime time.Time
	EndTime   time.Time

	// ndt7
	Upload   *ArchivalData `json:",omitempty"`
	Download *ArchivalData `json:",omitempty"`
}

// ArchivalData saves all instantaneous measurements over the lifetime of a test.
type ArchivalData struct {
	UUID               string
	StartTime          time.Time
	EndTime            time.Time
	ServerMeasurements []Measurement
	ClientMeasurements []Measurement
}

// The Measurement struct contains measurement results. This structure is
// meant to be serialised as JSON as sent as a textual message. This
// structure is specified in the ndt7 specification.
type Measurement struct {
	AppInfo        *AppInfo        `json:",omitempty"`
	ConnectionInfo *ConnectionInfo `json:",omitempty" bigquery:"-"`
	BBRInfo        *BBRInfo        `json:",omitempty"`
	TCPInfo        *TCPInfo        `json:",omitempty"`
}

// AppInfo contains an application level measurement. This structure is
// described in the ndt7 specification.
type AppInfo struct {
	NumBytes    int64
	ElapsedTime int64
}

// ConnectionInfo contains connection info. This structure is described
// in the ndt7 specification.
type ConnectionInfo struct {
	Client string
	Server string
	UUID   string `json:",omitempty"`
}

// The BBRInfo struct contains information measured using BBR. This structure is
// an extension to the ndt7 specification. Variables here have the same
// measurement unit that is used by the Linux kernel.
type BBRInfo struct {
	inetdiag.BBRInfo
	ElapsedTime int64
}

// The TCPInfo struct contains information measured using TCP_INFO. This
// structure is described in the ndt7 specification.
type TCPInfo struct {
	tcp.LinuxTCPInfo
}
