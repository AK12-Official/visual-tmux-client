package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/ringbuffer"
	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Backpressure thresholds and poll interval for the output pump. When the
// WebSocket writer falls behind, the output pump stops reading the pty so the
// kernel's pty buffer fills and tmux itself blocks on write — the correct
// end-to-end flow-control mechanism. Terminal output is never dropped and never
// buffered without bound.
const (
	outputHighWater = 1 << 20   // 1 MB: stop reading the pty above this
	outputLowWater  = 128 << 10 // 128 KB: resume below this
	pollInterval    = 100 * time.Millisecond
)

const maxDim = 1000 // max accepted terminal cols/rows

// maxInputMessage bounds a single inbound WebSocket message. The library
// default is 32 KiB, which silently kills the connection with
// StatusMessageTooBig when a user pastes more than that — the frontend sends a
// whole paste as ONE binary frame, so this was reachable by ordinary use.
//
// 8 MiB is the cap because Conn.Read buffers a full message in memory before
// returning it (io.ReadAll), so this value IS the per-connection input
// high-water mark: bounded, not unbounded. It comfortably covers a legitimate
// paste (a 100k-line log is a few MiB) while refusing a frame sized to exhaust
// hub memory. The pty write itself is chunked (see writeInput), so a paste this
// large is drained at the tty's pace rather than buffered again by the hub.
const maxInputMessage = 8 << 20

// inputChunk is the unit in which a client message is fed to the pty. Writing a
// multi-MiB paste in one os.File.Write would block in a single uninterruptible
// syscall until the tty drained it; chunking keeps each write short so the
// attachment stays responsive to teardown between chunks.
const inputChunk = 32 * 1024

// serverMessage types are the JSON text frames the server sends to the client.
type readyMessage struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
}

type exitMessage struct {
	Type string `json:"type"`
	Code *int   `json:"code"`
}

type errorMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type pongMessage struct {
	Type string `json:"type"`
}

// clientMessage is the JSON text frame the client sends (resize/ping).
type clientMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// outputQueue is a small byte-counting FIFO between the pty reader and the
// WebSocket writer. It carries the backpressure state: the reader pauses when
// the queued byte count reaches the high watermark, and the writer drains it.
type outputQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	chunks   [][]byte
	bytes    int
	paused   bool
	closed   bool
	exitCode *int // set on pty EOF so the writer can send the exit frame last
}

func (q *outputQueue) getCond() *sync.Cond {
	if q.cond == nil {
		q.cond = sync.NewCond(&q.mu)
	}
	return q.cond
}

func (q *outputQueue) push(b []byte) {
	if len(b) == 0 {
		return
	}
	q.mu.Lock()
	cond := q.getCond()
	q.chunks = append(q.chunks, append([]byte(nil), b...))
	q.bytes += len(b)
	cond.Signal()
	q.mu.Unlock()
}

// shouldRead is the backpressure gate: it reports whether the output pump may
// read more from the pty. Reading is paused once the queued byte count reaches
// the high watermark, and does not resume until it drains below the low
// watermark (hysteresis, to avoid thrashing at the boundary). It separately
// reports whether the queue is closed, which is terminal rather than a pause:
// the pump must stop entirely, since a closed queue never drains again.
func (q *outputQueue) shouldRead() (read, closed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, true
	}
	if q.paused {
		if q.bytes < outputLowWater {
			q.paused = false
		}
		return false, false
	}
	if q.bytes >= outputHighWater {
		q.paused = true
		return false, false
	}
	return true, false
}

// pop returns the next chunk, blocking until one is available or the
// queue is closed. It returns ok=false when closed and empty.
func (q *outputQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	cond := q.getCond()
	for len(q.chunks) == 0 && !q.closed {
		cond.Wait()
	}
	if len(q.chunks) > 0 {
		c := q.chunks[0]
		q.chunks[0] = nil
		q.chunks = q.chunks[1:]
		if len(q.chunks) == 0 {
			q.chunks = nil
		}
		q.bytes -= len(c)
		return c, true
	}
	return nil, false
}

