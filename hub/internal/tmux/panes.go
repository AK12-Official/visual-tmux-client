package tmux

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// paneFieldSep is ASCII Unit Separator. A pane record carries free-form text
// (window name, pane title, current command), so the right-split trick used by
// ParseSessions cannot be generalised to it, and a printable separator would
// collide with titles that legitimately contain it.
const paneFieldSep = "\x1f"

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
