package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

func tmuxAvailable(t *testing.T) bool {
	t.Helper()
	p, err := exec.LookPath("tmux")
	return err == nil && p != ""
}

func uniqueSocket(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tmuxtest-%d-%d", os.Getpid(), time.Now().UnixNano())
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	sock := uniqueSocket(t)
	c := NewClient("", sock)
	if c.err != nil {
		t.Fatalf("NewClient failed: %v", c.err)
	}

	ctx := context.Background()
	if _, _, code, err := c.Exec(ctx, "start-server"); err != nil || code != 0 {
		t.Fatalf("start-server failed: code=%d err=%v", code, err)
	}

	t.Cleanup(func() {
		if _, _, _, err := c.Exec(context.Background(), "kill-server"); err != nil {
			t.Logf("kill-server cleanup: %v", err)
		}
	})
	return c
}

func TestUnicodeAndExactTargeting(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// 1. Create unicode session
	name1 := "会话-测试-🚀"
	sess, err := c.Create(ctx, name1)
	if err != nil {
		t.Fatalf("Create unicode session failed: %v", err)
	}
	if sess.Name != name1 {
		t.Errorf("expected session name %q, got %q", name1, sess.Name)
	}

	// 2. Exact targeting: "api" vs "api-staging"
	_, err = c.Create(ctx, "api-staging")
	if err != nil {
		t.Fatalf("Create api-staging failed: %v", err)
	}
	if c.HasSession(ctx, "api") {
		t.Errorf("api should not exist when only api-staging was created")
	}

	_, err = c.Create(ctx, "api")
	if err != nil {
		t.Fatalf("Create api failed: %v", err)
	}

	// Rename "api" to "api-v2"
	err = c.Rename(ctx, "api", "api-v2")
	if err != nil {
		t.Fatalf("Rename api failed: %v", err)
	}
	if !c.HasSession(ctx, "api-staging") {
		t.Errorf("api-staging should still exist")
	}
	if !c.HasSession(ctx, "api-v2") {
		t.Errorf("api-v2 should exist")
	}

	// Kill "api-v2"
	err = c.Kill(ctx, "api-v2")
	if err != nil {
		t.Fatalf("Kill api-v2 failed: %v", err)
	}
	if !c.HasSession(ctx, "api-staging") {
		t.Errorf("api-staging must not be affected by killing api-v2")
	}
}

func TestCreateNamelessSessionAndAutoCollision(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	sess1, err := c.Create(ctx, "")
	if err != nil {
		t.Fatalf("Create first nameless session failed: %v", err)
	}
	if !strings.HasPrefix(sess1.Name, "session-") {
		t.Errorf("expected generated name with prefix session-, got: %q", sess1.Name)
	}
	if !c.HasSession(ctx, sess1.Name) {
		t.Errorf("expected HasSession true for sess1 %q", sess1.Name)
	}

	sess2, err := c.Create(ctx, "")
	if err != nil {
		t.Fatalf("Create second nameless session failed: %v", err)
	}
	if sess2.Name == sess1.Name {
		t.Errorf("expected distinct generated session names, got same: %q", sess1.Name)
	}
	if !c.HasSession(ctx, sess2.Name) {
		t.Errorf("expected HasSession true for sess2 %q", sess2.Name)
	}
}

func TestConflictAndNotFound(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	_, err := c.Create(ctx, "session-a")
	if err != nil {
		t.Fatalf("Create session-a failed: %v", err)
	}

	// Conflict on create
	_, err = c.Create(ctx, "session-a")
	if !errors.Is(err, session.ErrNameInUse) {
		t.Errorf("expected ErrNameInUse on duplicate create, got: %v", err)
	}

	// Create second session
	_, err = c.Create(ctx, "session-b")
	if err != nil {
		t.Fatalf("Create session-b failed: %v", err)
	}

	// Conflict on rename
	err = c.Rename(ctx, "session-a", "session-b")
	if !errors.Is(err, session.ErrNameInUse) {
		t.Errorf("expected ErrNameInUse on rename to existing name, got: %v", err)
	}

	// Rename to same name is no-op
	if err := c.Rename(ctx, "session-a", "session-a"); err != nil {
		t.Errorf("expected nil error on renaming session to itself, got: %v", err)
	}

	// Not found on rename
	err = c.Rename(ctx, "session-nonexistent", "session-c")
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound on renaming non-existent, got: %v", err)
	}

	// Not found on kill
	err = c.Kill(ctx, "session-nonexistent")
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound on killing non-existent, got: %v", err)
	}
}

