package internal

// #include <linux/inet_diag.h>
// #include <netinet/ip.h>
// #include <netinet/tcp.h>
import "C"

import (
	"net"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/m-lab/go/rtx"
)

func SetCC(c *websocket.Conn, cc string) error {
	conn := c.UnderlyingConn().(*net.TCPConn)
	fp, err := conn.File()
	rtx.Must(err, "cannot get fd for the connection")
	// Note: Fd() returns uintptr but on Unix we can safely use int for sockets.
	return syscall.SetsockoptString(int(fp.Fd()), syscall.IPPROTO_TCP,
		syscall.TCP_CONGESTION, cc)
}
