package terminal

import (
	"context"
	"io"
)

// Process encapsulates an attached terminal process with PTY communication and lifecycle control.
type Process interface {
	io.Reader
	io.Writer
	Resize(cols, rows int) error
	Close() error
	Wait() (*int, error)
	Detach() error
}

// ProcessFactory constructs a new Process attached to a target session.
type ProcessFactory interface {
	NewProcess(ctx context.Context, session string, cols, rows int) (Process, error)
}

// Peer represents a duplex transport connection (e.g. WebSocket client connection).
type Peer interface {
	SendText(ctx context.Context, v any) error
	SendBinary(ctx context.Context, data []byte) error
	ReadMessage(ctx context.Context) (messageType int, data []byte, err error)
	Close(code int, reason string) error
	CloseNow() error
}