// closeWithExit marks the queue closed with an exit code.
func (q *outputQueue) closeWithExit(code *int) {
	q.mu.Lock()
	cond := q.getCond()
	q.closed = true
	q.exitCode = code
	cond.Broadcast()
	q.mu.Unlock()
}

// closeAbnormal marks the queue closed without an exit code (no exit frame).
func (q *outputQueue) closeAbnormal() {
	q.mu.Lock()
	cond := q.getCond()
	q.closed = true
	cond.Broadcast()
	q.mu.Unlock()
}

// getExitCode returns the stored exit code.
func (q *outputQueue) getExitCode() *int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.exitCode
}

// attachment bridges one WebSocket to a pty running `tmux attach-session`.
type attachment struct {
	s       *server
	c       *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	session string

	ptmx *os.File
	cmd  *exec.Cmd

	// Guards the pty master's lifecycle. pty.Setsize goes through File.Fd(),
	// which bypasses os.File's internal refcounting, so an ioctl racing a
	// Close can be issued against an already-recycled descriptor — i.e. some
	// unrelated file. Every Setsize and the Close itself take this lock and
	// respect ptyClosed. The blocking Read in outputPump deliberately does NOT
	// take it (that would deadlock Close); a concurrent Read/Close on os.File
	// is safe on its own, since Read uses the refcounted path.
	ptyMu     sync.Mutex
	ptyClosed bool

	mu    sync.Mutex
	cols  int
	rows  int
	ready bool
	stage *ringbuffer.Buffer
	queue outputQueue

	once sync.Once
}

// parseSize parses cols/rows query params, falling back to 80x24 when absent or
// out of range. The client always sends measured dimensions; the fallback only
// applies to malformed or missing values.
func parseSize(colsStr, rowsStr string) (cols, rows int) {
	cols, rows = 80, 24
	if c, err := strconv.Atoi(colsStr); err == nil && c >= 1 && c <= maxDim {
		cols = c
	}
	if r, err := strconv.Atoi(rowsStr); err == nil && r >= 1 && r <= maxDim {
		rows = r
	}
	return cols, rows
}

