package terminal

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStagingBufferEvictionAndDrain(t *testing.T) {
	// Buffer with 10 bytes capacity
	buf := NewStagingBuffer(10)

	// Write 4 bytes
	if _, err := buf.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if buf.Size() != 4 || buf.Len() != 1 {
		t.Errorf("expected size 4 and len 1, got %d and %d", buf.Size(), buf.Len())
	}

	// Write 4 bytes
	if _, err := buf.Write([]byte("efgh")); err != nil {
		t.Fatal(err)
	}
	if buf.Size() != 8 || buf.Len() != 2 {
		t.Errorf("expected size 8 and len 2, got %d and %d", buf.Size(), buf.Len())
	}

	// Write 4 bytes: exceeds capacity (8+4 = 12 > 10). Oldest whole chunk "abcd" must be evicted
	if _, err := buf.Write([]byte("ijkl")); err != nil {
		t.Fatal(err)
	}
	if buf.Size() != 8 || buf.Len() != 2 {
		t.Errorf("expected size 8 and len 2 after eviction, got %d and %d", buf.Size(), buf.Len())
	}

	chunks := buf.Drain()
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if string(chunks[0]) != "efgh" || string(chunks[1]) != "ijkl" {
		t.Errorf("unexpected chunks content: %q, %q", string(chunks[0]), string(chunks[1]))
	}

	if buf.Size() != 0 || buf.Len() != 0 {
		t.Errorf("expected empty buffer after Drain, got size %d, len %d", buf.Size(), buf.Len())
	}
}

func TestOutputQueueBackpressure(t *testing.T) {
	const highWater = 100
	const lowWater = 30
	q := NewOutputQueue(highWater, lowWater)

	read, closed := q.ShouldRead()
	if !read || closed {
		t.Fatalf("expected initial state to allow reading, got read=%v closed=%v", read, closed)
	}

	// Push 90 bytes (below highWater)
	q.Push(make([]byte, 90))
	read, _ = q.ShouldRead()
	if !read {
		t.Fatalf("expected read=true below high water")
	}

	// Push 20 bytes (total 110 >= highWater)
	q.Push(make([]byte, 20))
	read, _ = q.ShouldRead()
	if read {
		t.Fatalf("expected read=false when reaching high water")
	}

	// Pop 90 bytes (leaving 20 < lowWater)
	_, ok := q.Pop()
	if !ok {
		t.Fatalf("Pop failed")
	}

	read, _ = q.ShouldRead()
	if !read {
		t.Fatalf("expected read=true after draining below low water")
	}

	// Close with exit
	code := 0
	q.CloseWithExit(&code)
	_, closed = q.ShouldRead()
	if !closed {
		t.Fatalf("expected closed=true")
	}
}

type fakePeer struct {
	mu           sync.Mutex
	events       []string
	sentBinary   [][]byte
	closed       bool
	closeCode    int
	closeReason  string
	closeNowDone bool
	closeCh      chan struct{}
}

func newFakePeer() *fakePeer {
	return &fakePeer{
		closeCh: make(chan struct{}),
	}
}

func (f *fakePeer) SendText(ctx context.Context, v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch msg := v.(type) {
	case readyMessage:
		f.events = append(f.events, "ready:"+msg.Session)
	case exitMessage:
		f.events = append(f.events, "exit")
	case pongMessage:
		f.events = append(f.events, "pong")
	}
	return nil
}

func (f *fakePeer) SendBinary(ctx context.Context, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, "binary:"+string(data))
	f.sentBinary = append(f.sentBinary, append([]byte(nil), data...))
	return nil
}

func (f *fakePeer) ReadMessage(ctx context.Context) (int, []byte, error) {
	select {
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	case <-f.closeCh:
		return 0, nil, errors.New("closed")
	}
}

func (f *fakePeer) Close(code int, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.closeCode = code
		f.closeReason = reason
		f.events = append(f.events, "close")
		if f.closeCh != nil {
			close(f.closeCh)
		}
	}
	return nil
}

func (f *fakePeer) CloseNow() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		if f.closeCh != nil {
			close(f.closeCh)
		}
	}
	f.closeNowDone = true
	f.events = append(f.events, "closeNow")
	return nil
}

type fakeProcess struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	readCh chan []byte
	waitCh chan struct{}
	closed bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{
		readCh: make(chan []byte, 10),
		waitCh: make(chan struct{}),
	}
}

func (p *fakeProcess) Read(b []byte) (int, error) {
	chunk, ok := <-p.readCh
	if !ok {
		return 0, errors.New("EOF")
	}
	n := copy(b, chunk)
	return n, nil
}

func (p *fakeProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Write(b)
}

func (p *fakeProcess) Resize(cols, rows int) error {
	return nil
}

func (p *fakeProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.readCh)
	}
	return nil
}

