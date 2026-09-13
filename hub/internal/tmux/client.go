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

// These defaults apply to the selected tmux server. Explicit session/window
// overrides still take precedence over the global session/window defaults.
var globalOptions = []struct {
	scope string
	name  string
	value string
}{
	{"-g", "window-size", "latest"},
	{"-g", "mouse", "on"},
	{"-s", "exit-unattached", "off"},
	{"-g", "destroy-unattached", "off"},
	{"-gw", "remain-on-exit", "failed"},
}

// EnsureGlobalOptions applies display and session preservation defaults.
// Inspects each option before setting to avoid redundant client repaints.
func (c *Client) EnsureGlobalOptions(ctx context.Context) error {
	if c.err != nil {
		return c.err
	}
	c.optionsMu.Lock()
	defer c.optionsMu.Unlock()

	for _, option := range globalOptions {
		out, stderr, code, err := c.Exec(ctx, "show-options", option.scope+"v", option.name)
		if err != nil {
			return fmt.Errorf("show-option %s: %w", option.name, err)
		}
		if code != 0 {
			return fmt.Errorf("show-option %s: %s", option.name, strings.TrimSpace(stderr))
		}
		if strings.TrimSpace(out) == option.value {
			continue
		}
		_, stderr, code, err = c.Exec(ctx, "set-option", option.scope, option.name, option.value)
		if err != nil {
			return fmt.Errorf("set-option %s: %w", option.name, err)
		}
		if code != 0 {
			return fmt.Errorf("set-option %s: %s", option.name, strings.TrimSpace(stderr))
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
	if c.err != nil {
		return nil, c.err
	}
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

	// One command queue keeps a newly started server alive and installs defaults
	// before its first pane can exit. A failed set-option aborts the queue.
	args := []string{"start-server"}
	for _, option := range globalOptions {
		args = append(args, ";", "set-option", option.scope, option.name, option.value)
	}
	args = append(args, ";", "new-session", "-d", "-s", name, "-c", homeDirectory())
	c.optionsMu.Lock()
	_, stderr, code, err := c.Exec(ctx, args...)
	c.optionsMu.Unlock()
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
	if c.err != nil {
		return c.err
	}
	if !c.HasSession(ctx, oldName) {
		return session.ErrNotFound
	}
	if oldName == newName {
		return nil
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
	if c.err != nil {
		return c.err
	}
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
