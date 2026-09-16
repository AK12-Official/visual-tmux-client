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
	want := []string{"display-message", "-p", "-t", "=work", "#{pane_current_path}"}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("expected the arguments %v, got %v", want, got)
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
