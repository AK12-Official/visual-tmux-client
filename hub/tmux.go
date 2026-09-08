package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Sentinel errors distinguish tmux-level outcomes so callers (and the JSON
// API) can map them to the right HTTP status codes without string-matching.
var (
	ErrTmuxNotFound = errors.New("tmux: executable not found")
	ErrNameInUse    = errors.New("session name already in use")
	ErrNotFound     = errors.New("session not found")
	ErrInvalidName  = errors.New("invalid session name")
)

// maxSessionNameRunes bounds session-name length counted in code points, not
// bytes: a CJK name spends 3 bytes per character, so a byte bound would admit
// only a third as many characters.
const maxSessionNameRunes = 64

// Session is a single tmux session as exposed to the JSON API.
type Session struct {
	Name     string `json:"name"`
	Windows  int    `json:"windows"`
	Attached int    `json:"attached"`
	Created  int64  `json:"created"`
}

// sessionFormat is the -F format string for list-sessions: one pipe-delimited
// record per line. Verified against tmux 3.7b (see design.md).
const sessionFormat = "#{session_name}|#{session_windows}|#{session_attached}|#{session_created}"

// tmuxClient executes tmux commands against a socket. An empty socket means
// tmux's default socket, which is what the hub uses in production; tests pass
// a dedicated `-L <socket>` name so they never disturb a real default-socket
// server.
type tmuxClient struct {
	path   string // resolved tmux binary path
	socket string // `-L <socket>`; empty = default socket
	err    error  // resolution error (e.g. ErrTmuxNotFound), if tmux is missing
}

// newTmuxClient resolves the tmux binary and returns a client. A failure to
// resolve tmux is recorded on the client rather than returned, so the hub can
// still start without tmux on PATH and report ErrTmuxNotFound per operation.
func newTmuxClient(socket string) *tmuxClient {
	path, err := resolveTmux()
	return &tmuxClient{path: path, socket: socket, err: err}
}

// resolveTmux returns the tmux binary path, honoring VISUAL_TMUX_CLIENT_TMUX_PATH and
// falling back to $PATH lookup. It returns ErrTmuxNotFound when no binary is
// available so callers can distinguish "tmux missing" from "no sessions".
func resolveTmux() (string, error) {
	if p := os.Getenv("VISUAL_TMUX_CLIENT_TMUX_PATH"); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%w: %v", ErrTmuxNotFound, err)
		}
		return p, nil
	}
	p, err := exec.LookPath("tmux")
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTmuxNotFound, err)
	}
	return p, nil
}

// validateSessionName enforces the permitted session-name rules. tmux itself
// accepts nearly anything (verified against 3.7b: CJK and spaces are fine), so
// the constraints are only the ones that keep a name unambiguous everywhere it
// travels: `:` and `.` are reserved by tmux's own target syntax
// (session:window.pane) and rejected by older tmux versions; control
// characters and edge whitespace make a name invisible or untypeable; length
// keeps names displayable. Everything else — including non-ASCII text — is
// allowed, because names never travel through a shell (runCommand passes an
// argv slice, see the shell-injection-safety requirement).
func validateSessionName(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidName)
	}
	runes := []rune(name)
	if len(runes) == 0 {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if len(runes) > maxSessionNameRunes {
		return fmt.Errorf("%w: name is longer than %d characters", ErrInvalidName, maxSessionNameRunes)
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: name contains a control character", ErrInvalidName)
		}
		if r == ':' || r == '.' {
			return fmt.Errorf("%w: name must not contain %q (reserved by tmux target syntax)", ErrInvalidName, r)
		}
		// Full-width lookalikes pass tmux untouched, but they render exactly
		// like the reserved characters and are one keystroke away in CJK
		// input methods — reject them so a forbidden-looking name can never
		// exist.
		if r == '：' || r == '．' {
			return fmt.Errorf("%w: name must not contain the full-width character %q (use the ASCII form)", ErrInvalidName, r)
		}
	}
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return fmt.Errorf("%w: name has leading or trailing whitespace", ErrInvalidName)
	}
	return nil
}

