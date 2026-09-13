package terminal

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Standard WebSocket close status codes.
const (
	StatusNormalClosure = 1000
)

// Message structures for client-server terminal protocol.
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

type pongMessage struct {
	Type string `json:"type"`
}

type clientMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Attachment coordinates PTY process I/O and peer communication for a single terminal session.
type Attachment struct {
	proc     Process
	peer     Peer
	opts     Options
	session  string
	untrack  func(*Attachment)
	onResize func(cols, rows int)

	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	cols  int
	rows  int
	ready bool

	stage *StagingBuffer
	queue *OutputQueue

	once sync.Once
}

// NewAttachment constructs a new Attachment instance.
func NewAttachment(
	proc Process,
	peer Peer,
	opts Options,
	session string,
	cols, rows int,
	untrack func(*Attachment),
	onResize func(cols, rows int),
) *Attachment {
	ctx, cancel := context.WithCancel(context.Background())
	return &Attachment{
		proc:     proc,
		peer:     peer,
		opts:     opts,
		session:  session,
		cols:     cols,
		rows:     rows,
		untrack:  untrack,
		onResize: onResize,
		ctx:      ctx,
		cancel:   cancel,
		stage:    NewStagingBuffer(opts.StagingBufferBytes),
		queue:    NewOutputQueue(opts.OutputHighWaterBytes, opts.OutputLowWaterBytes),
	}
}

// Start initiates the pumping goroutines, sends the ready frame, flushes staged data, and triggers repaint.
func (a *Attachment) Start() {
	go a.outputPump()
	go a.writePump()
	go a.inputPump()

	writeCtx, cancel := context.WithTimeout(a.ctx, a.opts.ControlWriteTimeout)
	_ = a.peer.SendText(writeCtx, readyMessage{ //nolint:errcheck // Best effort ready notification
		Type:    "ready",
		Session: a.session,
		Cols:    a.cols,
		Rows:    a.rows,
	})
	cancel()

	a.markReadyAndFlush()
	a.forceRepaint()
}

func (a *Attachment) markReadyAndFlush() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ready = true
	for _, chunk := range a.stage.Drain() {
		a.queue.Push(chunk)
	}
}

