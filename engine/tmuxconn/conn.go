// Package tmuxconn connects the pure control-mode parser (tmuxcm) to a real
// tmux server over a pty-backed subprocess, and translates the resulting
// notifications into domain model mutations and event bus publications.
package tmuxconn

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"

	"visual-tmux-client/engine/tmuxcm"
)

// sessionConn is one `tmux -CC attach` subprocess, its pty, and the parser
// state for its output stream. It is attached to exactly one session for
// the lifetime of the process, per design.md's per-session connection
// granularity decision.
type sessionConn struct {
	cmd  *exec.Cmd
	ptmx *os.File

	parser *tmuxcm.Parser

	mu        sync.Mutex
	resultCh  chan *tmuxcm.CommandResult
	awaiting  bool
	closeOnce sync.Once

	cmdMu sync.Mutex // serializes issueCommand calls on this connection

	exited chan struct{} // closed by readLoop when the pty read loop returns
}

// spawnSessionConn starts `tmux <socketArgs...> -CC attach [-t target]`
// under a pty. target may be empty to attach to the most-recently-used
// session (used for the bootstrap/discovery connection).
func spawnSessionConn(socketArgs []string, target string) (*sessionConn, error) {
	args := append([]string{}, socketArgs...)
	args = append(args, "-CC", "attach")
	if target != "" {
		args = append(args, "-t", target)
	}

	cmd := exec.Command("tmux", args...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("tmuxconn: starting %v under pty: %w", cmd.Args, err)
	}

	sc := &sessionConn{
		cmd:      cmd,
		ptmx:     ptmx,
		parser:   tmuxcm.NewParser(),
		resultCh: make(chan *tmuxcm.CommandResult, 1),
		exited:   make(chan struct{}),
	}
	return sc, nil
}

// readLoop reads from the pty until it closes or errors, feeding bytes into
// the parser and dispatching each resulting Event to onNotification or the
// pending command-result waiter. It returns when the underlying pty is
// closed (subprocess exited or Close was called).
func (sc *sessionConn) readLoop(onNotification func(*tmuxcm.Notification)) {
	defer close(sc.exited)
	buf := make([]byte, 4096)
	for {
		n, err := sc.ptmx.Read(buf)
		if n > 0 {
			for _, evt := range sc.parser.Feed(buf[:n]) {
				switch {
				case evt.Notification != nil:
					onNotification(evt.Notification)
				case evt.Result != nil:
					sc.mu.Lock()
					expecting := sc.awaiting
					sc.mu.Unlock()
					if expecting {
						select {
						case sc.resultCh <- evt.Result:
						default:
						}
					}
					// Else: an unsolicited %begin/%end block (tmux sends
					// one immediately on attach, before any command is
					// sent, using the server's running command counter —
					// confirmed empirically, see spike-notes.md). Drop it;
					// nothing is waiting for it.
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// issueCommand writes cmd to the control-mode connection and blocks for its
// %begin/%end (or %error) response block. Only one command may be
// outstanding at a time per connection; callers are responsible for
// serializing calls (host.go does this per HostConn via ctrlMu).
func (sc *sessionConn) issueCommand(cmd string) (*tmuxcm.CommandResult, error) {
	sc.cmdMu.Lock()
	defer sc.cmdMu.Unlock()

	sc.mu.Lock()
	sc.awaiting = true
	sc.mu.Unlock()
	defer func() {
		sc.mu.Lock()
		sc.awaiting = false
		sc.mu.Unlock()
	}()

	if _, err := sc.ptmx.Write([]byte(cmd + "\n")); err != nil {
		return nil, fmt.Errorf("tmuxconn: writing command %q: %w", cmd, err)
	}
	select {
	case res := <-sc.resultCh:
		if res.IsError {
			return res, fmt.Errorf("tmuxconn: command %q failed: %v", cmd, res.Lines)
		}
		return res, nil
	case <-sc.exited:
		return nil, fmt.Errorf("tmuxconn: connection closed while waiting for response to %q", cmd)
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("tmuxconn: timed out waiting for response to %q", cmd)
	}
}

// Close kills (and reaps) the subprocess before closing our own end of the
// pty. Closing the pty master from a different goroutine than the one
// blocked in Read on it is unsafe on this platform (observed to hang the
// read indefinitely); killing the subprocess first reliably unblocks that
// read with a genuine io.EOF, since the slave side actually closes.
func (sc *sessionConn) Close() error {
	var err error
	sc.closeOnce.Do(func() {
		if sc.cmd.Process != nil {
			_ = sc.cmd.Process.Kill()
		}
		_ = sc.cmd.Wait()
		err = sc.ptmx.Close()
	})
	return err
}

// Alive reports whether this connection's read loop is still running (its
// subprocess has not exited and its pty has not been closed).
func (sc *sessionConn) Alive() bool {
	select {
	case <-sc.exited:
		return false
	default:
		return true
	}
}
