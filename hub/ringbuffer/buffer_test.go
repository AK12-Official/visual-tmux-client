package ringbuffer

import (
	"bytes"
	"testing"
)

func TestBufferWriteWithinCapacity(t *testing.T) {
	b := New(10)
	b.Write([]byte("hello"))
	chunks := b.Drain()
	if len(chunks) != 1 || !bytes.Equal(chunks[0], []byte("hello")) {
		t.Fatalf("expected single chunk %q, got %q", "hello", chunks)
	}
	if b.Size() != 0 || b.Len() != 0 {
		t.Fatalf("expected drained buffer to be empty")
	}
}

func TestBufferEvictsWholeChunks(t *testing.T) {
	b := New(5)
	b.Write([]byte("abc")) // 3 bytes
	b.Write([]byte("def")) // 6 bytes total > 5: evict "abc" (whole)
	chunks := b.Drain()
	if len(chunks) != 1 || !bytes.Equal(chunks[0], []byte("def")) {
		t.Fatalf("expected [%q], got %q", "def", chunks)
	}
}

// TestBufferNeverSplitsChunks is the core regression: chunks totaling more than
// the cap must be evicted whole, never partially sliced.
func TestBufferNeverSplitsChunks(t *testing.T) {
	b := New(10)
	writes := [][]byte{
		[]byte("aaaa"),
		[]byte("bbbb"),
		[]byte("cccc"),
		[]byte("dddd"),
	}
	for _, w := range writes {
		b.Write(w)
	}
	chunks := b.Drain()
	for _, c := range chunks {
		if !contains(writes, c) {
			t.Fatalf("retained chunk %q is a partial slice, not a whole written chunk", c)
		}
	}
	if b.Size() != 0 {
		t.Fatalf("expected drained size 0, got %d", b.Size())
	}
}

func contains(haystack [][]byte, needle []byte) bool {
	for _, h := range haystack {
		if bytes.Equal(h, needle) {
			return true
		}
	}
	return false
}

func TestBufferDrainReturnsInOrder(t *testing.T) {
	b := New(100)
	for _, s := range []string{"a", "b", "c"} {
		b.Write([]byte(s))
	}
	chunks := b.Drain()
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for i, want := range []string{"a", "b", "c"} {
		if string(chunks[i]) != want {
			t.Fatalf("chunk %d: expected %q, got %q", i, want, chunks[i])
		}
	}
}

func TestBufferOversizedChunkRetainedWhole(t *testing.T) {
	b := New(4)
	big := []byte("abcdefgh")
	b.Write(big)
	chunks := b.Drain()
	if len(chunks) != 1 || !bytes.Equal(chunks[0], big) {
		t.Fatalf("expected oversized chunk retained whole, got %q", chunks)
	}
}

func TestBufferEmptyInitially(t *testing.T) {
	b := New(10)
	if b.Len() != 0 || b.Size() != 0 {
		t.Fatalf("expected empty buffer")
	}
	if got := b.Drain(); len(got) != 0 {
		t.Fatalf("expected empty drain, got %q", got)
	}
}
