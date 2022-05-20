//go:build !linux

package internal

import "github.com/gorilla/websocket"

func setCC(conn *websocket.Conn, cc string) error {
	return nil
}
