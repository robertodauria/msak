package internal

import (
	"net"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
)

func setCC(c *websocket.Conn, cc string) error {
	conn := c.UnderlyingConn().(*net.TCPConn)
	fp, err := conn.File()
	rtx.Must(err, "cannot get fd for the connection")
	// Note: Fd() returns uintptr but on Unix we can safely use int for sockets.
	return syscall.SetsockoptString(int(fp.Fd()), syscall.IPPROTO_TCP,
		syscall.TCP_CONGESTION, cc)
}
