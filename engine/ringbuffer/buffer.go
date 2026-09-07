// Package ringbuffer implements a bounded, thread-safe byte ring buffer
// used to retain the most recently written bytes per pane (for reconnect
// recovery and, later, cross-pane search), per design.md's decision to keep
// raw bytes rather than run headless terminal emulation in the engine.
package ringbuffer

import "sync"

// Buffer is a fixed-capacity ring buffer of bytes. Once full, writing
// discards the oldest bytes to make room for the newest, so it always holds
// at most Cap() bytes: the most recently written ones.
type Buffer struct {
	mu   sync.Mutex
	data []byte
	cap  int
}

// New returns an empty Buffer with the given capacity in bytes. Capacity
// must be positive.
func New(capacity int) *Buffer {
	if capacity <= 0 {
		panic("ringbuffer: capacity must be positive")
	}
	return &Buffer{cap: capacity}
}

// Cap returns the buffer's fixed capacity in bytes.
func (b *Buffer) Cap() int {
	return b.cap
}

// Write appends p to the buffer, discarding the oldest bytes if the
// combined length would exceed the buffer's capacity. It always succeeds
// and never returns an error, satisfying io.Writer's contract trivially.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(p) >= b.cap {
		// The new write alone fills or exceeds capacity: keep only its
		// tail and drop everything previously buffered.
		b.data = append([]byte(nil), p[len(p)-b.cap:]...)
		return len(p), nil
	}

	combined := append(b.data, p...)
	if len(combined) > b.cap {
		combined = combined[len(combined)-b.cap:]
	}
	b.data = combined
	return len(p), nil
}

// Bytes returns a copy of the buffer's current contents, oldest byte first.
func (b *Buffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

// Len returns the number of bytes currently held, which is at most Cap().
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.data)
}
