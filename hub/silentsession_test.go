package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A disconnect must release the tmux client and let outputPump exit, even when
// the session is completely silent.
//
// This is the case that Close alone does not handle. creack/pty's master fd is
// not registered with the netpoller, so os.File.Close cannot interrupt a Read
// that is already in flight: it drops a reference and defers the real close(2)
// until that Read returns, while still reporting success. outputPump is parked
// in exactly that Read whenever the session is quiet, so without signalling the
// child it stays parked, the child is never reaped, and the tmux client remains
// attached for the hub's lifetime. Because the window-size policy is "latest"
// (see pinWindowSizePolicy), such a ghost client keeps constraining the real
// user's terminal size.
//
// The test is written to be deterministic rather than to depend on timing:
//   - the pane runs `sleep`, so it never writes
//   - the status line is disabled, so tmux emits no periodic repaint
//   - teardown happens only after an initial quiet window, so any incidental
//     one-off terminal output (mode resets and similar arrive a few seconds in)
//     is past and cannot unpark the Read by coincidence
//   - liveness is sampled with ps and goroutine stacks only, never a tmux
//     command, since running one generates pty output that would itself unpark
//     the Read and mask the defect
func TestDisconnectReleasesSilentSession(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)

	if _, _, code, err := runCommand(tmux, "-L", sock, "new-session", "-d", "-s", "quiet", "sleep", "3600"); err != nil || code != 0 {
		t.Fatalf("new-session: code=%d err=%v", code, err)
	}
	runCommand(tmux, "-L", sock, "set-option", "-g", "status", "off")
	runCommand(tmux, "-L", sock, "set-option", "-g", "status-interval", "0")

	id, _, _ := s.tickets.issue("quiet")
	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/quiet?ticket=%s&cols=80&rows=24", id), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("ready: %v", err)
	}

	var pid int
	s.mu.Lock()
	for a := range s.attachments {
		if a.cmd != nil && a.cmd.Process != nil {
			pid = a.cmd.Process.Pid
		}
	}
	s.mu.Unlock()
	if pid == 0 {
		t.Fatalf("could not determine attach child pid")
	}
	t.Cleanup(func() {
		// Never leave a parked child behind if this test fails.
		_ = exec.Command("kill", "-9", fmt.Sprint(pid)).Run()
	})

	// Drain the attach paint and any incidental one-off output, then confirm
	// quiescence, so outputPump is genuinely parked in Read at teardown.
	//
	// The read context must NOT carry a deadline. coder/websocket arms the read
	// timeout from the context passed to Read and closes the whole connection
	// when it fires, so draining with an expiring context makes the drain itself
	// the disconnect — the server tears the attachment down there, the CloseNow
	// below becomes a no-op, and the "live outputPump before teardown" check
	// races a teardown already in flight (it fails outright once the teardown
	// wins, which on a slow box it does). Read in a goroutine on a cancellable
	// context instead, and detect quiescence from the arrival times.
	var lastMu sync.Mutex
	last := time.Now()
	readCtx, stopRead := context.WithCancel(context.Background())
	defer stopRead()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
			lastMu.Lock()
			last = time.Now()
			lastMu.Unlock()
		}
	}()

	quietDeadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(quietDeadline) {
		time.Sleep(250 * time.Millisecond)
		lastMu.Lock()
		quiet := time.Since(last)
		lastMu.Unlock()
		if quiet > 4*time.Second {
			break
		}
	}

	outputPumps := func() int {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		return strings.Count(string(buf[:n]), "attachment).outputPump")
	}
	if outputPumps() == 0 {
		t.Fatalf("expected a live outputPump goroutine before teardown")
	}

	_ = conn.CloseNow() // browser vanishes; no close handshake

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		out, _ := exec.Command("ps", "-o", "command=", "-p", fmt.Sprint(pid)).Output()
		childAlive := strings.Contains(string(out), "attach-session")
		if !childAlive && outputPumps() == 0 {
			return
		}
	}
	out, _ := exec.Command("ps", "-o", "stat=,command=", "-p", fmt.Sprint(pid)).Output()
	t.Fatalf("silent session not released 20s after disconnect: outputPump goroutines=%d, ps=%q",
		outputPumps(), strings.TrimSpace(string(out)))
}
