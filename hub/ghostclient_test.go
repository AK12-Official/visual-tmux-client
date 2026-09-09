package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Killing the `tmux attach-session` CLIENT process must only detach it: the
// session itself must survive. onClientGone depends on this — the whole point
// is that a browser disconnect does not destroy the user's session.
func TestDetachReleasesClientAndKeepsSession(t *testing.T) {
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	sock := uniqueSocket(t)
	tmux, _ := exec.LookPath("tmux")
	if _, _, code, err := runCommand(tmux, "-L", sock, "new-session", "-d", "-s", "keep"); err != nil || code != 0 {
		t.Fatalf("new-session: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { runCommand(tmux, "-L", sock, "kill-server") })

	cmd := exec.Command(tmux, "-L", sock, "attach-session", "-t", "=keep")
	cmd.Env = childEnv()
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("pty.StartWithSize: %v", err)
	}
	t.Cleanup(func() { _ = ptmx.Close() })

	// Park a Read, like outputPump does.
	readReturned := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			_, err := ptmx.Read(buf)
			if err != nil {
				readReturned <- err
				return
			}
		}
	}()
	time.Sleep(700 * time.Millisecond) // let the client fully attach

	out, _, _, _ := runCommand(tmux, "-L", sock, "list-clients", "-t", "=keep")
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected an attached client before kill")
	}

	// The teardown we are about to ship: close the pty, then kill the client.
	_ = ptmx.Close()
	_ = cmd.Process.Kill()

	select {
	case err := <-readReturned:
		t.Logf("detach: parked Read returned, err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("detach: Read still blocked after Close+Kill")
	}
	if err := cmd.Wait(); err != nil {
		t.Logf("detach: child reaped, Wait err=%v", err)
	}

	// The session MUST still exist, and the ghost client must be gone.
	time.Sleep(500 * time.Millisecond)
	if _, _, code, _ := runCommand(tmux, "-L", sock, "has-session", "-t", "=keep"); code != 0 {
		t.Fatalf("detach REGRESSION: killing the attach client destroyed the session")
	}
	clients, _, _, _ := runCommand(tmux, "-L", sock, "list-clients", "-t", "=keep")
	t.Logf("detach RESULT: session survived; remaining clients=%q", strings.TrimSpace(clients))
	if strings.TrimSpace(clients) != "" {
		t.Errorf("detach: ghost client still attached: %q", strings.TrimSpace(clients))
	}
}
