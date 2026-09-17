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
// Records are split on newlines before the field count is checked, so a field
// carrying the record terminator would not be read as one field. What follows
// depends on which field it is:
//
//   - A break before the last field drops that pane's record -- the fields before
//     the break are too few to parse -- and leaves the tail to be read, with the
//     separators a forger writes into it and the fields that follow it in the
//     real record, as a record of its own.
//   - A break in the last field keeps the pane's record, read with its command
//     truncated, and the tail may be read as a record of its own all the same.
//
// Either way a record can appear that no pane stands behind, and since a
// record's first field is a session name, what it names is a session that
// exists: that session's summary is then chosen from a fragment of another
// pane's field. The field count does not detect either case and must not be read
// as a defence -- it validates the shape of what it was given, and says nothing
// about whether a value inside it held one field or two.
// TestALineBreakInAFieldWouldNotBeDetected pins both outcomes, so that neither
// has to be worked out from here.
//
// What keeps the terminator out of the fields is tmux, not this parser. A
// session name and a window name containing a newline are refused when they are
// set ("invalid session name", "invalid window name"), and a pane title
// containing one is refused by select-pane -- silently, leaving the title as it
// was, which is why the test asserts the title rather than a status. The indexes
// and the flags are tmux's own numbers.
// TestNamesTmuxRefusesToLetCarryALineBreak pins those three against a real
// server, because none of it is this code's to guarantee: the assumption is
// measured against tmux, and a version that stopped holding it would fail that
// test rather than quietly mis-attribute a summary.
//
// The command field is the one a program names itself, and it is the one thing
// here that no test covers: what tmux reports for it was seen to escape a line
// break once, which later attempts could not reproduce, and a process whose own
// name carries one could not be produced on this machine at all. That tmux
// escapes control bytes on its way out is a real behaviour -- it is why the
// separator in this file is printable, which the delimiter test pins -- so the
// observation is recorded as the likely answer rather than dismissed. It is the
// residual this comment exists to state.
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

	// Only the terminator tmux appends is removed, and exactly one of it.
	//
	// TrimSpace would take real characters with it. A directory name may legally
	// begin or end with a space or a tab, and one that does would be opened at a
	// different path than the session is actually in -- or, where the trimmed
	// remainder is empty, reported as a session with no directory at all.
	//
	// Exactly one, rather than every trailing newline, because a path may itself
	// contain one: the file is written as the path followed by a single newline,
	// so removing a single newline leaves a path that ends in one intact. For the
	// same reason a carriage return is not stripped here as it is from a listing
	// line: tmux writes the format's own bytes, so a trailing carriage return is
	// far more likely to be the last character of the directory's name than half
	// of a line ending that was never written.
	//
	// An unknown format expands to nothing, which is not a directory the file
	// manager could open at.
	path := strings.TrimSuffix(stdout, "\n")
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
