package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// Client executes tmux commands against a socket and implements session.Backend.
type Client struct {
	path      string
	socket    string
	err       error
	optionsMu sync.Mutex
}

// NewClient resolves the tmux binary and constructs a Client.
// If tmux cannot be resolved, an ErrTmuxNotFound error is recorded so the hub can start
// and report failures per operation.
func NewClient(tmuxPath, socket string) *Client {
	c := &Client{socket: socket}
	if tmuxPath != "" {
		if _, err := os.Stat(tmuxPath); err != nil {
			c.err = fmt.Errorf("%w: %w", session.ErrTmuxNotFound, err)
			return c
		}
		c.path = tmuxPath
		return c
	}

	p, err := exec.LookPath("tmux")
	if err != nil {
		c.err = fmt.Errorf("%w: %w", session.ErrTmuxNotFound, err)
		return c
	}
	c.path = p
	return c
}

// Exec runs a tmux command with the given arguments, using a scrubbed environment.
func (c *Client) Exec(ctx context.Context, args ...string) (stdout, stderr string, exitCode int, err error) {
	if c.err != nil {
		return "", "", 0, c.err
	}
	full := args
	if c.socket != "" {
		full = append([]string{"-L", c.socket}, args...)
	}

	cmd := exec.CommandContext(ctx, c.path, full...)
	cmd.Env = ScrubbedEnv()

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	execErr := cmd.Run()
	if execErr != nil {
		var ee *exec.ExitError
		if errors.As(execErr, &ee) {
			return outBuf.String(), errBuf.String(), ee.ExitCode(), nil
		}
		return outBuf.String(), errBuf.String(), 0, execErr
	}
	return outBuf.String(), errBuf.String(), 0, nil
}

// EnsureGlobalOptions configures tmux's global options (`window-size latest` and `mouse on`).
// Inspects each option before setting to avoid redundant client repaints.
func (c *Client) EnsureGlobalOptions(ctx context.Context) error {
	if c.err != nil {
		return c.err
	}
	c.optionsMu.Lock()
	defer c.optionsMu.Unlock()

	winOut, _, winCode, winErr := c.Exec(ctx, "show-options", "-gv", "window-size")
	if winErr != nil || winCode != 0 || strings.TrimSpace(winOut) != "latest" {
		if _, _, _, err := c.Exec(ctx, "set-option", "-g", "window-size", "latest"); err != nil {
			return fmt.Errorf("set-option window-size: %w", err)
		}
	}

	mouseOut, _, mouseCode, mouseErr := c.Exec(ctx, "show-options", "-gv", "mouse")
	if mouseErr != nil || mouseCode != 0 || strings.TrimSpace(mouseOut) != "on" {
		if _, _, _, err := c.Exec(ctx, "set-option", "-g", "mouse", "on"); err != nil {
			return fmt.Errorf("set-option mouse: %w", err)
		}
	}
	return nil
}

// HasSession checks whether a session exists by exact name.
func (c *Client) HasSession(ctx context.Context, name string) bool {
	_, _, code, err := c.Exec(ctx, "has-session", "-t", ExactTarget(name))
	return err == nil && code == 0
}

// List returns all active tmux sessions. If no server is running, returns an empty slice.
func (c *Client) List(ctx context.Context) ([]session.Session, error) {
	stdout, stderr, code, err := c.Exec(ctx, "list-sessions", "-F", SessionFormat)
	if err != nil {
		return nil, fmt.Errorf("exec list-sessions: %w", err)
	}
	if code != 0 {
		if IsNoServer(stderr) {
			return []session.Session{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %s", strings.TrimSpace(stderr))
	}
	return ParseSessions(stdout), nil
}

// GetSession returns a single session by name or session.ErrNotFound.
func (c *Client) GetSession(ctx context.Context, name string) (*session.Session, error) {
	sessions, err := c.List(ctx)
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

// Get satisfies session.Backend fast getter interface.
func (c *Client) Get(ctx context.Context, name string) (*session.Session, error) {
	return c.GetSession(ctx, name)
}

func (c *Client) generateUniqueName(ctx context.Context) (string, error) {
	base := time.Now().Format("session-20060102-150405")
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if !c.HasSession(ctx, name) {
			return name, nil
		}
	}
	return "", session.ErrNameInUse
}

func homeDirectory() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// Create starts a new detached tmux session with global options applied.
func (c *Client) Create(ctx context.Context, name string) (*session.Session, error) {
	if name == "" {
		generated, err := c.generateUniqueName(ctx)
		if err != nil {
			return nil, fmt.Errorf("generate unique name: %w", err)
		}
		name = generated
	}

	if c.HasSession(ctx, name) {
		return nil, session.ErrNameInUse
	}

	if err := c.EnsureGlobalOptions(ctx); err != nil {
		return nil, fmt.Errorf("ensure global options: %w", err)
	}

	_, stderr, code, err := c.Exec(ctx, "new-session", "-d", "-s", name, "-c", homeDirectory())
	if err != nil {
		return nil, fmt.Errorf("exec new-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return nil, session.ErrNameInUse
		}
		return nil, fmt.Errorf("tmux new-session: %s", strings.TrimSpace(stderr))
	}
	return c.GetSession(ctx, name)
}

// Rename renames an existing tmux session.
func (c *Client) Rename(ctx context.Context, oldName, newName string) error {
	if !c.HasSession(ctx, oldName) {
		return session.ErrNotFound
	}
	if c.HasSession(ctx, newName) {
		return session.ErrNameInUse
	}

	_, stderr, code, err := c.Exec(ctx, "rename-session", "-t", ExactTarget(oldName), "--", newName)
	if err != nil {
		return fmt.Errorf("exec rename-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return session.ErrNameInUse
		}
		if strings.Contains(stderr, "can't find session") {
			return session.ErrNotFound
		}
		return fmt.Errorf("tmux rename-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// Kill terminates an existing tmux session.
func (c *Client) Kill(ctx context.Context, name string) error {
	if !c.HasSession(ctx, name) {
		return session.ErrNotFound
	}

	_, stderr, code, err := c.Exec(ctx, "kill-session", "-t", ExactTarget(name))
	if err != nil {
		return fmt.Errorf("exec kill-session: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "can't find session") {
			return session.ErrNotFound
		}
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// AttachCommand builds the exec.Cmd for a pty-backed `tmux attach-session`,
// scrubbing hub secrets from the child environment and pinning TERM.
func (c *Client) AttachCommand(name string) *exec.Cmd {
	args := []string{}
	if c.socket != "" {
		args = append(args, "-L", c.socket)
	}
	args = append(args, "attach-session", "-t", ExactTarget(name))
	cmd := exec.Command(c.path, args...)
	cmd.Env = ChildEnv()
	return cmd
}