// runCommand executes path with the given argv and returns stdout, stderr and
// exit code separately. A non-zero exit is NOT an error here: the exit code is
// returned so callers can inspect tmux's own error messages. The returned
// error is reserved for "could not start the process" failures.
//
// Every tmux subprocess the hub spawns runs with a scrubbed environment so the
// hub's secrets (VISUAL_TMUX_CLIENT_TOKEN) never leak into a session, and so a hub that
// happens to run inside a tmux session does not nest (TMUX/TMUX_PANE removed).
func runCommand(path string, args ...string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.Command(path, args...)
	cmd.Env = scrubbedEnv()
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), errBuf.String(), ee.ExitCode(), nil
		}
		return out.String(), errBuf.String(), 0, err
	}
	return out.String(), errBuf.String(), 0, nil
}

// execTmux runs tmux (default socket) with an argv slice. It exists as a
// package-level function per the task spec; the tmuxClient methods below use
// runCommand directly so they can inject a test socket.
func execTmux(args ...string) (stdout, stderr string, exitCode int, err error) {
	path, err := resolveTmux()
	if err != nil {
		return "", "", 0, err
	}
	return runCommand(path, args...)
}

// exec runs a tmux command against this client's socket.
func (c *tmuxClient) exec(args ...string) (stdout, stderr string, exitCode int, err error) {
	if c.err != nil {
		return "", "", 0, c.err
	}
	full := args
	if c.socket != "" {
		full = append([]string{"-L", c.socket}, args...)
	}
	return runCommand(c.path, full...)
}

// exactTarget prefixes a session name with "=" so tmux matches it exactly,
// never as a prefix/pattern. A caller naming `api` must not touch `api-staging`.
func exactTarget(name string) string {
	return "=" + name
}

// isNoServer reports whether tmux's stderr means "no tmux server is running".
// tmux emits "no server running on <path>" for a known-but-absent server, and
// "error connecting to <path>" when the socket was never created; both mean the
// server is unreachable and should map to an empty session list.
func isNoServer(stderr string) bool {
	return strings.Contains(stderr, "no server running") || strings.Contains(stderr, "error connecting to")
}

// hasSession reports whether a session with the given name exists.
func (c *tmuxClient) hasSession(name string) bool {
	_, _, code, err := c.exec("has-session", "-t", exactTarget(name))
	return err == nil && code == 0
}

// getSession returns a single session's details, or ErrNotFound.
func (c *tmuxClient) getSession(name string) (Session, error) {
	sessions, err := c.ListSessions()
	if err != nil {
		return Session{}, err
	}
	for _, s := range sessions {
		if s.Name == name {
			return s, nil
		}
	}
	return Session{}, ErrNotFound
}

// parseSessions parses list-sessions -F output into a Session slice.
func parseSessions(stdout string) []Session {
	out := make([]Session, 0)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		windows, _ := strconv.Atoi(parts[1])
		attached, _ := strconv.Atoi(parts[2])
		created, _ := strconv.ParseInt(parts[3], 10, 64)
		out = append(out, Session{
			Name:     parts[0],
			Windows:  windows,
			Attached: attached,
			Created:  created,
		})
	}
	return out
}

// ListSessions returns the sessions present on the server. "No tmux server is
// running" is mapped to an empty slice + nil error (per spec), while a missing
// tmux binary still propagates as ErrTmuxNotFound.
func (c *tmuxClient) ListSessions() ([]Session, error) {
	stdout, stderr, code, err := c.exec("list-sessions", "-F", sessionFormat)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		if isNoServer(stderr) {
			return []Session{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %s", strings.TrimSpace(stderr))
	}
	return parseSessions(stdout), nil
}

// homeDir returns the invoking user's home directory, used as the working
// directory for new sessions.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "."
}

// generateSessionName produces the base auto-name of the form
// session-YYYYMMDD-HHMMSS. The base has second resolution, so two nameless
// creates within one second collide; uniqueness is resolved by
// generateUniqueName's suffix retries.
func generateSessionName(now time.Time) string {
	return now.Format("session-20060102-150405")
}

// generateUniqueName returns an auto-generated name not already in use,
// retrying the timestamp base with -1…-9 suffixes. The bound makes a runaway
// loop impossible; exhausting it surfaces ErrNameInUse.
func (c *tmuxClient) generateUniqueName() (string, error) {
	base := generateSessionName(time.Now())
	for i := 0; i < 10; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if !c.hasSession(name) {
			return name, nil
		}
	}
	return "", ErrNameInUse
}