// attach handles GET /ws/{hostId}/{session}. It accepts the WebSocket, redeems
// the single-use ticket, then bridges the pty.
func (s *server) attach(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("hostId") != "local" {
		writeError(w, http.StatusNotFound, "host_not_found")
		return
	}
	session := r.PathValue("session")

	// Select origin policy: an explicit configured origin takes precedence and
	// requires an exact match (originless clients allowed); the default empty
	// configuration relies on the WebSocket library's request-host matching.
	var acceptOpts *websocket.AcceptOptions
	if s.origin != "" {
		if !checkOrigin(r.Header.Get("Origin"), s.origin) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		// Application-level origin verification has completed successfully for
		// the explicitly configured origin (or an originless client). Skip the
		// library's duplicate Origin check so that requests behind a Host-rewriting
		// reverse proxy are accepted.
		acceptOpts = &websocket.AcceptOptions{
			InsecureSkipVerify: true,
		}
	}

	c, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		return
	}
	// Raise the 32 KiB default so an ordinary large paste is not treated as a
	// protocol violation. See maxInputMessage.
	c.SetReadLimit(maxInputMessage)

	// Use a background context: r.Context() is cancelled when this handler
	// returns (net/http cancels the request context after ServeHTTP), which
	// would immediately tear down the attachment we hand off to goroutines.
	ctx, cancel := context.WithCancel(context.Background())
	a := &attachment{
		s:       s,
		c:       c,
		ctx:     ctx,
		cancel:  cancel,
		session: session,
		stage:   ringbuffer.New(2 << 20), // 2 MB pre-ready staging
	}

	// Redeem the ticket first; refuse (retryable=false) on any failure.
	if err := s.tickets.redeem(r.URL.Query().Get("ticket"), session); err != nil {
		msg := "invalid ticket"
		switch {
		case errors.Is(err, ErrTicketExpired):
			msg = "ticket expired"
		case errors.Is(err, ErrTicketMismatch):
			msg = "ticket does not match session"
		}
		a.sendErrorAndClose(msg, false)
		return
	}

	cols, rows := parseSize(r.URL.Query().Get("cols"), r.URL.Query().Get("rows"))

	// Spawn `tmux attach-session -t =<name>` under a pty sized before the first
	// read, with secrets scrubbed from the child environment.
	if s.tmux.err != nil {
		a.sendErrorAndClose("tmux unavailable: "+s.tmux.err.Error(), true)
		return
	}
	cmd := s.tmux.attachCommand(session)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	if err != nil {
		a.sendErrorAndClose("attach failed: "+err.Error(), true)
		return
	}
	a.ptmx = ptmx
	a.cmd = cmd
	a.mu.Lock()
	a.cols, a.rows = cols, rows
	a.mu.Unlock()

	// Defensive for older tmux: window-size latest is already the default on
	// 3.7b. A non-zero exit must not fail the attachment. Done once per hub
	// run, NOT per attach: setting the global option repaints every tmux
	// client on the server, which would light up every background session's
	// activity indicator with output the user did not cause.
	s.pinWindowSizePolicy()

	// Start pumping output before sending ready, so output produced between
	// spawn and ready is staged (and later flushed) rather than lost.
	s.track(a)
	go a.outputPump()
	go a.writePump()
	go a.inputPump()

	// Send ready, then mark the attachment ready and flush any staged output.
	_ = a.sendText(readyMessage{Type: "ready", Session: session, Cols: cols, Rows: rows})
	a.markReadyAndFlush()

	// Force a repaint so a same-size attach still paints immediately.
	a.forceRepaint()
}

