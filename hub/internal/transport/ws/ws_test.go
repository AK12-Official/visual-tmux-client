package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/coder/websocket"
)

type mockRedeemer struct {
	mu       sync.Mutex
	tickets  map[string]string // ticket -> session
	expired  map[string]bool
	redeemed []string
}

func newMockRedeemer() *mockRedeemer {
	return &mockRedeemer{
		tickets: make(map[string]string),
		expired: make(map[string]bool),
	}
}

func (m *mockRedeemer) add(ticket, session string, expired bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tickets[ticket] = session
	m.expired[ticket] = expired
}

func (m *mockRedeemer) Redeem(ticket, session string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.tickets[ticket]
	if !ok {
		return errors.New("ticket not found")
	}
	if m.expired[ticket] {
		return auth.ErrTicketExpired
	}
	if sess != session {
		return auth.ErrTicketMismatch
	}
	delete(m.tickets, ticket)
	m.redeemed = append(m.redeemed, ticket)
	return nil
}

type mockProcess struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	readCh chan []byte
	closed bool
}

func newMockProcess() *mockProcess {
	return &mockProcess{
		readCh: make(chan []byte, 20),
	}
}

func (p *mockProcess) Read(b []byte) (int, error) {
	chunk, ok := <-p.readCh
	if !ok {
		return 0, errors.New("EOF")
	}
	n := copy(b, chunk)
	return n, nil
}

func (p *mockProcess) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buf.Write(b)
}

func (p *mockProcess) Resize(cols, rows int) error {
	return nil
}

func (p *mockProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.readCh)
	}
	return nil
}

func (p *mockProcess) Detach() error {
	return p.Close()
}

func (p *mockProcess) Wait() (*int, error) {
	code := 0
	return &code, nil
}

type mockFactory struct {
	lastProc *mockProcess
}

func (f *mockFactory) NewProcess(ctx context.Context, session string, cols, rows int) (terminal.Process, error) {
	proc := newMockProcess()
	f.lastProc = proc
	return proc, nil
}

func wsURL(ts *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + path
}

func readErrorFrame(t *testing.T, conn *websocket.Conn) errorMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("expected error frame read: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("expected text message, got %v", msgType)
	}
	var em errorMessage
	if err := json.Unmarshal(data, &em); err != nil {
		t.Fatalf("failed to unmarshal error message: %v", err)
	}
	return em
}

func closeConn(conn *websocket.Conn) {
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "") //nolint:errcheck // Test cleanup
	}
}

func TestWSOriginMatrix(t *testing.T) {
	redeemer := newMockRedeemer()
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)
	opts := DefaultOptions()
	opts.Origin = "https://tmux.example.com"

	handler := NewHandler(redeemer, factory, mgr, opts, nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Configured origin matches -> connects and upgrades (gets ticket error since no ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/s1"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://tmux.example.com"}},
	})
	if err != nil {
		t.Fatalf("expected connect success: %v", err)
	}
	defer closeConn(conn)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected 101, got %d", resp.StatusCode)
	}
	em := readErrorFrame(t, conn)
	if em.Message != "invalid ticket" {
		t.Errorf("expected invalid ticket, got %s", em.Message)
	}

	// 2. Foreign origin -> 403 Forbidden rejected before upgrade
	_, resp, err = websocket.Dial(ctx, wsURL(ts, "/ws/local/s1"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.com"}},
	})
	if err == nil {
		t.Fatal("expected foreign origin to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", resp)
	}
	if resp.Body != nil {
		_ = resp.Body.Close() //nolint:errcheck // test cleanup
	}

	// 3. Originless (e.g. CLI tool) -> allowed to upgrade
	conn2, resp, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/s1"), nil)
	if err != nil {
		t.Fatalf("expected originless dial success: %v", err)
	}
	defer closeConn(conn2)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("expected 101, got %d", resp.StatusCode)
	}
}