// CreateSession creates a new detached session starting in $HOME. An empty
// name is replaced with a generated one. Returns ErrNameInUse on conflict and
// ErrInvalidName on a rejected name.
func (c *tmuxClient) CreateSession(name string) (Session, error) {
	if name == "" {
		var err error
		name, err = c.generateUniqueName()
		if err != nil {
			return Session{}, err
		}
	}
	if err := validateSessionName(name); err != nil {
		return Session{}, err
	}
	if c.hasSession(name) {
		return Session{}, ErrNameInUse
	}
	_, stderr, code, err := c.exec("new-session", "-d", "-s", name, "-c", homeDir())
	if err != nil {
		return Session{}, err
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return Session{}, ErrNameInUse
		}
		return Session{}, fmt.Errorf("tmux new-session: %s", strings.TrimSpace(stderr))
	}
	return c.getSession(name)
}

// RenameSession renames an existing session. Returns ErrNotFound for an absent
// target and ErrNameInUse for a rename onto an existing name.
func (c *tmuxClient) RenameSession(oldName, newName string) error {
	if err := validateSessionName(newName); err != nil {
		return err
	}
	if !c.hasSession(oldName) {
		return ErrNotFound
	}
	if c.hasSession(newName) {
		return ErrNameInUse
	}
	_, stderr, code, err := c.exec("rename-session", "-t", exactTarget(oldName), newName)
	if err != nil {
		return err
	}
	if code != 0 {
		if strings.Contains(stderr, "duplicate session") {
			return ErrNameInUse
		}
		return fmt.Errorf("tmux rename-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// KillSession terminates an existing session. Returns ErrNotFound for an
// absent target.
func (c *tmuxClient) KillSession(name string) error {
	if !c.hasSession(name) {
		return ErrNotFound
	}
	_, stderr, code, err := c.exec("kill-session", "-t", exactTarget(name))
	if err != nil {
		return err
	}
	if code != 0 {
		if strings.Contains(stderr, "can't find session") {
			return ErrNotFound
		}
		return fmt.Errorf("tmux kill-session: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// attachCommand builds the exec.Cmd for a pty-backed `tmux attach-session`,
// scrubbing hub secrets from the child environment and pinning TERM.
func (c *tmuxClient) attachCommand(name string) *exec.Cmd {
	args := []string{}
	if c.socket != "" {
		args = append(args, "-L", c.socket)
	}
	args = append(args, "attach-session", "-t", exactTarget(name))
	cmd := exec.Command(c.path, args...)
	cmd.Env = childEnv()
	return cmd
}

// scrubbedEnv returns the hub's environment with its own secrets and any
// tmux nesting markers removed.
func scrubbedEnv() []string {
	return filterEnv(os.Environ(), "TMUX", "TMUX_PANE", "VISUAL_TMUX_CLIENT_TOKEN")
}

// filterEnv returns env with entries whose key matches any of the given names
// removed.
func filterEnv(env []string, drop ...string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropSet[d] = struct{}{}
	}
	var out []string
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := dropSet[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// childEnv returns an environment for attached session processes with the
// hub's secrets removed and TERM pinned to xterm-256color.
func childEnv() []string {
	return append(scrubbedEnv(), "TERM=xterm-256color")
}

// refreshSessionSize is a best-effort "belt-and-braces" resize: after
// pty.Setsize (the authoritative mechanism, which fires SIGWINCH at tmux), it
// also asks tmux to refresh the size of THIS attachment's tmux client. The
// client is matched by pid because a session can carry several clients (other
// browser tabs, other hubs) and resizing those would fight their own
// authoritative resize path. Failure is silently tolerated; Setsize already
// handled the resize.
func (c *tmuxClient) refreshSessionSize(session string, clientPID int, cols, rows int) {
	stdout, _, code, err := c.exec("list-clients", "-t", exactTarget(session), "-F", "#{client_pid} #{client_tty}")
	if err != nil || code != 0 {
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid != clientPID {
			continue
		}
		_, _, _, _ = c.exec("refresh-client", "-t", fields[1], "-C", fmt.Sprintf("%dx%d", cols, rows))
		return
	}
}
