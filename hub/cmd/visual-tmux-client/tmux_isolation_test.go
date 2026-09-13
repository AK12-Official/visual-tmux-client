package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

func TestTerminalTestsPreserveParentTmux(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := t.TempDir()
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
