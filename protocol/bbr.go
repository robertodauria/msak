package protocol

import (
	"os"
	"syscall"
)

func EnableBBR(fp *os.File) error {
	// Note: Fd() returns uintptr but on Unix we can safely use int for sockets.
	return syscall.SetsockoptString(int(fp.Fd()), syscall.IPPROTO_TCP,
		syscall.TCP_CONGESTION, "bbr")
}
