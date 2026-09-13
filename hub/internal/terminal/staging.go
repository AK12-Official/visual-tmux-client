package terminal

import "sync"

// StagingBuffer is a thread-safe, chunk-evicting ring buffer for pre-ready terminal output.
type StagingBuffer struct {
	mu     sync.Mutex
	chunks [][]byte
	size   int
	cap    int
}

const defaultStagingCapacity = 2 * 1024 * 1024 // 2 MiB fallback

// NewStagingBuffer constructs a StagingBuffer with the given maximum byte capacity.
func NewStagingBuffer(capacity int) *StagingBuffer {
	if capacity <= 0 {
		capacity = defaultStagingCapacity
	}
	return &StagingBuffer{cap: capacity}
}

// Cap returns the buffer's byte capacity.
func (b *StagingBuffer) Cap() int {
	return b.cap
}

// Write appends p as an atomic chunk, evicting the oldest whole chunks if needed.
func (b *StagingBuffer) Write(p []byte) (int, error) {
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

// Drain atomically returns all chunks in order (oldest first) and clears the buffer.
func (b *StagingBuffer) Drain() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.chunks
	b.chunks = nil
	b.size = 0
	return out
}

// Len returns the number of chunks currently held.
func (b *StagingBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.chunks)
}

// Size returns the total bytes held in the buffer.
func (b *StagingBuffer) Size() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}
