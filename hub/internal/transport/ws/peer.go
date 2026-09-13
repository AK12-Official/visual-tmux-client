package ws

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/websocket"
)

// ConnPeer adapts a *websocket.Conn to the terminal.Peer interface.
type ConnPeer struct {
	conn *websocket.Conn
}

// NewConnPeer wraps a WebSocket connection.
func NewConnPeer(c *websocket.Conn) *ConnPeer {
	return &ConnPeer{conn: c}
}

// SendText marshals v to JSON and writes a text frame.
func (p *ConnPeer) SendText(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal text message: %w", err)
	}
	return p.conn.Write(ctx, websocket.MessageText, b)
}

// SendBinary writes raw bytes as a binary frame.
func (p *ConnPeer) SendBinary(ctx context.Context, data []byte) error {
	return p.conn.Write(ctx, websocket.MessageBinary, data)
}

// ReadMessage reads the next frame, returning its type, payload, and error.
func (p *ConnPeer) ReadMessage(ctx context.Context) (int, []byte, error) {
	msgType, data, err := p.conn.Read(ctx)
	return int(msgType), data, err
}

// Close gracefully closes the WebSocket with the given status code and reason.
func (p *ConnPeer) Close(code int, reason string) error {
	return p.conn.Close(websocket.StatusCode(code), reason)
}

// CloseNow abruptly cuts the connection.
func (p *ConnPeer) CloseNow() error {
	return p.conn.CloseNow()
}
