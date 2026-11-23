// Package spike supplies a disposable backend for native container verification.
// It deliberately cannot start a process or mutate tmux sessions.
package spike

import (
	"context"
	"errors"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
)

var errReadOnly = errors.New("desktop compatibility spike: session operations are disabled")

// Backend implements the session backend with one synthetic session.
type Backend struct{}

func (Backend) List(context.Context) ([]session.Session, error) {
	return []session.Session{{Name: "desktop-spike", Windows: 1}}, nil
}

func (Backend) Create(context.Context, string) (*session.Session, error) {
	return nil, errReadOnly
}

func (Backend) Rename(context.Context, string, string) error { return errReadOnly }

func (Backend) Kill(context.Context, string) error { return errReadOnly }

// Processes refuses attachments; it never invokes tmux or starts a shell.
type Processes struct{}

func (Processes) NewProcess(context.Context, string, int, int) (terminal.Process, error) {
	return nil, errReadOnly
}
