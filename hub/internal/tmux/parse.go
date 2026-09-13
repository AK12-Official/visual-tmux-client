package tmux

import (
	"strconv"
	"strings"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// SessionFormat is the -F format string for list-sessions: one pipe-delimited record per line.
const SessionFormat = "#{session_name}|#{session_windows}|#{session_attached}|#{session_created}"

// ExactTarget prefixes a session name with "=" so tmux matches it exactly, never as a pattern.
func ExactTarget(name string) string {
	return "=" + name
}

// IsNoServer reports whether tmux's stderr indicates that no server is running.
func IsNoServer(stderr string) bool {
	return strings.Contains(stderr, "no server running") || strings.Contains(stderr, "error connecting to")
}

// ParseSessions parses list-sessions -F output into a slice of session.Session.
// The three trailing fields are numeric, so the line is split from the right:
// a session name may itself contain "|" which a left-anchored split would truncate.
func ParseSessions(stdout string) []session.Session {
	out := make([]session.Session, 0)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		const minParts = 4
		if len(parts) < minParts {
			continue
		}
		n := len(parts)
		name := strings.Join(parts[:n-3], "|")
		windows, err := strconv.Atoi(parts[n-3])
		if err != nil {
			windows = 0
		}
		attached, err := strconv.Atoi(parts[n-2])
		if err != nil {
			attached = 0
		}
		created, err := strconv.ParseInt(parts[n-1], 10, 64)
		if err != nil {
			created = 0
		}
		out = append(out, session.Session{
			Name:     name,
			Windows:  windows,
			Attached: attached,
			Created:  created,
		})
	}
	return out
}
