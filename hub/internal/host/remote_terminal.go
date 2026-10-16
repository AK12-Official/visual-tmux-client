package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"golang.org/x/crypto/ssh"
)

// RemoteProcess implements terminal.Process over an SSH PTY session.
type RemoteProcess struct {
	conn     *ManagedConn
	session  *ssh.Session
	stdin    io.WriteCloser
	stdout   io.Reader
	mu       sync.Mutex
	closed   bool
	waitOnce sync.Once
	exitCode *int
	waitErr  error
}

// RemoteProcessFactory creates remote PTY-attached processes for terminal sessions.
type RemoteProcessFactory struct {
	lease    ClientLease
	tmuxPath string
}

func NewRemoteProcessFactory(lease ClientLease, tmuxPath string) *RemoteProcessFactory {
	if tmuxPath == "" {
		tmuxPath = "tmux"
	}
	return &RemoteProcessFactory{
		lease:    lease,
		tmuxPath: tmuxPath,
	}
}

// NewProcess initiates an SSH session with PTY and attaches to the target tmux session.
func (f *RemoteProcessFactory) NewProcess(ctx context.Context, sessionName string, cols, rows int) (terminal.Process, error) {
	if err := f.lease.CheckValid(); err != nil {
		return nil, err
	}

	conn, err := f.lease.OpenDedicated(ctx, "terminal")
	if err != nil {
		return nil, fmt.Errorf("open dedicated terminal transport: %w", err)
	}

	sshSession, err := conn.Client.NewSession()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open terminal session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          0, // disable local echo; remote tmux handles line editing
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}

	if err := sshSession.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sshSession.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdinPipe, err := sshSession.StdinPipe()
	if err != nil {
		_ = sshSession.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := sshSession.StdoutPipe()
	if err != nil {
		_ = sshSession.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	attachCmd := EncodePOSIXCommand(f.tmuxPath, "attach-session", "-t", tmux.ExactTarget(sessionName))
	if err := sshSession.Start(attachCmd); err != nil {
		_ = sshSession.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("start attach-session: %w", err)
	}

	return &RemoteProcess{
		conn:    conn,
		session: sshSession,
		stdin:   stdinPipe,
		stdout:  stdoutPipe,
	}, nil
}

func (p *RemoteProcess) Read(b []byte) (int, error) {
	if p.stdout == nil {
		return 0, io.EOF
	}
	return p.stdout.Read(b)
}

func (p *RemoteProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.stdin == nil {
		return 0, io.ErrClosedPipe
	}
	return p.stdin.Write(b)
}

func (p *RemoteProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.session == nil {
		return nil
	}
	return p.session.WindowChange(rows, cols)
}

func (p *RemoteProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var errs []error
	if p.stdin != nil {
		errs = append(errs, p.stdin.Close())
	}
	if p.session != nil {
		errs = append(errs, p.session.Close())
	}
	if p.conn != nil {
		errs = append(errs, p.conn.Close())
	}
	return errors.Join(errs...)
}

func (p *RemoteProcess) Detach() error {
	return p.Close()
}

func (p *RemoteProcess) Wait() (*int, error) {
	p.waitOnce.Do(func() {
		if p.session == nil {
			return
		}
		err := p.session.Wait()
		if err == nil {
			code := 0
			p.exitCode = &code
			return
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitStatus()
			p.exitCode = &code
			return
		}
		p.waitErr = err
	})
	return p.exitCode, p.waitErr
}