func (p *fakeProcess) Detach() error {
	_ = p.Close() //nolint:errcheck // Test stub detach
	return nil
}

func (p *fakeProcess) Wait() (*int, error) {
	code := 0
	return &code, nil
}

func TestManagerShutdownAndRace(t *testing.T) {
	mgr := NewManager(100 * time.Millisecond)

	opts := DefaultOptions()
	peer := newFakePeer()
	proc := newFakeProcess()

	att := NewAttachment(proc, peer, opts, "sess-1", 80, 24, mgr.Untrack, nil)
	if err := mgr.Register(att); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if mgr.ActiveCount() != 1 {
		t.Errorf("expected 1 active attachment, got %d", mgr.ActiveCount())
	}

	// Trigger shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = mgr.Shutdown(ctx) //nolint:errcheck // Test race shutdown
	}()

	// Concurrent register must fail with ErrManagerClosing
	for i := 0; i < 10; i++ {
		att2 := NewAttachment(proc, peer, opts, "sess-race", 80, 24, mgr.Untrack, nil)
		_ = mgr.Register(att2) //nolint:errcheck // Test race register
	}
	wg.Wait()

	// Subsequent register MUST fail
	att3 := NewAttachment(proc, peer, opts, "sess-post", 80, 24, mgr.Untrack, nil)
	if err := mgr.Register(att3); !errors.Is(err, ErrManagerClosing) {
		t.Errorf("expected ErrManagerClosing after shutdown, got: %v", err)
	}
}

func TestAttachmentLifecycleOrdering(t *testing.T) {
	peer := newFakePeer()
	proc := newFakeProcess()
	opts := DefaultOptions()
	opts.BackpressurePollInterval = 5 * time.Millisecond

	att := NewAttachment(proc, peer, opts, "test-sess", 80, 24, nil, nil)
	att.Start()

	// Push data into process
	proc.readCh <- []byte("hello terminal\r\n")

	// Wait for delivery
	time.Sleep(20 * time.Millisecond)

	// Close process to trigger exit sequence
	_ = proc.Close() //nolint:errcheck // Test close

	// Wait for queue to drain and exit message to be sent
	time.Sleep(50 * time.Millisecond)

	peer.mu.Lock()
	events := append([]string(nil), peer.events...)
	peer.mu.Unlock()

	if len(events) < 4 {
		t.Fatalf("expected at least 4 events (ready, binary, exit, close), got: %v", events)
	}
	if events[0] != "ready:test-sess" {
		t.Errorf("expected first event to be ready:test-sess, got: %s", events[0])
	}
	if events[1] != "binary:hello terminal\r\n" {
		t.Errorf("expected second event to be binary data, got: %s", events[1])
	}
	if events[2] != "exit" {
		t.Errorf("expected third event to be exit, got: %s", events[2])
	}
	if events[3] != "close" {
		t.Errorf("expected fourth event to be close, got: %s", events[3])
	}
}

func TestAttachmentSilentSession(t *testing.T) {
	peer := newFakePeer()
	proc := newFakeProcess()
	opts := DefaultOptions()

	att := NewAttachment(proc, peer, opts, "silent-sess", 80, 24, nil, nil)
	att.Start()

	// Silent session produces no output. Now shutdown.
	att.Shutdown()

	peer.mu.Lock()
	closed := peer.closed
	peer.mu.Unlock()

	if !closed {
		t.Errorf("expected peer to be closed after shutdown")
	}
}

type hangingPeer struct {
	fakePeer
}

func (h *hangingPeer) Close(code int, reason string) error {
	time.Sleep(200 * time.Millisecond)
	return nil
}

func TestManagerForceCloseOnTimeout(t *testing.T) {
	mgr := NewManager(20 * time.Millisecond)
	hPeer := &hangingPeer{fakePeer: *newFakePeer()}
	proc := newFakeProcess()
	opts := DefaultOptions()

	att := NewAttachment(proc, hPeer, opts, "hanging-sess", 80, 24, mgr.Untrack, nil)
	if err := mgr.Register(att); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := mgr.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	hPeer.mu.Lock()
	closeNowDone := hPeer.closeNowDone
	hPeer.mu.Unlock()

	if !closeNowDone {
		t.Errorf("expected CloseNow to be called when Close timed out")
	}
}