func TestWSTicketRedemptionMatrix(t *testing.T) {
	redeemer := newMockRedeemer()
	redeemer.add("valid-tkt", "demo", false)
	redeemer.add("expired-tkt", "demo", true)
	redeemer.add("mismatch-tkt", "other", false)

	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)
	handler := NewHandler(redeemer, factory, mgr, DefaultOptions(), nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Expired ticket
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/demo?ticket=expired-tkt"), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer closeConn(conn)
	em := readErrorFrame(t, conn)
	if em.Message != "ticket expired" || em.Retryable {
		t.Errorf("expected non-retryable ticket expired, got %+v", em)
	}

	// 2. Mismatched session ticket
	conn2, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/demo?ticket=mismatch-tkt"), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer closeConn(conn2)
	em2 := readErrorFrame(t, conn2)
	if em2.Message != "ticket does not match session" || em2.Retryable {
		t.Errorf("expected mismatch error, got %+v", em2)
	}

	// 3. Valid ticket attaches and sends ready
	conn3, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/demo?ticket=valid-tkt"), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer closeConn(conn3)

	msgType, data, err := conn3.Read(ctx)
	if err != nil {
		t.Fatalf("failed reading ready frame: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("expected text frame, got %v", msgType)
	}
	var ready map[string]any
	if err := json.Unmarshal(data, &ready); err != nil {
		t.Fatal(err)
	}
	if ready["type"] != "ready" || ready["session"] != "demo" {
		t.Errorf("unexpected ready message: %+v", ready)
	}
}

func TestWSLargePasteMessage(t *testing.T) {
	redeemer := newMockRedeemer()
	redeemer.add("t1", "sess", false)
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)
	opts := DefaultOptions()
	opts.MaxInputMessage = 2 * 1024 * 1024 // 2 MiB test cap

	handler := NewHandler(redeemer, factory, mgr, opts, nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/sess?ticket=t1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(conn)

	// Read ready frame
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Send 100 KiB paste (well above 32 KiB default)
	largePaste := bytes.Repeat([]byte("A"), 100*1024)
	err = conn.Write(ctx, websocket.MessageBinary, largePaste)
	if err != nil {
		t.Fatalf("failed to send large paste: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	factory.lastProc.mu.Lock()
	received := factory.lastProc.buf.Len()
	factory.lastProc.mu.Unlock()

	if received != 100*1024 {
		t.Errorf("expected 100KB received by process, got %d", received)
	}
}

func TestWSResizeAndConcurrentAttach(t *testing.T) {
	redeemer := newMockRedeemer()
	redeemer.add("t-a", "sess-a", false)
	redeemer.add("t-b", "sess-b", false)
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)

	var mu sync.Mutex
	resizes := make(map[string][]int) // session -> [cols, rows]
	onResize := func(session string, cols, rows int) {
		mu.Lock()
		defer mu.Unlock()
		resizes[session] = []int{cols, rows}
	}

	handler := NewHandler(redeemer, factory, mgr, DefaultOptions(), nil, onResize)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect first client
	connA, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/sess-a?ticket=t-a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(connA)
	_, _, err = connA.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Connect second client concurrently
	connB, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/sess-b?ticket=t-b"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(connB)
	_, _, err = connB.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Send resize on client A
	resizeMsg, err := json.Marshal(map[string]any{"type": "resize", "cols": 120, "rows": 40})
	if err != nil {
		t.Fatal(err)
	}
	if err := connA.Write(ctx, websocket.MessageText, resizeMsg); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	rA, ok := resizes["sess-a"]
	mu.Unlock()

	if !ok {
		t.Fatal("expected resize event for sess-a")
	}
	if rA[0] != 120 || rA[1] != 40 {
		t.Errorf("expected 120x40 resize, got %v", rA)
	}
}

func TestCheckOrigin_Matrix(t *testing.T) {
	tests := []struct {
		origin     string
		configured string
		want       bool
	}{
		{"", "", true},
		{"", "http://localhost:7690", true},
		{"http://localhost:7690", "http://localhost:7690", true},
		{"https://app.example.com", "https://app.example.com", true},
		{"http://localhost:8080", "http://localhost:7690", false},
		{"http://example.com", "https://example.com", false},
		{"https://sub.example.com", "https://example.com", false},
		{"https://example.com/path", "https://example.com", false},
		{"https://evil.com", "https://example.com", false},
		{"https://example.com.evil.com", "https://example.com", false},
	}

	for _, tt := range tests {
		got := CheckOrigin(tt.origin, tt.configured)
		if got != tt.want {
			t.Errorf("CheckOrigin(%q, %q) = %v; want %v", tt.origin, tt.configured, got, tt.want)
		}
	}
}

func TestParseDimensions(t *testing.T) {
	tests := []struct {
		colsStr  string
		rowsStr  string
		maxDim   int
		wantCols int
		wantRows int
	}{
		{"", "", 1000, 80, 24},
		{"120", "40", 1000, 120, 40},
		{"0", "0", 1000, 80, 24},
		{"-5", "-10", 1000, 80, 24},
		{"1001", "24", 1000, 80, 24},
		{"80", "1001", 1000, 80, 24},
		{"invalid", "abc", 1000, 80, 24},
		{"1000", "1000", 1000, 1000, 1000},
	}

	for _, tt := range tests {
		c, r := ParseDimensions(tt.colsStr, tt.rowsStr, tt.maxDim)
		if c != tt.wantCols || r != tt.wantRows {
			t.Errorf("ParseDimensions(%q, %q, %d) = (%d, %d); want (%d, %d)",
				tt.colsStr, tt.rowsStr, tt.maxDim, c, r, tt.wantCols, tt.wantRows)
		}
	}
}

func TestWSHostNotFound(t *testing.T) {
	redeemer := newMockRedeemer()
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)
	handler := NewHandler(redeemer, factory, mgr, DefaultOptions(), nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(ts, "/ws/remote-host/demo"), nil)
	if err == nil {
		t.Fatal("expected dial to fail for non-local host ID")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got: %v", resp)
	}
}

func TestWSPayloadLimitEnforced(t *testing.T) {
	redeemer := newMockRedeemer()
	redeemer.add("limit-tkt", "sess", false)
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)
	opts := DefaultOptions()
	opts.MaxInputMessage = 1024

	handler := NewHandler(redeemer, factory, mgr, opts, nil, nil)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/sess?ticket=limit-tkt"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(conn)

	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	oversized := bytes.Repeat([]byte("X"), 4096)
	err = conn.Write(ctx, websocket.MessageBinary, oversized)
	if err != nil {
		return
	}

	time.Sleep(50 * time.Millisecond)
	readCtx, readCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer readCancel()
	_, _, rErr := conn.Read(readCtx)
	if rErr == nil {
		t.Errorf("expected read error due to payload limit exceeded, got nil")
	}
}

