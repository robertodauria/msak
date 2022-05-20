package internal

import (
	"github.com/gorilla/websocket"
)

func SetCC(conn *websocket.Conn, cc string) error {
	return setCC(conn, cc)
}