// setSize resizes the pty master, unless it is already closed. See ptyMu.
func (a *attachment) setSize(cols, rows int) {
	a.ptyMu.Lock()
	defer a.ptyMu.Unlock()
	if a.ptyClosed || a.ptmx == nil {
		return
	}
	_ = pty.Setsize(a.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// closePty closes the pty master exactly once, excluding concurrent Setsize
// calls so no ioctl can land on a recycled descriptor. See ptyMu.
func (a *attachment) closePty() {
	a.ptyMu.Lock()
	defer a.ptyMu.Unlock()
	if a.ptyClosed || a.ptmx == nil {
		return
	}
	a.ptyClosed = true
	_ = a.ptmx.Close()
}

// detach ends the attachment's hold on the session: it closes the pty master
// and signals the `tmux attach-session` child to exit.
//
// The Kill is not belt-and-braces, it is the load-bearing part. creack/pty
// opens the master with syscall.Open + os.NewFile, so the descriptor is not
// registered with the runtime netpoller; os.File.Close on such a descriptor
// cannot interrupt a Read that is already in flight (it only drops a
// reference, deferring the real close(2) until the parked Read returns) and
// still reports success. Since outputPump is parked in exactly that Read
// whenever the session is idle, closing alone leaves the child running and any
// subsequent cmd.Wait blocked forever. Signalling the child makes the slave
// side hang up, which returns the parked Read with EOF and lets it be reaped.
//
// This is not theoretical: on a silent session the parked Read never returns on
// its own, so the goroutine, the pty fd and the attached tmux client all leak
// for the hub's lifetime, and the ghost client keeps constraining the real
// user's window size (the size policy is "latest"). Incidental session output
// can happen to unpark the Read, which makes the leak timing-dependent and easy
// to mistake for correct behaviour. See TestDisconnectReleasesSilentSession.
//
// Killing the client process only detaches it; the session itself survives,
// which is what onClientGone requires.
func (a *attachment) detach() {
	a.closePty()
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
}

// sendText marshals and writes a JSON text frame, bounded by a 5-second timeout.
func (a *attachment) sendText(v any) error {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return a.sendTextWithContext(ctx, v)
}

// sendTextWithContext marshals and writes a JSON text frame with the given context.
func (a *attachment) sendTextWithContext(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return a.c.Write(ctx, websocket.MessageText, b)
}

// sendErrorAndClose sends an error text frame then closes with an appropriate
// code: 1012 (retryable, service restart) or 1008 (refused).
func (a *attachment) sendErrorAndClose(message string, retryable bool) {
	_ = a.sendText(errorMessage{Type: "error", Message: message, Retryable: retryable})
	code := websocket.StatusPolicyViolation
	if retryable {
		code = websocket.StatusServiceRestart
	}
	_ = a.c.Close(code, message)
}

// markReadyAndFlush atomically transitions from staging to live and flushes any
// staged output, preserving production order. The flush happens while a.mu is
// still held: releasing it first would let a concurrent deliver() observe
// ready==true and push fresh pty output onto the queue ahead of the older
// staged chunks, reordering the byte stream and splitting escape sequences.
func (a *attachment) markReadyAndFlush() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ready = true
	for _, chunk := range a.stage.Drain() {
		a.queue.push(chunk)
	}
}

// deliver routes a pty read to either the pre-ready staging buffer or the live
// write queue.
func (a *attachment) deliver(b []byte) {
	a.mu.Lock()
	if !a.ready {
		a.stage.Write(b)
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.queue.push(b)
}

// outputPump reads the pty and forwards bytes. It never transforms them: bytes
// read from the pty are written to the client exactly as produced.
func (a *attachment) outputPump() {
	buf := make([]byte, 32*1024)
	for {
		// Backpressure: pause reading the pty while the writer is behind. This
		// lets the kernel's pty buffer fill and tmux block on write, rather
		// than buffering output without bound in the hub. A closed queue is
		// terminal: the client is gone (or the hub is shutting down), so stop
		// pumping instead of spinning forever on a queue that never drains.
		read, closed := a.queue.shouldRead()
		if closed {
			// The queue closed under us (client gone, or hub shutting down).
			// detach() before reaping: waitChild blocks until the child exits,
			// and closing the pty alone does not make that happen (see detach).
			// Both are idempotent, so this is a no-op when the closer did it.
			a.detach()
			a.waitChild()
			return
		}
		if !read {
			time.Sleep(pollInterval)
			continue
		}

		n, err := a.ptmx.Read(buf)
		if n > 0 {
			a.deliver(buf[:n])
		}
		if err != nil {
			a.onOutputEnd()
			return
		}
	}
}

// onOutputEnd runs when the pty reaches EOF (session ended) or the master was
// closed. It reaps the child and tells the writer to send the exit frame last.
func (a *attachment) onOutputEnd() {
	a.closePty()
	code := a.waitChild()
	a.queue.closeWithExit(code)
}

// waitChild reaps the attach process and returns its exit code (nil if it was
// killed by a signal).
//
// Both call sites (onOutputEnd and outputPump's closed-queue branch) live in
// the outputPump goroutine and each returns immediately afterwards, so Wait is
// never called twice. That invariant is load-bearing: os/exec's Wait is not
// safe to call concurrently (it carries a known PID-reuse hazard), and a second
// sequential call would return a bare "Wait was already called" error, which is
// not an *exec.ExitError and so would be reported here as an unknown exit code.
func (a *attachment) waitChild() *int {
	if a.cmd == nil {
		return nil
	}
	err := a.cmd.Wait()
	if err == nil {
		code := 0
		return &code
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		return &code
	}
	return nil
}

// writePump drains the queue to the WebSocket as binary frames, and sends the
// exit frame (if any) once the queue is drained, preserving order.
func (a *attachment) writePump() {
	defer a.finish()
	for {
		chunk, ok := a.queue.pop()
		if !ok {
			// closed and drained
			if code := a.queue.getExitCode(); code != nil {
				writeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = a.sendTextWithContext(writeCtx, exitMessage{Type: "exit", Code: code})
				cancel()
			}
			_ = a.c.Close(websocket.StatusNormalClosure, "")
			return
		}
		writeCtx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		err := a.c.Write(writeCtx, websocket.MessageBinary, chunk)
		cancel()
		if err != nil {
			// The client is unreachable, so detach: this releases the tmux
			// client instead of leaving it attached for the hub's lifetime,
			// and unblocks outputPump so it can reap the child. Close the
			// WebSocket immediately.
			a.queue.closeAbnormal()
			a.detach()
			_ = a.c.CloseNow()
			return
		}
	}
}

// inputPump reads client frames: binary → pty write verbatim; text → control.
func (a *attachment) inputPump() {
	for {
		msgType, data, err := a.c.Read(a.ctx)
		if err != nil {
			a.onClientGone()
			return
		}
		switch msgType {
		case websocket.MessageBinary:
			a.writeInput(data)
		case websocket.MessageText:
			a.handleControl(data)
		}
	}
}

// writeInput feeds client bytes to the pty in bounded chunks. os.File.Write
// blocks until the tty accepts everything, which is the backpressure we want —
// a big paste is throttled by the terminal, not buffered by the hub — but a
// single multi-MiB write would sit in one syscall for the whole drain. Chunking
// bounds each blocking call and lets teardown be observed in between. A write
// error means the pty is gone, so there is nothing left to feed.
func (a *attachment) writeInput(data []byte) {
	for len(data) > 0 {
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		n := len(data)
		if n > inputChunk {
			n = inputChunk
		}
		if _, err := a.ptmx.Write(data[:n]); err != nil {
			return
		}
		data = data[n:]
	}
}

// handleControl dispatches a JSON control frame from the client.
func (a *attachment) handleControl(data []byte) {
	var msg clientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	switch msg.Type {
	case "resize":
		a.applyResize(msg.Cols, msg.Rows)
	case "ping":
		_ = a.sendText(pongMessage{Type: "pong"})
	}
}

// applyResize validates and applies a new size, retaining the last valid size
// when the new one is out of range.
func (a *attachment) applyResize(cols, rows int) {
	if cols < 1 || cols > maxDim || rows < 1 || rows > maxDim {
		return // retain last valid size
	}
	a.setSize(cols, rows)
	a.mu.Lock()
	a.cols, a.rows = cols, rows
	a.mu.Unlock()
	var pid int
	if a.cmd != nil && a.cmd.Process != nil {
		pid = a.cmd.Process.Pid
	}
	a.s.tmux.refreshSessionSize(a.session, pid, cols, rows)
}

// forceRepaint applies rows-1 then rows in quick succession so tmux emits a
// redraw. If the size applied at attach already equals the size tmux has for
// the session (a reattach to unchanged dimensions), TIOCSWINSZ fires no
// SIGWINCH and tmux does not redraw, leaving a blank screen until the next
// output. The dummy shrink-then-restore guarantees a repaint.
func (a *attachment) forceRepaint() {
	a.mu.Lock()
	cols, rows := a.cols, a.rows
	a.mu.Unlock()
	if rows < 2 {
		return
	}
	a.setSize(cols, rows-1)
	a.setSize(cols, rows)
}

// onClientGone runs when the WebSocket read fails (client disconnected). It
// detaches so tmux releases the client; the session itself survives.
func (a *attachment) onClientGone() {
	a.queue.closeAbnormal()
	a.cancel()
	a.detach()
	a.finish()
}

// shutdown terminates the attachment (used on hub shutdown): detach the pty
// and attach process, and close the WebSocket.
func (a *attachment) shutdown() {
	a.queue.closeAbnormal()
	a.cancel()
	a.detach()
	_ = a.c.Close(websocket.StatusNormalClosure, "hub shutting down")
	a.finish()
}

// finish cancels the attachment context and untracks it from the server,
// exactly once.
func (a *attachment) finish() {
	a.once.Do(func() {
		a.cancel()
		a.s.untrack(a)
	})
}