func TestWSControlMessageHandling(t *testing.T) {
	redeemer := newMockRedeemer()
	redeemer.add("ctl-tkt", "sess", false)
	factory := &mockFactory{}
	mgr := terminal.NewManager(time.Second)

	var mu sync.Mutex
	var resizedCols, resizedRows int
	onResize := func(session string, cols, rows int) {
		mu.Lock()
		defer mu.Unlock()
		resizedCols = cols
		resizedRows = rows
	}

	handler := NewHandler(redeemer, factory, mgr, DefaultOptions(), nil, onResize)
	mux := http.NewServeMux()
	mux.Handle("GET /ws/{hostId}/{session}", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(ts, "/ws/local/sess?ticket=ctl-tkt"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeConn(conn)

	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	err = conn.Write(ctx, websocket.MessageText, []byte("{malformed-json"))
	if err != nil {
		t.Fatal(err)
	}

	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"}) //nolint:errcheck // test marshal
	if err := conn.Write(ctx, websocket.MessageText, pingMsg); err != nil {
		t.Fatal(err)
	}

	pongReceived := false
	for i := 0; i < 5; i++ {
		readCtx, rCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		msgType, data, rErr := conn.Read(readCtx)
		rCancel()
		if rErr != nil {
			break
		}
		if msgType == websocket.MessageText {
			var m map[string]any
			if jErr := json.Unmarshal(data, &m); jErr == nil && m["type"] == "pong" {
				pongReceived = true
				break
			}
		}
	}
	if !pongReceived {
		t.Errorf("expected pong frame response to ping")
	}

	invalidResize, _ := json.Marshal(map[string]any{ //nolint:errcheck // test marshal
		"type": "resize",
		"cols": -50,
		"rows": 24,
	})
	if err := conn.Write(ctx, websocket.MessageText, invalidResize); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if resizedCols != 0 || resizedRows != 0 {
		t.Errorf("invalid resize should have been ignored, got cols=%d, rows=%d", resizedCols, resizedRows)
	}
	mu.Unlock()
}
