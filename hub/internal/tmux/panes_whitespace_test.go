package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shQuote wraps a value so a shell echoes it byte for byte.
func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Every character a directory name may contain is part of the path, and this
// value is used as one. Trimming whitespace off it does not discard formatting:
// it names a different directory, or -- where the name is whitespace throughout
// -- reports a session with no directory at all.
func TestPaneWorkingDirectoryKeepsWhitespaceInTheName(t *testing.T) {
	for _, want := range []string{
		"/srv/my project ", // a trailing space
		"/srv/ leading",    // a leading space
		"/srv/tab\t",       // a trailing tab
		"/srv/double  gap", // whitespace inside the name
		"/srv/line\nbreak", // a name spanning two lines
		"/srv/all space  ", // more than one kind at once
	} {
		c := fakeTmux(t, fmt.Sprintf("printf '%%s\\n' %s\n", shQuote(want)))

		dir, err := c.PaneWorkingDirectory(context.Background(), "work")
		if err != nil {
			t.Fatalf("%q: PaneWorkingDirectory failed: %v", want, err)
		}
		if dir != want {
			t.Errorf("expected %q back unchanged, got %q", want, dir)
		}
	}
}

// A name that is only whitespace is still a name, and is not the same thing as
// an unknown format expanding to nothing.
func TestPaneWorkingDirectoryKeepsANameThatIsOnlyWhitespace(t *testing.T) {
	c := fakeTmux(t, "printf '  \\n'\n")

	dir, err := c.PaneWorkingDirectory(context.Background(), "work")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory failed: %v", err)
	}
	if dir != "  " {
		t.Errorf("expected the two spaces back, got %q", dir)
	}
}

// The fake pins the parser; only a real server pins what the parser is fed. The
// whole fix rests on tmux writing the directory and exactly one newline and
// nothing else -- so that is the assumption worth testing against tmux itself.
func TestPaneWorkingDirectoryKeepsATrailingSpaceAgainstARealServer(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "trailing space ")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The kernel reports the resolved path for a pane's directory, and on macOS
	// a temporary directory is reached through one or more links.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, stderr, code, err := c.Exec(ctx, "new-session", "-d", "-s", "spaced", "-c", dir); err != nil || code != 0 {
		t.Fatalf("new-session failed: %v (%s)", err, stderr)
	}

	got, err := c.PaneWorkingDirectory(ctx, "spaced")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory: %v", err)
	}
	if got != resolved {
		t.Errorf("expected %q, got %q", resolved, got)
	}
}
