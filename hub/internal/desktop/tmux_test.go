package desktop

import (
	"errors"
	"testing"
)

func TestResolveTmux(t *testing.T) {
	for _, tc := range []struct{ name, configured, platform, path, fallback, want string }{
		{"explicit", "/custom/tmux", "darwin", "/path/tmux", "/opt/homebrew/bin/tmux", "/custom/tmux"},
		{"path", "", "darwin", "/path/tmux", "/opt/homebrew/bin/tmux", "/path/tmux"},
		{"homebrew", "", "darwin", "", "/opt/homebrew/bin/tmux", "/opt/homebrew/bin/tmux"},
		{"intel", "", "darwin", "", "/usr/local/bin/tmux", "/usr/local/bin/tmux"},
		{"linux-no-fallback", "", "linux", "", "/opt/homebrew/bin/tmux", ""},
		{"missing", "", "darwin", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTmux(tc.configured, tc.platform, func(string) (string, error) {
				if tc.configured != "" {
					t.Fatal("lookup overrides explicit path")
				}
				if tc.path == "" {
					return "", errors.New("not found")
				}
				return tc.path, nil
			}, func(path string) bool { return path == tc.fallback })
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
