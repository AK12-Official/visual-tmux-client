package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
)

// Never let a test inherit the caller's tmux server or temporary directory.
// Cleanup must also use an explicit -S socket, independently of this environment.
func testTmuxEnv(dir string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "TMUX" && key != "TMUX_PANE" && key != "TMUX_TMPDIR" {
			env = append(env, entry)
		}
	}
	return append(env, "TMUX_TMPDIR="+dir)
}

// hubEnv returns the environment for a hub under test. The hub keeps its host
// state in the config directory, which Linux reads from XDG_CONFIG_HOME and
// macOS from HOME, so both name the test's directory and appear exactly once:
// where a name repeats, the platform decides which value holds, and a repeated
// real HOME lets a hub under test open the user's own state.
func hubEnv(env []string, stateDir string) []string {
	return append(
		tmux.FilterEnv(env, "HOME", "XDG_CONFIG_HOME"),
		"XDG_CONFIG_HOME="+stateDir,
		"HOME="+stateDir,
	)
}

func TestHubEnvKeepsTheHubAwayFromTheUserState(t *testing.T) {
	given := []string{"PATH=/bin", "HOME=/real/home", "XDG_CONFIG_HOME=/real/xdg", "HOME=/real/home"}
	env := hubEnv(given, "/test/state")
	want := []string{"PATH=/bin", "XDG_CONFIG_HOME=/test/state", "HOME=/test/state"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("hub environment:\n got %q\nwant %q", env, want)
	}
}

func TestTerminalTestsPreserveParentTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := testutil.SocketDir(t)
	socket := filepath.Join(dir, "parent.sock")
	env := testTmuxEnv(dir)
	// This disposable server stands in for the user's real session.
	_, stderr, code, err := runCmd(tmux, env, "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "parent", "sleep 120")
	if err != nil || code != 0 {
		t.Fatalf("start parent: code=%d err=%v stderr=%s", code, err, stderr)
	}
	t.Cleanup(func() {
		_, _, _, _ = runCmd(tmux, env, "-S", socket, "kill-server") //nolint:errcheck // disposable parent cleanup
	})
	pid, stderr, code, err := runCmd(tmux, env, "-S", socket, "display-message", "-p", "#{pid}")
	if err != nil || code != 0 {
		t.Fatalf("parent pid: code=%d err=%v stderr=%s", code, err, stderr)
	}
	t.Setenv("TMUX", fmt.Sprintf("%s,%s,0", socket, strings.TrimSpace(pid)))
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("TMUX_TMPDIR", dir)
	for _, test := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"smoke", TestSmokeTerminalInputOutputAndReconnect},
		{"restart", TestE2ERestartPreservesSession},
	} {
		t.Run(test.name, test.run)
		// t.Run returns only after the child test's cleanup has completed.
		out, stderr, code, err := runCmd(tmux, env, "-S", socket, "list-sessions", "-F", "#{session_name}")
		if err != nil || code != 0 || strings.TrimSpace(out) != "parent" {
			t.Fatalf("%s affected parent server: sessions=%q code=%d err=%v stderr=%s",
				test.name, out, code, err, stderr)
		}
	}
}
