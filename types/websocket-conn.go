package types

import (
	"github.com/Presslogic-Media/engine.io/v2/events"
	"github.com/gorilla/websocket"
)

type WebSocketConn struct {
	events.EventEmitter
	*websocket.Conn
}
