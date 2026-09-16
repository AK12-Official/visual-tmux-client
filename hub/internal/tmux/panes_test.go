package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// fakeTmux points a Client at a script instead of a real tmux, so the command a
// method builds can be checked without starting a server.
func fakeTmux(t *testing.T, body string) *Client {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewClient(bin, "")
}

// The target has to be a pane target: a bare `=work` is only a session target,
// which tmux accepts while resolving nothing, printing an empty line and exiting
// zero. That reads as "no directory" instead of as the targeting mistake it is.
func TestPaneWorkingDirectoryTargetsTheExactSession(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "args")
	c := fakeTmux(t, fmt.Sprintf(
		"printf '%%s\\n' \"$@\" > %s\nprintf '%%s\\n' '/srv/project'\n", recorded))

	dir, err := c.PaneWorkingDirectory(context.Background(), "work")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory failed: %v", err)
	}
	if dir != "/srv/project" {
		t.Errorf("expected the pane directory, got %q", dir)
	}

	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"display-message", "-p", "-t", "=work:", "#{pane_current_path}"}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("expected the arguments %v, got %v", want, got)
	}
}

// The fake above pins the argument vector; this one pins that the command
// actually works against a real server, which is the part a wrong target or a
// wrong format would break.
func TestPaneWorkingDirectoryAgainstARealServer(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Create(ctx, "work"); err != nil {
		t.Fatalf("create a session to ask about: %v", err)
	}

	dir, err := c.PaneWorkingDirectory(ctx, "work")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("expected an absolute directory, got %q", dir)
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		t.Errorf("expected %q to be a directory that exists: %v", dir, statErr)
	}
}

func TestPaneWorkingDirectoryReportsAnAbsentSession(t *testing.T) {
	c := fakeTmux(t, "echo \"can't find session: absent\" >&2\nexit 1\n")

	if _, err := c.PaneWorkingDirectory(context.Background(), "absent"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected not-found, got: %v", err)
	}
}

// An unknown format expands to nothing rather than failing, and an empty string
// is not a directory the file manager could open at.
func TestPaneWorkingDirectoryReportsAnEmptyAnswer(t *testing.T) {
	c := fakeTmux(t, "printf '\\n'\n")

	if _, err := c.PaneWorkingDirectory(context.Background(), "work"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected not-found, got: %v", err)
	}
}
