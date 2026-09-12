// Package ringbuffer implements a bounded, thread-safe FIFO of byte chunks.
//
// Its role changed with the browser-session-manager rewrite: it is no longer a
// per-pane scrollback of raw bytes, but the staging buffer for terminal output
// produced between pty spawn and the client being "ready". The element type is
// therefore a whole []byte chunk rather than a flat byte array, and eviction is
// whole-chunk rather than byte-granular.
package ringbuffer

import "sync"

// Buffer is a bounded FIFO of []byte chunks with a byte-size cap. When a write
// would exceed the cap, the oldest whole chunks are evicted — never a partial
// slice of a chunk — so no escape sequence or multi-byte character is ever cut
// in half (the corruption this rewrite exists to fix).
type Buffer struct {
	mu     sync.Mutex
	chunks [][]byte
	size   int // total bytes currently held
	cap    int // maximum total bytes
}

// New returns an empty Buffer with the given byte capacity. Capacity must be
// positive.
func New(capacity int) *Buffer {
	if capacity <= 0 {
		panic("ringbuffer: capacity must be positive")
	}
	return &Buffer{cap: capacity}
}

// Cap returns the buffer's byte capacity.
func (b *Buffer) Cap() int {
	return b.cap
}

// Write appends p as a single chunk (a copy), evicting the oldest whole chunks
// if needed to stay within capacity. It always reports len(p) written and a nil
// error, satisfying io.Writer. A single chunk larger than the capacity is
// retained whole as the only chunk: the cap is a bound on the common case, and
// dropping or splitting an oversized chunk would corrupt the byte stream.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	chunk := append([]byte(nil), p...)
	for b.size+len(chunk) > b.cap && len(b.chunks) > 0 {
		b.size -= len(b.chunks[0])
		b.chunks[0] = nil
		b.chunks = b.chunks[1:]
	}
	if len(b.chunks) == 0 {
		b.chunks = nil
	}
	b.chunks = append(b.chunks, chunk)
	b.size += len(chunk)
	return len(p), nil
}

// Drain returns all currently-held chunks in order (oldest first) and empties
// the buffer. The caller takes ownership of the returned slices.
func (b *Buffer) Drain() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.chunks
	b.chunks = nil
	b.size = 0
	return out
}

// Len returns the number of chunks currently held.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.chunks)
}

// Size returns the total number of bytes currently held.
func (b *Buffer) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}
