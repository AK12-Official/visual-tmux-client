package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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

const (
	defaultTerminalSpeed        = 14400
	defaultTerminalSetupTimeout = 30 * time.Second
)

func normalizeTerminalDimensions(cols, rows int) (int, int) {
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}
	return cols, rows
}

func startSSHSession(
	setupCtx context.Context,
	client *ssh.Client,
	tmuxPath, sessionName string,
	cols, rows int,
) (*ssh.Session, io.WriteCloser, io.Reader, error) {
	sshSession, err := client.NewSession()
	if err != nil {
		if setupCtx.Err() != nil {
			return nil, nil, nil, fmt.Errorf("open terminal session timed out or cancelled: %w", setupCtx.Err())
		}
		return nil, nil, nil, fmt.Errorf("open terminal session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          0, // disable local echo; remote tmux handles line editing
		ssh.TTY_OP_ISPEED: defaultTerminalSpeed,
		ssh.TTY_OP_OSPEED: defaultTerminalSpeed,
	}

	cols, rows = normalizeTerminalDimensions(cols, rows)
	if err := sshSession.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = sshSession.Close() //nolint:errcheck // teardown on failure
		if setupCtx.Err() != nil {
			return nil, nil, nil, fmt.Errorf("request pty timed out or cancelled: %w", setupCtx.Err())
		}
		return nil, nil, nil, fmt.Errorf("request pty: %w", err)
	}

	stdinPipe, err := sshSession.StdinPipe()
	if err != nil {
		_ = sshSession.Close() //nolint:errcheck // teardown on failure
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := sshSession.StdoutPipe()
	if err != nil {
		_ = sshSession.Close() //nolint:errcheck // teardown on failure
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	attachCmd := EncodePOSIXCommand(tmuxPath, "attach-session", "-t", tmux.ExactTarget(sessionName))
	if err := sshSession.Start(attachCmd); err != nil {
		_ = sshSession.Close() //nolint:errcheck // teardown on failure
		if setupCtx.Err() != nil {
			return nil, nil, nil, fmt.Errorf("start attach-session timed out or cancelled: %w", setupCtx.Err())
		}
		return nil, nil, nil, fmt.Errorf("start attach-session: %w", err)
	}

	return sshSession, stdinPipe, stdoutPipe, nil
}

// NewProcess initiates an SSH session with PTY and attaches to the target tmux session.
func (f *RemoteProcessFactory) NewProcess(
	ctx context.Context, sessionName string, cols, rows int,
) (terminal.Process, error) {
	if err := f.lease.CheckValid(); err != nil {
		return nil, err
	}

	setupCtx, cancelSetup := context.WithTimeout(ctx, defaultTerminalSetupTimeout)
	defer cancelSetup()

	conn, err := f.lease.OpenDedicated(setupCtx, "terminal")
	if err != nil {
		return nil, fmt.Errorf("open dedicated terminal transport: %w", err)
	}

	var (
		handover  bool
		mu        sync.Mutex
		closeOnce sync.Once
	)

	stopSetupWatcher := make(chan struct{})
	setupDone := make(chan struct{})

	stopWatcher := func() {
		closeOnce.Do(func() {
			close(stopSetupWatcher)
			<-setupDone
		})
	}

	go func() {
		select {
		case <-setupCtx.Done():
			mu.Lock()
			if !handover {
				_ = conn.Close() //nolint:errcheck // abort hanging setup
			}
			mu.Unlock()
		case <-stopSetupWatcher:
		}
		close(setupDone)
	}()

	succeeded := false
	defer func() {
		if !succeeded {
			stopWatcher()
			_ = conn.Close() //nolint:errcheck // teardown on failure
		}
	}()

	sshSession, stdinPipe, stdoutPipe, err := startSSHSession(
		setupCtx, conn.Client, f.tmuxPath, sessionName, cols, rows,
	)
	if err != nil {
		return nil, err
	}

	mu.Lock()
	if setupCtx.Err() != nil {
		mu.Unlock()
		_ = sshSession.Close() //nolint:errcheck // teardown on failure
		return nil, fmt.Errorf("terminal setup cancelled: %w", setupCtx.Err())
	}
	handover = true
	succeeded = true
	mu.Unlock()
	stopWatcher()

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
