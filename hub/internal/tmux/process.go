package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/creack/pty"
)

// PTYProcess wraps a pty-backed tmux attach process with thread-safe lifecycle control.
type PTYProcess struct {
	cmd       *exec.Cmd
	ptmx      *os.File
	ptyMu     sync.Mutex
	ptyClosed bool

	waitOnce sync.Once
	exitCode *int
	waitErr  error
}

// NewProcess starts a pty-backed attach-session process against the target session.
func (c *Client) NewProcess(ctx context.Context, sess string, cols, rows int) (terminal.Process, error) {
	if c.err != nil {
		return nil, c.err
	}
	if !c.HasSession(ctx, sess) {
		return nil, session.ErrNotFound
	}

	if err := c.EnsureGlobalOptions(ctx); err != nil {
		return nil, fmt.Errorf("ensure global options: %w", err)
	}

	cmd := c.AttachCommand(sess)
	winsize := &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	}
	ptmx, err := pty.StartWithSize(cmd, winsize)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	return &PTYProcess{
		cmd:  cmd,
		ptmx: ptmx,
	}, nil
}

// Read reads output from the pty master.
func (p *PTYProcess) Read(b []byte) (int, error) {
	if p.ptmx == nil {
		return 0, os.ErrClosed
	}
	return p.ptmx.Read(b)
}

// Write writes input bytes to the pty master.
func (p *PTYProcess) Write(b []byte) (int, error) {
	if p.ptmx == nil {
		return 0, os.ErrClosed
	}
	return p.ptmx.Write(b)
}

// Resize resizes the pty master descriptor safely under ptyMu.
func (p *PTYProcess) Resize(cols, rows int) error {
	p.ptyMu.Lock()
	defer p.ptyMu.Unlock()
	if p.ptyClosed || p.ptmx == nil {
		return nil
	}
	return pty.Setsize(p.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Close closes the pty master descriptor under ptyMu, avoiding ioctl races on recycled fds.
func (p *PTYProcess) Close() error {
	p.ptyMu.Lock()
	defer p.ptyMu.Unlock()
	if p.ptyClosed || p.ptmx == nil {
		return nil
	}
	p.ptyClosed = true
	return p.ptmx.Close()
}

// Detach closes the pty master and terminates the attach child without affecting the tmux session.
func (p *PTYProcess) Detach() error {
	var errs []error
	if err := p.Close(); err != nil {
		errs = append(errs, err)
	}
	if p.cmd != nil && p.cmd.Process != nil {
		if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Wait reaps the attach child once and caches the exit code for subsequent callers.
func (p *PTYProcess) Wait() (*int, error) {
	p.waitOnce.Do(func() {
		if p.cmd == nil {
			return
		}
		err := p.cmd.Wait()
		if err == nil {
			code := 0
			p.exitCode = &code
			return
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code := ee.ExitCode()
			p.exitCode = &code
			return
		}
		p.waitErr = err
	})
	return p.exitCode, p.waitErr
}