func TestGlobalOptionsNoRedundantRepaint(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create a session so session options are instantiated
	if _, err := c.Create(ctx, "opt-test"); err != nil {
		t.Fatalf("Create session failed: %v", err)
	}

	// Initial ensure sets mouse on and window-size latest
	if err := c.EnsureGlobalOptions(ctx); err != nil {
		t.Fatalf("EnsureGlobalOptions failed: %v", err)
	}

	mouseOut, _, _, err := c.Exec(ctx, "show-options", "-gv", "mouse")
	if err != nil {
		t.Fatalf("show-options mouse failed: %v", err)
	}
	if strings.TrimSpace(mouseOut) != "on" {
		t.Errorf("expected mouse on, got %q", mouseOut)
	}

	winOut, _, _, err := c.Exec(ctx, "show-options", "-gv", "window-size")
	if err != nil {
		t.Fatalf("show-options window-size failed: %v", err)
	}
	if strings.TrimSpace(winOut) != "latest" {
		t.Errorf("expected window-size latest, got %q", winOut)
	}

	// Calling again when already set: verify options remain unchanged
	if err := c.EnsureGlobalOptions(ctx); err != nil {
		t.Fatalf("EnsureGlobalOptions second call failed: %v", err)
	}
}

func TestEnvironmentScrubbing(t *testing.T) {
	rawEnv := []string{
		"PATH=/usr/bin",
		"VISUAL_TMUX_CLIENT_TOKEN=supersecret",
		"TMUX=/tmp/tmux-1000/default,1234,0",
		"TMUX_PANE=%1",
		"USER=alice",
	}

	filtered := FilterEnv(rawEnv, "TMUX", "TMUX_PANE", "VISUAL_TMUX_CLIENT_TOKEN")
	for _, kv := range filtered {
		if strings.HasPrefix(kv, "VISUAL_TMUX_CLIENT_TOKEN=") {
			t.Errorf("secret token not scrubbed from env")
		}
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			t.Errorf("tmux nesting marker not scrubbed from env")
		}
	}

	child := ChildEnv()
	hasTerm := false
	for _, kv := range child {
		if kv == "TERM=xterm-256color" {
			hasTerm = true
		}
		if strings.HasPrefix(kv, "VISUAL_TMUX_CLIENT_TOKEN=") {
			t.Errorf("child env has secret token")
		}
	}
	if !hasTerm {
		t.Errorf("child env missing TERM=xterm-256color")
	}
}

func TestExactTarget(t *testing.T) {
	if ExactTarget("session1") != "=session1" {
		t.Errorf("expected =session1, got %s", ExactTarget("session1"))
	}
	if ExactTarget("") != "=" {
		t.Errorf("expected =, got %s", ExactTarget(""))
	}
}

func TestIsNoServer(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{"no server running on /tmp/tmux-1000/default", true},
		{"error connecting to /tmp/tmux-1000/default", true},
		{"can't find session: foo", false},
		{"duplicate session: bar", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsNoServer(tt.stderr)
		if got != tt.want {
			t.Errorf("IsNoServer(%q) = %v; want %v", tt.stderr, got, tt.want)
		}
	}
}

func TestParseSessions(t *testing.T) {
	input := strings.Join([]string{
		"sess1|1|0|1700000000",
		"sess|with|pipes|2|1|1700000001",
		"malformed_line_not_enough_fields",
		"corrupt|abc|def|xyz",
		"",
	}, "\n")

	sessions := ParseSessions(input)
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	if sessions[0].Name != "sess1" || sessions[0].Windows != 1 ||
		sessions[0].Attached != 0 || sessions[0].Created != 1700000000 {
		t.Errorf("unexpected session 0: %+v", sessions[0])
	}

	if sessions[1].Name != "sess|with|pipes" || sessions[1].Windows != 2 ||
		sessions[1].Attached != 1 || sessions[1].Created != 1700000001 {
		t.Errorf("unexpected session 1: %+v", sessions[1])
	}

	if sessions[2].Name != "corrupt" || sessions[2].Windows != 0 ||
		sessions[2].Attached != 0 || sessions[2].Created != 0 {
		t.Errorf("unexpected session 2: %+v", sessions[2])
	}
}

func TestNewClient_NotFoundPath(t *testing.T) {
	c := NewClient("/nonexistent/binary/path/tmux", "sock")
	if c.err == nil {
		t.Fatal("expected error for nonexistent binary path")
	}
	if !errors.Is(c.err, session.ErrTmuxNotFound) {
		t.Errorf("expected ErrTmuxNotFound, got %v", c.err)
	}

	ctx := context.Background()
	if _, _, _, err := c.Exec(ctx, "ls"); !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("expected Exec to return ErrTmuxNotFound, got %v", err)
	}
	if err := c.EnsureGlobalOptions(ctx); !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("expected EnsureGlobalOptions to return ErrTmuxNotFound, got %v", err)
	}
	if _, err := c.Create(ctx, "test"); !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("expected Create to return ErrTmuxNotFound, got %v", err)
	}
	if err := c.Rename(ctx, "old", "new"); !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("expected Rename to return ErrTmuxNotFound, got %v", err)
	}
	if err := c.Kill(ctx, "test"); !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("expected Kill to return ErrTmuxNotFound, got %v", err)
	}
}
