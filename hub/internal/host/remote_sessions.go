package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"golang.org/x/crypto/ssh"
)

// RemoteSessionBackend implements session.Backend, session.PaneLister,
// and session.PaneDirectoryReader over an SSH ClientLease.
type RemoteSessionBackend struct {
	lease    ClientLease
	home     string
	tmuxPath string
}

func NewRemoteSessionBackend(lease ClientLease, home, tmuxPath string) *RemoteSessionBackend {
	if tmuxPath == "" {
		tmuxPath = "tmux"
	}
	if home == "" {
		home = "."
	}
	return &RemoteSessionBackend{
		lease:    lease,
		home:     home,
		tmuxPath: tmuxPath,
	}
}

// NewRemoteSessionService wraps the remote backend in the standard session service orchestration.
func NewRemoteSessionService(lease ClientLease, home, tmuxPath string) *session.Service {
	backend := NewRemoteSessionBackend(lease, home, tmuxPath)
	return session.NewService(backend)
}

func (b *RemoteSessionBackend) exec(ctx context.Context, isMutation bool, args ...string) (stdout, stderr string, exitCode int, err error) {
	if err := b.lease.CheckValid(); err != nil {
		return "", "", 0, err
	}
	client, err := b.lease.GetControl(ctx)
	if err != nil {
		return "", "", 0, err
	}

	sshSession, err := client.NewSession()
	if err != nil {
		b.lease.InvalidateControl(client)
		return "", "", 0, &Error{Target: b.lease.Target, Code: ErrUnavailable, Cause: err}
	}
	defer sshSession.Close()

	var outBuf, errBuf bytes.Buffer
	sshSession.Stdout = &outBuf
	sshSession.Stderr = &errBuf

	fullArgs := append([]string{b.tmuxPath}, args...)
	cmdText := EncodePOSIXCommand(fullArgs...)

	startErr := sshSession.Start(cmdText)
	if startErr != nil {
		b.lease.InvalidateControl(client)
		return outBuf.String(), errBuf.String(), 0, &Error{Target: b.lease.Target, Code: ErrUnavailable, Cause: startErr}
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- sshSession.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = sshSession.Close()
		b.lease.InvalidateControl(client)
		<-waitDone
		if isMutation {
			return outBuf.String(), errBuf.String(), 0, &Error{Target: b.lease.Target, Code: ErrOutcomeUnknown, Cause: ctx.Err()}
		}
		return outBuf.String(), errBuf.String(), 0, &Error{Target: b.lease.Target, Code: ErrUnavailable, Cause: ctx.Err()}
	case waitErr := <-waitDone:
		if waitErr != nil {
			var exitErr *ssh.ExitError
			if errors.As(waitErr, &exitErr) {
				return outBuf.String(), errBuf.String(), exitErr.ExitStatus(), nil
			}
			b.lease.InvalidateControl(client)
			if isMutation {
				return outBuf.String(), errBuf.String(), 0, &Error{Target: b.lease.Target, Code: ErrOutcomeUnknown, Cause: waitErr}
			}
			return outBuf.String(), errBuf.String(), 0, &Error{Target: b.lease.Target, Code: ErrUnavailable, Cause: waitErr}
		}
		return outBuf.String(), errBuf.String(), 0, nil
	}
}

func (b *RemoteSessionBackend) HasSession(ctx context.Context, name string) bool {
	_, _, code, err := b.exec(ctx, false, tmux.HasSessionArgs(name)...)
	return err == nil && code == 0
}

func (b *RemoteSessionBackend) generateUniqueName(ctx context.Context) (string, error) {
	base := time.Now().Format("session-20060102-150405")
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if !b.HasSession(ctx, name) {
			return name, nil
		}
	}
	return "", session.ErrNameInUse
}

func (b *RemoteSessionBackend) List(ctx context.Context) ([]session.Session, error) {
	stdout, stderr, code, err := b.exec(ctx, false, tmux.ListArgs()...)
	if err != nil {
		return nil, fmt.Errorf("exec list-sessions: %w", err)
	}
	if code != 0 {
		if tmux.IsNoServer(stderr) {
			return []session.Session{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %s", strings.TrimSpace(stderr))
	}
	return tmux.ParseSessions(stdout), nil
}

func (b *RemoteSessionBackend) Get(ctx context.Context, name string) (*session.Session, error) {
	sessions, err := b.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].Name == name {
			return &sessions[i], nil
		}
	}
	return nil, session.ErrNotFound
}

func (b *RemoteSessionBackend) Create(ctx context.Context, name string) (*session.Session, error) {
	if name == "" {
		generated, err := b.generateUniqueName(ctx)
		if err != nil {
			return nil, fmt.Errorf("generate unique name: %w", err)
		}
		name = generated
	}

	args := tmux.CreateArgs(name, b.home)
	_, stderr, code, err := b.exec(ctx, true, args...)
	if err != nil {
		return nil, fmt.Errorf("exec new-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return nil, session.ErrNameInUse
		}
		return nil, fmt.Errorf("tmux new-session: %s", strings.TrimSpace(stderr))
	}
	sess, err := b.Get(ctx, name)
	if err != nil {
		return &session.Session{Name: name, Windows: 1}, nil
	}
	return sess, nil
}

func (b *RemoteSessionBackend) Rename(ctx context.Context, oldName, newName string) error {
	if oldName == newName {
		return nil
	}

	_, stderr, code, err := b.exec(ctx, true, tmux.RenameArgs(oldName, newName)...)
	if err != nil {
		return fmt.Errorf("exec rename-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return session.ErrNameInUse
		}
		if strings.Contains(stderr, "can't find session") || tmux.IsNoServer(stderr) {
			return session.ErrNotFound
		}
		return fmt.Errorf("tmux rename-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

func (b *RemoteSessionBackend) Kill(ctx context.Context, name string) error {
	_, stderr, code, err := b.exec(ctx, true, tmux.KillArgs(name)...)
	if err != nil {
		return fmt.Errorf("exec kill-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "can't find session") || tmux.IsNoServer(stderr) {
			return session.ErrNotFound
		}
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

func (b *RemoteSessionBackend) ListPanes(ctx context.Context) ([]session.Pane, error) {
	stdout, stderr, code, err := b.exec(ctx, false, tmux.ListPanesArgs()...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if tmux.IsNoServer(stderr) {
			return []session.Pane{}, nil
		}
		return nil, fmt.Errorf("tmux list-panes: %s", strings.TrimSpace(stderr))
	}
	return tmux.ParsePanes(stdout), nil
}

func (b *RemoteSessionBackend) PaneWorkingDirectory(ctx context.Context, name string) (string, error) {
	stdout, stderr, code, err := b.exec(ctx, false, tmux.WorkingDirectoryArgs(name)...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		if strings.Contains(stderr, "can't find session") || tmux.IsNoServer(stderr) {
			return "", session.ErrNotFound
		}
		return "", fmt.Errorf("tmux pane working directory: %s", strings.TrimSpace(stderr))
	}
	path := strings.TrimSpace(stdout)
	if path == "" {
		return "", session.ErrNotFound
	}
	return path, nil
}
