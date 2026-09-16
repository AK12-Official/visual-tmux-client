package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// paneFieldSep stays printable because some tmux versions escape control bytes
// in command output (a Unit Separator becomes the literal text \037). A full
// delimiter inside a field makes the record ambiguous and is rejected below;
// ordinary pipes and backslash sequences remain untouched.
const paneFieldSep = "|vtc-pane|"

// paneFieldCount is the number of fields PaneFormat emits.
const paneFieldCount = 9

// tmuxTrue is how tmux renders a set boolean in a format string.
const tmuxTrue = "1"

// PaneFormat is the -F format string for list-panes, one record per pane.
var PaneFormat = strings.Join([]string{
	"#{session_name}",
	"#{window_index}",
	"#{pane_index}",
	"#{window_active}",
	"#{pane_active}",
	"#{pane_dead}",
	"#{window_name}",
	"#{pane_title}",
	"#{pane_current_command}",
}, paneFieldSep)

// ParsePanes parses list-panes -F output into panes.
// A record that does not carry exactly paneFieldCount fields is dropped rather
// than guessed at: a mis-attributed title would be shown to the user as fact,
// which is worse than showing no summary at all.
//
// Records are split on newlines before the field count is checked, so a newline
// inside a field splits one pane's record in two. The tail is then usually
// dropped, but if it carries enough separators of its own it parses as a record
// in its own right -- and since the session name is its first field, that record
// can name a session that exists, displacing that session's real summary with a
// fragment of another pane's. Field-order is therefore not guaranteed against a
// value containing a raw newline.
//
// This line-based format assumes fields do not contain the record terminator.
// Do not treat field-count validation as escaping or as a security boundary:
// executable names and argv can contain newlines.
func ParsePanes(stdout string) []session.Pane {
	out := make([]session.Pane, 0)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, paneFieldSep)
		if len(fields) != paneFieldCount {
			continue
		}
		out = append(out, session.Pane{
			Session:        fields[0],
			WindowIndex:    atoiOrZero(fields[1]),
			PaneIndex:      atoiOrZero(fields[2]),
			WindowActive:   fields[3] == tmuxTrue,
			PaneActive:     fields[4] == tmuxTrue,
			Dead:           fields[5] == tmuxTrue,
			WindowName:     fields[6],
			Title:          fields[7],
			CurrentCommand: fields[8],
		})
	}
	return out
}

// atoiOrZero parses a numeric tmux field, treating anything unparseable as zero
// rather than discarding the whole record.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// paneTargetOf turns a session name into a target naming that session's active
// pane.
//
// The trailing colon is load-bearing. `display-message -t` wants a *pane*
// target, and a bare `=name` is only a session target: tmux accepts it, resolves
// no pane, and prints an empty line with a zero exit status. That reads as "this
// session has no directory" rather than as the targeting mistake it is. Adding
// the colon makes it a session:window target, which resolves to the session's
// current window and its active pane -- still an exact session match, so the
// name is never treated as a pattern.
func paneTargetOf(name string) string {
	return ExactTarget(name) + ":"
}

// PaneWorkingDirectory reports the working directory of a session's active pane.
//
// It is what the browser opens the file manager at: a navigation seed, not an
// authorization input. The boundary is decided the same way wherever the user
// goes afterwards.
func (c *Client) PaneWorkingDirectory(ctx context.Context, name string) (string, error) {
	stdout, stderr, code, err := c.Exec(ctx,
		"display-message", "-p", "-t", paneTargetOf(name), "#{pane_current_path}")
	if err != nil {
		return "", fmt.Errorf("exec display-message: %w", err)
	}
	if code != 0 {
		if strings.Contains(stderr, "can't find session") || IsNoServer(stderr) {
			return "", session.ErrNotFound
		}
		return "", fmt.Errorf("tmux display-message: %s", strings.TrimSpace(stderr))
	}

	// An unknown format expands to nothing, which is not a directory the file
	// manager could open at.
	path := strings.TrimSpace(stdout)
	if path == "" {
		return "", session.ErrNotFound
	}
	return path, nil
}

// ListPanes returns every pane across all sessions on the tmux server in a
// single invocation. If no server is running, returns an empty slice.
func (c *Client) ListPanes(ctx context.Context) ([]session.Pane, error) {
	stdout, stderr, code, err := c.Exec(ctx, "list-panes", "-a", "-F", PaneFormat)
	if err != nil {
		return nil, fmt.Errorf("exec list-panes: %w", err)
	}
	if code != 0 {
		if IsNoServer(stderr) {
			return []session.Pane{}, nil
		}
		return nil, fmt.Errorf("tmux list-panes: %s", strings.TrimSpace(stderr))
	}
	return ParsePanes(stdout), nil
}
