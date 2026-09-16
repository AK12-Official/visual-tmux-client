// Package testutil holds helpers shared by tests across the hub. It is imported
// only from _test.go files, so it is never linked into the shipped binary.
package testutil

import (
	"os"
	"testing"
)

// SocketDir returns a fresh directory short enough to hold a unix socket.
//
// tmux places its server socket at TMUX_TMPDIR/tmux-<uid>/<socket name>, and the
// platform's sun_path limit (~104 bytes on macOS) counts the entire path.
// t.TempDir() nests under the OS temp root — on macOS that is
// /var/folders/<long id>/T/<test name>/<counter> — which is already long enough
// that tmux cannot bind at all and every test fails with "File name too long".
//
// /tmp is used deliberately. Callers must keep their own socket names short too.
func SocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "vtc")
	if err != nil {
		t.Skipf("no short temp directory available for a tmux socket: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir) //nolint:errcheck // test-scoped directory
	})
	return dir
}