func TestStagingBuffer_SingleOversizedChunk(t *testing.T) {
	buf := NewStagingBuffer(10)
	if _, err := buf.Write([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	oversized := []byte("0123456789abcdefghij")
	if _, err := buf.Write(oversized); err != nil {
		t.Fatal(err)
	}
	if buf.Size() != 20 || buf.Len() != 1 {
		t.Errorf("expected size 20 and len 1, got size %d, len %d", buf.Size(), buf.Len())
	}
	chunks := buf.Drain()
	if len(chunks) != 1 || !bytes.Equal(chunks[0], oversized) {
		t.Fatalf("expected oversized chunk retained whole, got %q", chunks)
	}
}

func TestStagingBuffer_EdgeCases(t *testing.T) {
	b0 := NewStagingBuffer(0)
	if b0.Cap() != defaultStagingCapacity {
		t.Errorf("expected default capacity %d for 0, got %d", defaultStagingCapacity, b0.Cap())
	}
	bNeg := NewStagingBuffer(-10)
	if bNeg.Cap() != defaultStagingCapacity {
		t.Errorf("expected default capacity %d for negative, got %d", defaultStagingCapacity, bNeg.Cap())
	}

	b := NewStagingBuffer(100)
	n, err := b.Write([]byte{})
	if n != 0 || err != nil {
		t.Errorf("Write([]byte{}) = (%d, %v); want (0, nil)", n, err)
	}
	if b.Len() != 0 || b.Size() != 0 {
		t.Errorf("expected empty buffer after zero-length write")
	}

	emptyChunks := b.Drain()
	if len(emptyChunks) != 0 {
		t.Errorf("expected 0 chunks from empty drain, got %d", len(emptyChunks))
	}
}

func TestStagingBuffer_ConcurrentWritesAndDrain(t *testing.T) {
	buf := NewStagingBuffer(1024)
	var wg sync.WaitGroup
	const writers = 20

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte(idx)}, 50)
			for j := 0; j < 50; j++ {
				_, _ = buf.Write(payload) //nolint:errcheck // test concurrent writes
				if j%10 == 0 {
					_ = buf.Drain()
				}
				_ = buf.Len()
				_ = buf.Size()
			}
		}(i)
	}
	wg.Wait()
}

func TestOutputQueue_ConcurrentPushAndPop(t *testing.T) {
	q := NewOutputQueue(1000, 200)
	var wg sync.WaitGroup
	const pushes = 50

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < pushes; i++ {
			chunk, ok := q.Pop()
			if !ok {
				return
			}
			if len(chunk) == 0 {
				t.Errorf("received empty chunk from Pop")
			}
		}
	}()

	for i := 0; i < pushes; i++ {
		q.Push([]byte("test data chunk"))
		_ = q.Bytes()
	}

	code := 0
	q.CloseWithExit(&code)
	wg.Wait()
}

func TestOutputQueue_PopUnblockOnClose(t *testing.T) {
	q := NewOutputQueue(1000, 200)

	done := make(chan struct{})
	go func() {
		chunk, ok := q.Pop()
		if ok || chunk != nil {
			t.Errorf("expected ok=false and nil chunk on close, got %v, %v", chunk, ok)
		}
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	q.CloseAbnormal()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Pop did not unblock after CloseAbnormal")
	}
}

func TestGhostClientCleanup_OnClientDisconnect(t *testing.T) {
	mgr := NewManager(time.Second)
	peer := newFakePeer()
	proc := newFakeProcess()
	opts := DefaultOptions()

	att := NewAttachment(proc, peer, opts, "ghost-disconnect-sess", 80, 24, mgr.Untrack, nil)
	if err := mgr.Register(att); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if mgr.ActiveCount() != 1 {
		t.Fatalf("expected 1 active attachment before start, got %d", mgr.ActiveCount())
	}

	att.Start()

	// Simulate client dropping connection
	_ = peer.Close(StatusNormalClosure, "client gone") //nolint:errcheck // test disconnect

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.ActiveCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if mgr.ActiveCount() != 0 {
		t.Errorf("expected 0 active attachments after client disconnect, got %d", mgr.ActiveCount())
	}

	proc.mu.Lock()
	procClosed := proc.closed
	proc.mu.Unlock()
	if !procClosed {
		t.Errorf("expected process to be detached/closed on client disconnect")
	}
}

type errorPeer struct {
	fakePeer
}

func (e *errorPeer) SendBinary(ctx context.Context, data []byte) error {
	return errors.New("simulated write failure")
}

func TestGhostClientCleanup_OnWriteFailure(t *testing.T) {
	mgr := NewManager(time.Second)
	peer := &errorPeer{fakePeer: *newFakePeer()}
	proc := newFakeProcess()
	opts := DefaultOptions()

	att := NewAttachment(proc, peer, opts, "ghost-write-fail-sess", 80, 24, mgr.Untrack, nil)
	if err := mgr.Register(att); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if mgr.ActiveCount() != 1 {
		t.Fatalf("expected 1 active attachment before start, got %d", mgr.ActiveCount())
	}

	att.Start()

	proc.readCh <- []byte("trigger failure\n")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mgr.ActiveCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if mgr.ActiveCount() != 0 {
		t.Errorf("expected 0 active attachments after write failure, got %d", mgr.ActiveCount())
	}

	proc.mu.Lock()
	procClosed := proc.closed
	proc.mu.Unlock()
	if !procClosed {
		t.Errorf("expected process to be detached on write failure")
	}
}
