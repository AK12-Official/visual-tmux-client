package terminal

import "sync"

// OutputQueue is a FIFO chunk buffer providing backpressure hysteresis between
// the process reader and peer writer.
type OutputQueue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	chunks    [][]byte
	bytes     int
	paused    bool
	closed    bool
	exitCode  *int
	highWater int
	lowWater  int
}

// NewOutputQueue creates an OutputQueue with explicit high and low watermarks.
func NewOutputQueue(highWater, lowWater int) *OutputQueue {
	q := &OutputQueue{
		highWater: highWater,
		lowWater:  lowWater,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Push enqueues a chunk and signals waiting readers.
func (q *OutputQueue) Push(b []byte) {
	if len(b) == 0 {
		return
	}
	q.mu.Lock()
	q.chunks = append(q.chunks, append([]byte(nil), b...))
	q.bytes += len(b)
	q.cond.Signal()
	q.mu.Unlock()
}

// ShouldRead reports whether reading from the process should continue.
// It pauses above highWater until bytes drain below lowWater.
func (q *OutputQueue) ShouldRead() (read, closed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false, true
	}
	if q.paused {
		if q.bytes < q.lowWater {
			q.paused = false
			return true, false
		}
		return false, false
	}
	if q.bytes >= q.highWater {
		q.paused = true
		return false, false
	}
	return true, false
}

// Pop retrieves the next chunk, blocking until data arrives or the queue closes.
func (q *OutputQueue) Pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
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

// CloseWithExit marks the queue closed with an exit code.
func (q *OutputQueue) CloseWithExit(code *int) {
	q.mu.Lock()
	q.closed = true
	q.exitCode = code
	q.cond.Broadcast()
	q.mu.Unlock()
}

// CloseAbnormal marks the queue closed without sending an exit frame.
func (q *OutputQueue) CloseAbnormal() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

// GetExitCode returns the stored exit code if set.
func (q *OutputQueue) GetExitCode() *int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.exitCode
}

// Bytes returns the total unconsumed bytes in the queue.
func (q *OutputQueue) Bytes() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}
