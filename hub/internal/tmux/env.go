package tmux

import (
	"os"
	"strings"
)

// ScrubbedEnv returns the process environment with hub secrets and tmux nesting variables removed.
func ScrubbedEnv() []string {
	return FilterEnv(os.Environ(), "TMUX", "TMUX_PANE", "VISUAL_TMUX_CLIENT_TOKEN")
}

// FilterEnv returns env with entries whose key matches any of the given drop names removed.
func FilterEnv(env []string, drop ...string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropSet[d] = struct{}{}
	}
	var out []string
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if _, ok := dropSet[key]; ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ChildEnv returns an environment for attached session processes with hub secrets removed
// and TERM pinned to xterm-256color.
func ChildEnv() []string {
	return append(ScrubbedEnv(), "TERM=xterm-256color")
}
