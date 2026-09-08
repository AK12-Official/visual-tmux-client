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
	chunks   [][]byte
	bytes    int
	paused   bool
	closed   bool
	exitCode *int // set on pty EOF so the writer can send the exit frame last
}

func (q *outputQueue) push(b []byte) {
	if len(b) == 0 {
		return
	}
	q.mu.Lock()
	q.chunks = append(q.chunks, append([]byte(nil), b...))
	q.bytes += len(b)
	q.mu.Unlock()
}

// shouldRead is the backpressure gate: it reports whether the output pump may
// read more from the pty. Reading is paused once the queued byte count reaches
// the high watermark, and does not resume until it drains below the low
// watermark (hysteresis, to avoid thrashing at the boundary).
func (q *outputQueue) shouldRead() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if q.paused {
		if q.bytes < outputLowWater {
			q.paused = false
		}
		return false
	}
	if q.bytes >= outputHighWater {
		q.paused = true
		return false
	}
	return true
}

// pop returns the next chunk, blocking (polling) until one is available or the
// queue is closed. It returns ok=false when closed and empty.
func (q *outputQueue) pop() ([]byte, bool) {
	for {
		q.mu.Lock()
		if len(q.chunks) > 0 {
			c := q.chunks[0]
			q.chunks = q.chunks[1:]
			q.bytes -= len(c)
			q.mu.Unlock()
			return c, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, false
		}
		time.Sleep(pollInterval)
	}
}

// closeWithExit marks the queue closed with an exit code.
func (q *outputQueue) closeWithExit(code *int) {
	q.mu.Lock()
	q.closed = true
	q.exitCode = code
	q.mu.Unlock()
}

// closeAbnormal marks the queue closed without an exit code (no exit frame).
func (q *outputQueue) closeAbnormal() {
	q.mu.Lock()
	q.closed = true
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

	// Origin is validated before any pty is spawned.
	if !checkOrigin(r.Header.Get("Origin"), s.origin) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

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
	// 3.7b. A non-zero exit must not fail the attachment.
	_, _, _, _ = s.tmux.exec("set-option", "-g", "window-size", "latest")

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

// sendText marshals and writes a JSON text frame.
func (a *attachment) sendText(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return a.c.Write(a.ctx, websocket.MessageText, b)
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
// staged output, preserving production order.
func (a *attachment) markReadyAndFlush() {
	a.mu.Lock()
	a.ready = true
	staged := a.stage.Drain()
	a.mu.Unlock()
	for _, chunk := range staged {
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
		// than buffering output without bound in the hub.
		if !a.queue.shouldRead() {
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
	code := a.waitChild()
	a.queue.closeWithExit(code)
}

// waitChild reaps the attach process and returns its exit code (nil if it was
// killed by a signal).
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
	for {
		chunk, ok := a.queue.pop()
		if !ok {
			// closed and drained
			if code := a.queue.getExitCode(); code != nil {
				_ = a.sendText(exitMessage{Type: "exit", Code: code})
			}
			_ = a.c.Close(websocket.StatusNormalClosure, "")
			a.finish()
			return
		}
		if err := a.c.Write(a.ctx, websocket.MessageBinary, chunk); err != nil {
			a.queue.closeAbnormal()
			a.finish()
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
			_, _ = a.ptmx.Write(data)
		case websocket.MessageText:
			a.handleControl(data)
		}
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
	_ = pty.Setsize(a.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
	a.mu.Lock()
	a.cols, a.rows = cols, rows
	a.mu.Unlock()
	a.s.tmux.refreshSessionSize(a.session, cols, rows)
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
	_ = pty.Setsize(a.ptmx, &pty.Winsize{Rows: uint16(rows - 1), Cols: uint16(cols)})
	_ = pty.Setsize(a.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// onClientGone runs when the WebSocket read fails (client disconnected). It
// closes the pty so tmux detaches the client; the session itself survives.
func (a *attachment) onClientGone() {
	a.queue.closeAbnormal()
	a.cancel()
	if a.ptmx != nil {
		_ = a.ptmx.Close()
	}
	a.finish()
}

// shutdown terminates the attachment (used on hub shutdown): close the pty,
// kill the attach process, and close the WebSocket.
func (a *attachment) shutdown() {
	a.queue.closeAbnormal()
	a.cancel()
	if a.ptmx != nil {
		_ = a.ptmx.Close()
	}
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
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
