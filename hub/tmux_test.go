package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tmuxAvailable reports whether a real tmux binary is on PATH.
func tmuxAvailable(t *testing.T) bool {
	t.Helper()
	p, err := exec.LookPath("tmux")
	return err == nil && p != ""
}

// uniqueSocket returns a dedicated tmux socket name for one test.
func uniqueSocket(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("hubtest-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// newTestClient starts an isolated tmux server on a dedicated socket and
// returns a client bound to it. Cleanup kills the server.
func newTestClient(t *testing.T) *tmuxClient {
	t.Helper()
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	sock := uniqueSocket(t)
	tmux, _ := exec.LookPath("tmux")
	// start-server gives us a server with no sessions, which the no-sessions
	// tests rely on.
	if _, _, code, err := runCommand(tmux, "-L", sock, "start-server"); err != nil || code != 0 {
		t.Fatalf("tmux start-server failed: code=%d err=%v", code, err)
	}
	c := &tmuxClient{path: tmux, socket: sock}
	t.Cleanup(func() {
		runCommand(tmux, "-L", sock, "kill-server")
	})
	return c
}

// mkSession creates a detached session on the client's socket.
func mkSession(t *testing.T, c *tmuxClient, name string) {
	t.Helper()
	_, stderr, code, err := c.exec("new-session", "-d", "-s", name, "-c", "/tmp")
	if err != nil || code != 0 {
		t.Fatalf("new-session %q failed: code=%d stderr=%q err=%v", name, code, stderr, err)
	}
}

func TestResolveTmuxNotFound(t *testing.T) {
	t.Setenv("TMUX_HUB_TMUX_PATH", "")
	t.Setenv("PATH", "")
	_, err := resolveTmux()
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Fatalf("expected ErrTmuxNotFound, got %v", err)
	}
}

func TestResolveTmuxHonorsEnvPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_HUB_TMUX_PATH", fake)
	got, err := resolveTmux()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fake {
		t.Fatalf("expected %q, got %q", fake, got)
	}
}

func TestValidateSessionName(t *testing.T) {
	reject := []string{
		"",
		strings.Repeat("a", 65),
		";", "$", "`", "'", "\"", " ", ":",
		"a b", "a;b", "a$b", "a`b", "a:b", "a'b", "a\"b",
	}
	for _, name := range reject {
		if err := validateSessionName(name); err == nil {
			t.Errorf("expected rejection of %q", name)
		}
	}
	accept := []string{"api", "api-staging", "web.2", "a_b", "a", "A.1-2_b"}
	for _, name := range accept {
		if err := validateSessionName(name); err != nil {
			t.Errorf("expected %q to be accepted, got %v", name, err)
		}
	}
}

// TestRunCommandPassesDiscreteArgs proves argv is passed without any shell
// interpretation: a fake tmux echoes each argument on its own line, and a name
// full of shell metacharacters survives intact.
func TestRunCommandPassesDiscreteArgs(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	nasty := "=a;b$c`d'e\"f g:h"
	stdout, _, code, err := runCommand(fake, "list-sessions", "-t", nasty)
	if err != nil || code != 0 {
		t.Fatalf("runCommand failed: code=%d err=%v", code, err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 3 || lines[0] != "list-sessions" || lines[1] != "-t" || lines[2] != nasty {
		t.Fatalf("args not passed discretely: got %q", lines)
	}
}

func TestExactTarget(t *testing.T) {
	if got := exactTarget("probe"); got != "=probe" {
		t.Fatalf("expected =probe, got %q", got)
	}
}

func TestExactTargetingKillDoesNotTouchPrefix(t *testing.T) {
	c := newTestClient(t)
	mkSession(t, c, "probe")
	mkSession(t, c, "probe-staging")

	if err := c.KillSession("probe"); err != nil {
		t.Fatalf("KillSession(probe): %v", err)
	}
	if c.hasSession("probe") {
		t.Errorf("probe should be gone")
	}
	if !c.hasSession("probe-staging") {
		t.Errorf("probe-staging should survive")
	}
}

func TestListSessionsWithSessions(t *testing.T) {
	c := newTestClient(t)
	mkSession(t, c, "alpha")
	// give alpha a second window
	_, _, _, _ = c.exec("new-window", "-t", exactTarget("alpha"))
	mkSession(t, c, "beta")

	sessions, err := c.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(sessions), sessions)
	}
	byName := map[string]Session{}
	for _, s := range sessions {
		byName[s.Name] = s
	}
	if a := byName["alpha"]; a.Windows != 2 {
		t.Errorf("alpha should have 2 windows, got %d", a.Windows)
	}
	if b := byName["beta"]; b.Windows != 1 {
		t.Errorf("beta should have 1 window, got %d", b.Windows)
	}
}

func TestListSessionsEmptyServer(t *testing.T) {
	c := newTestClient(t)
	sessions, err := c.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions on empty server should be nil error, got %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty slice, got %+v", sessions)
	}
	if sessions == nil {
		t.Fatalf("expected non-nil empty slice")
	}
}

func TestListSessionsNoServerRunning(t *testing.T) {
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	sock := uniqueSocket(t)
	tmux, _ := exec.LookPath("tmux")
	c := &tmuxClient{path: tmux, socket: sock}
	// no start-server: the socket has no server behind it
	sessions, err := c.ListSessions()
	if err != nil {
		t.Fatalf("expected no-server to map to empty/nil, got %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty slice, got %+v", sessions)
	}
}

func TestCreateSessionExplicitName(t *testing.T) {
	c := newTestClient(t)
	sess, err := c.CreateSession("api")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Name != "api" {
		t.Fatalf("expected name api, got %q", sess.Name)
	}
	if !c.hasSession("api") {
		t.Fatalf("api should exist")
	}
}

func TestCreateSessionGeneratedName(t *testing.T) {
	c := newTestClient(t)
	sess, err := c.CreateSession("")
	if err != nil {
		t.Fatalf("CreateSession(\"\"): %v", err)
	}
	if !strings.HasPrefix(sess.Name, "session-") {
		t.Fatalf("expected generated name with session- prefix, got %q", sess.Name)
	}
}

func TestCreateSessionDuplicate(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.CreateSession("dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := c.CreateSession("dup"); !errors.Is(err, ErrNameInUse) {
		t.Fatalf("expected ErrNameInUse, got %v", err)
	}
}

func TestCreateSessionInvalidName(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.CreateSession("bad;name"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestRenameSession(t *testing.T) {
	c := newTestClient(t)
	mkSession(t, c, "old")
	if err := c.RenameSession("old", "new"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if !c.hasSession("new") {
		t.Fatalf("new should exist")
	}
	if c.hasSession("old") {
		t.Fatalf("old should be gone")
	}
	if err := c.RenameSession("absent", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRenameSessionConflict(t *testing.T) {
	c := newTestClient(t)
	mkSession(t, c, "a")
	mkSession(t, c, "b")
	if err := c.RenameSession("a", "b"); !errors.Is(err, ErrNameInUse) {
		t.Fatalf("expected ErrNameInUse, got %v", err)
	}
}

func TestKillSession(t *testing.T) {
	c := newTestClient(t)
	mkSession(t, c, "victim")
	if err := c.KillSession("victim"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if c.hasSession("victim") {
		t.Fatalf("victim should be gone")
	}
	if err := c.KillSession("victim"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
