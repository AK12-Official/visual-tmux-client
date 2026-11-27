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

// lastEnvValue returns the value a child process keeps for name when the name
// repeats in env, and whether env names it at all.
func lastEnvValue(env []string, name string) (string, bool) {
	value, found := "", false
	for _, entry := range env {
		if value2, ok := strings.CutPrefix(entry, name+"="); ok {
			value, found = value2, true
		}
	}
	return value, found
}

// hubTmuxSocket returns the socket a hub under test is bound to: a short name
// directly inside the last TMUX_TMPDIR of env, the entry the hub keeps when
// the name repeats. The directory exists for as long as the test does, and
// tmux needs it to: -S never creates one. It is empty when env names no
// absolute directory.
func hubTmuxSocket(env []string) string {
	dir, _ := lastEnvValue(env, "TMUX_TMPDIR")
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	return filepath.Join(dir, "hub.sock")
}

// bindTmuxToSocket puts a tmux first on the PATH of a hub under test: the real
// binary, with -S socket in front of every command it is given. The hub has
// no setting for its tmux socket, so on its own it names none and its
// isolation rests on TMUX_TMPDIR alone. tmux discards a TMUX_TMPDIR that no
// longer exists in favour of the default position, where a developer's server
// lives; a socket named with an absolute -S never falls back. Without tmux
// installed there is nothing for a hub to reach, so env returns unchanged.
func bindTmuxToSocket(t *testing.T, env []string, socket string) []string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		return env
	}
	if socket == "" || !filepath.IsAbs(socket) {
		t.Fatalf("the hub environment names no absolute TMUX_TMPDIR, so its tmux has no socket to bind to")
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create the tmux wrapper directory: %v", err)
	}
	wrapper := filepath.Join(binDir, "tmux")
	// Guard the socket directory in the wrapper as well: tmux 3.4 (Ubuntu 24.04)
	// fails to create the socket when its parent directory is gone, yet exits 0.
	script := fmt.Sprintf(
		"#!/bin/sh\n"+
			"if [ ! -d %q ]; then\n"+
			"  echo \"bound tmux: socket directory does not exist\" >&2\n"+
			"  exit 1\n"+
			"fi\n"+
			"exec %q -S %q \"$@\"\n",
		filepath.Dir(socket), real, socket,
	)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write the tmux wrapper: %v", err)
	}
	path, _ := lastEnvValue(env, "PATH")
	bound := append(tmux.FilterEnv(env, "PATH"), "PATH="+binDir+string(os.PathListSeparator)+path)
	return bound
}
func TestHubTmuxSocketKeepsTheLastTemporaryDirectory(t *testing.T) {
	want := "/tmp/second/hub.sock"
	env := []string{"PATH=/bin", "TMUX_TMPDIR=/tmp/first", "TMUX_TMPDIR=/tmp/second"}
	if got := hubTmuxSocket(env); got != want {
		t.Fatalf("hubTmuxSocket = %q, want %q", got, want)
	}
	for name, given := range map[string][]string{
		"no directory":    {"PATH=/bin"},
		"relative one":    {"TMUX_TMPDIR=relative"},
		"relative second": {"TMUX_TMPDIR=/tmp/first", "TMUX_TMPDIR=relative"},
	} {
		if got := hubTmuxSocket(given); got != "" {
			t.Errorf("%s: hubTmuxSocket = %q, want none", name, got)
		}
	}
}

// The bound tmux reaches its server through the named socket, even while the
// temporary directory tmux would use on its own does exist.
func TestBoundTmuxIgnoresTheTemporaryDirectory(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	socket := filepath.Join(testutil.SocketDir(t), "bound.sock")
	other := testutil.SocketDir(t)
	env := bindTmuxToSocket(t, testTmuxEnv(other), socket)
	t.Cleanup(func() {
		_, _, _, _ = runCmd(tmux, env, "-S", socket, "kill-server") //nolint:errcheck // disposable server cleanup
	})
	path, _ := lastEnvValue(env, "PATH")
	bound, _, _ := strings.Cut(path, string(os.PathListSeparator))
	if bound == "" || !strings.HasPrefix(path, bound+string(os.PathListSeparator)) {
		t.Fatalf("the bound tmux is not first on the PATH: %q", path)
	}

	_, stderr, code, err := runCmd(bound+"/tmux", env, "-f", "/dev/null",
		"new-session", "-d", "-s", "bound", "sleep", "30")
	if err != nil || code != 0 {
		t.Fatalf("new-session through the bound tmux: code=%d err=%v stderr=%s", code, err, stderr)
	}
	out, _, code, err := runCmd(tmux, env, "-S", socket, "list-sessions", "-F", "#{session_name}")
	if err != nil || code != 0 || strings.TrimSpace(out) != "bound" {
		t.Fatalf("the named socket: sessions=%q code=%d err=%v", out, code, err)
	}
	// The socket tmux would have chosen on its own holds no server.
	onItsOwn := filepath.Join(other, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	if _, _, code, err := runCmd(tmux, env, "-S", onItsOwn, "list-sessions"); err != nil || code == 0 {
		t.Fatalf("a server answered on the socket tmux picks on its own: code=%d err=%v", code, err)
	}
}

// This is the incident the binding exists for: a hub that runs tmux after its
// temporary directory is gone. Unbound, tmux would discard the directory and
// start the session on the default socket, where a developer's server lives;
// the command would then succeed. Bound, it must fail.
func TestBoundTmuxFailsOnceTheDirectoryIsGone(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	socketDir := testutil.SocketDir(t)
	env := bindTmuxToSocket(t, testTmuxEnv(socketDir), filepath.Join(socketDir, "hub.sock"))
	if err := os.RemoveAll(socketDir); err != nil {
		t.Fatal(err)
	}
	path, _ := lastEnvValue(env, "PATH")
	bound, _, _ := strings.Cut(path, string(os.PathListSeparator))
	_, stderr, code, err := runCmd(bound+"/tmux", env, "new-session", "-d", "-s", "gone", "sleep", "30")
	if err != nil || code == 0 {
		t.Fatalf(
			"a session was created after the temporary directory went: code=%d err=%v stderr=%s",
			code, err, stderr,
		)
	}
	if _, statErr := os.Stat(socketDir); !os.IsNotExist(statErr) {
		t.Fatalf("temporary directory reappeared after bound tmux ran: err=%v", statErr)
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
