package ws

import (
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
)

// Default transport boundaries.
const (
	// DefaultMaxInputMessage is the default maximum size of an incoming WebSocket message in bytes.
	DefaultMaxInputMessage int64 = 8 * 1024 * 1024 // 8 MiB
	// DefaultMaxDimension is the default maximum terminal column or row dimension.
	DefaultMaxDimension int = 1000
)

// Options specifies tuning and boundaries for the WebSocket transport.
type Options struct {
	Origin          string
	MaxInputMessage int64
	MaxDimension    int
	TerminalOptions terminal.Options
}

// DefaultOptions returns standard WebSocket transport options.
func DefaultOptions() Options {
	return Options{
		MaxInputMessage: DefaultMaxInputMessage,
		MaxDimension:    DefaultMaxDimension,
		TerminalOptions: terminal.DefaultOptions(),
	}
}
