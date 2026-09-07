package ringbuffer

import (
	"bytes"
	"testing"
)

func TestBuffer_WriteWithinCapacity(t *testing.T) {
	b := New(10)
	b.Write([]byte("hello"))

	if got := b.Bytes(); !bytes.Equal(got, []byte("hello")) {
		t.Errorf("expected %q, got %q", "hello", got)
	}
	if b.Len() != 5 {
		t.Errorf("expected Len() 5, got %d", b.Len())
	}
}

func TestBuffer_WriteBeyondCapacityDiscardsOldest(t *testing.T) {
	b := New(5)
	b.Write([]byte("abc"))
	b.Write([]byte("def"))

	// "abcdef" is 6 bytes, capacity is 5: expect the oldest byte ('a')
	// discarded, retaining "bcdef".
	if got := b.Bytes(); !bytes.Equal(got, []byte("bcdef")) {
		t.Errorf("expected %q, got %q", "bcdef", got)
	}
	if b.Len() != 5 {
		t.Errorf("expected Len() to stay at capacity 5, got %d", b.Len())
	}
}

func TestBuffer_SingleWriteLargerThanCapacityKeepsTail(t *testing.T) {
	b := New(4)
	b.Write([]byte("abcdefgh"))

	if got := b.Bytes(); !bytes.Equal(got, []byte("efgh")) {
		t.Errorf("expected tail %q, got %q", "efgh", got)
	}
}

func TestBuffer_ManySmallWritesRetainMostRecentBound(t *testing.T) {
	b := New(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		b.Write([]byte(s))
	}

	if got := b.Bytes(); !bytes.Equal(got, []byte("cde")) {
		t.Errorf("expected most recent 3 bytes %q, got %q", "cde", got)
	}
}

func TestBuffer_EmptyInitially(t *testing.T) {
	b := New(10)
	if b.Len() != 0 {
		t.Errorf("expected empty buffer to have Len() 0, got %d", b.Len())
	}
	if got := b.Bytes(); len(got) != 0 {
		t.Errorf("expected empty buffer to return empty Bytes(), got %q", got)
	}
}
