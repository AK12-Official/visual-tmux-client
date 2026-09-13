package session

import "errors"

// Sentinel errors distinguishing session business outcomes.
var (
	ErrTmuxNotFound = errors.New("tmux: executable not found")
	ErrNameInUse    = errors.New("session name already in use")
	ErrNotFound     = errors.New("session not found")
	ErrInvalidName  = errors.New("invalid session name")
)
