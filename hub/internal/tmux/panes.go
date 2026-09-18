package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// paneFieldCount is the number of fields PaneFormat emits.
const paneFieldCount = 9

// tmuxTrue is how tmux renders a set boolean in a format string.
const tmuxTrue = "1"

// paneFormatFields is the order shared by PaneFormat and ParsePanes.
var paneFormatFields = []string{
	"session_name",
	"window_index",
	"pane_index",
	"window_active",
	"pane_active",
	"pane_dead",
	"window_name",
	"pane_title",
	"pane_current_command",
}

// framedPaneField emits one byte-length-prefixed value. tmux's n modifier is
// explicitly a byte count, so arbitrary field bytes -- including newlines,
// colons and the strings another record uses -- cannot change the framing.
func framedPaneField(name string) string {
	return "#{n:" + name + "}:#{" + name + "}"
}

// PaneFormat is the -F format string for list-panes, one framed record per pane.
var PaneFormat = func() string {
	var out strings.Builder
	for _, name := range paneFormatFields {
		out.WriteString(framedPaneField(name))
	}
	return out.String()
}()

// ParsePanes parses length-prefixed list-panes output. Length framing is the
// boundary: no field value is treated as syntax, so a command or externally
// created session containing a newline cannot create a record that no pane
// stands behind. A malformed frame stops parsing rather than guessing where the
// next record begins; displaying fewer summaries is safer than mis-attributing
// one to another session.
func ParsePanes(stdout string) []session.Pane {
	out := make([]session.Pane, 0)
	for len(stdout) > 0 {
		// Tolerate blank transport lines, including CRLF, around records.
		if strings.HasPrefix(stdout, "\r\n") {
			stdout = stdout[2:]
			continue
		}
		if strings.HasPrefix(stdout, "\n") {
			stdout = stdout[1:]
			continue
		}

		fields, rest, ok := parsePaneRecord(stdout)
		if !ok {
			break
		}
		stdout = rest
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

// parsePaneRecord reads exactly paneFieldCount byte-length-prefixed fields and
// the line ending tmux appends to the formatted record.
func parsePaneRecord(input string) ([]string, string, bool) {
	fields := make([]string, 0, paneFieldCount)
	for range paneFieldCount {
		colon := strings.IndexByte(input, ':')
		if colon <= 0 {
			return nil, input, false
		}
		length, err := strconv.Atoi(input[:colon])
		if err != nil || length < 0 || length > len(input)-colon-1 {
			return nil, input, false
		}
		input = input[colon+1:]
		fields = append(fields, input[:length])
		input = input[length:]
	}
	if strings.HasPrefix(input, "\r\n") {
		return fields, input[2:], true
	}
	if strings.HasPrefix(input, "\n") {
		return fields, input[1:], true
	}
	if input == "" {
		return fields, input, true
	}
	return nil, input, false
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
