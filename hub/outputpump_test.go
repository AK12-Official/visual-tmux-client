package main

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// outputPump must terminate when the queue closes under it — the state
// writePump leaves behind when a WebSocket write fails (closeAbnormal, no
// pty EOF of its own).
//
// The child is silent (`exec cat` never writes to the pty), so outputPump is
// parked in ptmx.Read and cannot be woken by incidental output. That is the
// case that matters and the one an already-closed queue does NOT exercise: an
// attachment whose queue is closed before the first iteration takes the closed
// branch immediately, without a Read ever being in flight.
//
// Closing the pty master alone does not unpark that Read: creack/pty's master
// is not registered with the netpoller, so os.File.Close cannot interrupt an
// in-flight Read (it defers the real close(2) until the Read returns) while
// still reporting success. The child must be signalled. See attachment.detach.
//
// This is a unit-level check of the teardown logic with no tmux involved. The
// equivalent end-to-end case, with real tmux on a silent session, is
// TestDisconnectReleasesSilentSession — where the leak is also reachable.
func TestOutputPumpClosedQueueReapsChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exec cat") // stands in for `tmux attach-session`
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
	})

	a := &attachment{ptmx: ptmx, cmd: cmd}

	done := make(chan struct{})
	go func() {
		a.outputPump()
		close(done)
	}()

	// Let outputPump reach and park in ptmx.Read before anything closes.
	time.Sleep(500 * time.Millisecond)
	select {
	case <-done:
		t.Fatalf("outputPump returned before the queue was closed")
	default:
	}

	// Exactly what writePump does when a.c.Write fails.
	a.queue.closeAbnormal()
	a.detach()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("outputPump did not return: parked in Read or blocked in cmd.Wait() on a child nothing terminated")
	}

	// The child must be gone and reaped, not left attached to the session.
	//
	// The liveness probe is signal 0, the standard "does this process exist"
	// check: it returns nil for a live process and ErrProcessDone once the
	// process has been reaped. Signal(nil) cannot be used here — nil is not a
	// syscall.Signal, so os.Process.Signal rejects it with "unsupported signal
	// type" whether the child is alive or dead, which would make this assertion
	// vacuous and hide exactly the leak the test exists to catch.
	if err := cmd.Process.Signal(syscall.Signal(0)); err == nil {
		t.Fatalf("outputPump returned but child is still alive (leaked tmux client)")
	}
}