func (a *Attachment) deliver(b []byte) {
	a.mu.Lock()
	if !a.ready {
		_, _ = a.stage.Write(b) //nolint:errcheck // StagingBuffer.Write never errors
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	a.queue.Push(b)
}

func (a *Attachment) outputPump() {
	readChunkSize := a.opts.InputChunkSize
	if readChunkSize <= 0 {
		readChunkSize = 32768
	}
	buf := make([]byte, readChunkSize)

	for {
		select {
		case <-a.ctx.Done():
			_ = a.proc.Detach()  //nolint:errcheck // Best-effort process detach
			_, _ = a.proc.Wait() //nolint:errcheck // Best-effort process wait
			return
		default:
		}

		read, closed := a.queue.ShouldRead()
		if closed {
			_ = a.proc.Detach()  //nolint:errcheck // Best-effort process detach
			_, _ = a.proc.Wait() //nolint:errcheck // Best-effort process wait
			return
		}
		if !read {
			timer := time.NewTimer(a.opts.BackpressurePollInterval)
			select {
			case <-a.ctx.Done():
				timer.Stop()
				_ = a.proc.Detach()  //nolint:errcheck // Best-effort process detach
				_, _ = a.proc.Wait() //nolint:errcheck // Best-effort process wait
				return
			case <-timer.C:
				continue
			}
		}

		n, err := a.proc.Read(buf)
		if n > 0 {
			a.deliver(buf[:n])
		}
		if err != nil {
			a.onOutputEnd()
			return
		}
	}
}

func (a *Attachment) onOutputEnd() {
	_ = a.proc.Close()       //nolint:errcheck // Process closed on EOF
	code, _ := a.proc.Wait() //nolint:errcheck // Exit code extracted
	a.queue.CloseWithExit(code)
}

func (a *Attachment) writePump() {
	defer a.finish()
	for {
		chunk, ok := a.queue.Pop()
		if !ok {
			// Closed and drained
			if code := a.queue.GetExitCode(); code != nil {
				writeCtx, cancel := context.WithTimeout(context.Background(), a.opts.ExitWriteTimeout)
				_ = a.peer.SendText(writeCtx, exitMessage{Type: "exit", Code: code}) //nolint:errcheck // Best effort
				cancel()
			}
			_ = a.peer.Close(StatusNormalClosure, "") //nolint:errcheck // Normal closure
			return
		}

		writeCtx, cancel := context.WithTimeout(a.ctx, a.opts.OutputWriteTimeout)
		err := a.peer.SendBinary(writeCtx, chunk)
		cancel()
		if err != nil {
			a.queue.CloseAbnormal()
			_ = a.proc.Detach()   //nolint:errcheck // Detach on write failure
			_ = a.peer.CloseNow() //nolint:errcheck // Force close peer
			return
		}
	}
}

func (a *Attachment) inputPump() {
	for {
		msgType, data, err := a.peer.ReadMessage(a.ctx)
		if err != nil {
			a.onClientGone()
			return
		}
		const msgTypeBinary = 2
		const msgTypeText = 1
		switch msgType {
		case msgTypeBinary:
			a.writeInput(data)
		case msgTypeText:
			a.handleControl(data)
		}
	}
}

func (a *Attachment) writeInput(data []byte) {
	chunkSize := a.opts.InputChunkSize
	if chunkSize <= 0 {
		chunkSize = 32768
	}
	for len(data) > 0 {
		select {
		case <-a.ctx.Done():
			return
		default:
		}
		n := len(data)
		if n > chunkSize {
			n = chunkSize
		}
		if _, err := a.proc.Write(data[:n]); err != nil {
			return
		}
		data = data[n:]
	}
}

func (a *Attachment) handleControl(data []byte) {
	var msg clientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	switch msg.Type {
	case "resize":
		a.applyResize(msg.Cols, msg.Rows)
	case "ping":
		ctx, cancel := context.WithTimeout(a.ctx, a.opts.ControlWriteTimeout)
		_ = a.peer.SendText(ctx, pongMessage{Type: "pong"}) //nolint:errcheck // Best effort pong
		cancel()
	}
}

func (a *Attachment) applyResize(cols, rows int) {
	if cols < 1 || cols > a.opts.MaxDimension || rows < 1 || rows > a.opts.MaxDimension {
		return
	}
	_ = a.proc.Resize(cols, rows) //nolint:errcheck // Best effort resize
	a.mu.Lock()
	a.cols, a.rows = cols, rows
	a.mu.Unlock()

	if a.onResize != nil {
		a.onResize(cols, rows)
	}
}

func (a *Attachment) forceRepaint() {
	a.mu.Lock()
	cols, rows := a.cols, a.rows
	a.mu.Unlock()
	const minRepaintRows = 2
	if rows < minRepaintRows {
		return
	}
	_ = a.proc.Resize(cols, rows-1) //nolint:errcheck // Repaint cycle
	_ = a.proc.Resize(cols, rows)   //nolint:errcheck // Repaint cycle
}

func (a *Attachment) onClientGone() {
	a.queue.CloseAbnormal()
	a.cancel()
	_ = a.proc.Detach() //nolint:errcheck // Detach on client gone
	a.finish()
}

// Shutdown gracefully stops the attachment.
func (a *Attachment) Shutdown() {
	a.queue.CloseAbnormal()
	a.cancel()
	_ = a.proc.Detach()                                        //nolint:errcheck // Detach on shutdown
	_ = a.peer.Close(StatusNormalClosure, "hub shutting down") //nolint:errcheck // Close on shutdown
	a.finish()
}

// ForceClose forcefully terminates peer and process.
func (a *Attachment) ForceClose() error {
	a.queue.CloseAbnormal()
	a.cancel()
	_ = a.proc.Detach() //nolint:errcheck // Detach on force close
	err := a.peer.CloseNow()
	a.finish()
	return err
}

func (a *Attachment) finish() {
	a.once.Do(func() {
		a.cancel()
		if a.untrack != nil {
			a.untrack(a)
		}
	})
}
