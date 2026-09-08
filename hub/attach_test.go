package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// attachFixture starts an isolated tmux server and a hub wired to it, and
// returns the hub server, the httptest server, the tmux binary, and the socket.
func attachFixture(t *testing.T) (*server, *httptest.Server, string, string) {
	t.Helper()
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	sock := uniqueSocket(t)
	tmux, _ := exec.LookPath("tmux")
	if _, _, code, err := runCommand(tmux, "-L", sock, "start-server"); err != nil || code != 0 {
		t.Fatalf("start-server: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { runCommand(tmux, "-L", sock, "kill-server") })

	s := newServer(&config{addr: "127.0.0.1:0", origin: "http://example.com"}, "test-token")
	s.tmux = &tmuxClient{path: tmux, socket: sock}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return s, ts, tmux, sock
}

func mkSessionCmd(t *testing.T, tmux, sock, name, cmd string) {
	t.Helper()
	args := []string{"-L", sock, "new-session", "-d", "-s", name}
	if cmd != "" {
		args = append(args, cmd)
	}
	if _, _, code, err := runCommand(tmux, args...); err != nil || code != 0 {
		t.Fatalf("new-session %q: code=%d err=%v", name, code, err)
	}
}

func wsURL(ts *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http") + path
}

func dialWS(t *testing.T, ts *httptest.Server, path string, header http.Header) *websocket.Conn {
	t.Helper()
	conn, resp, err := dialWSResp(t, ts, path, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func dialWSResp(t *testing.T, ts *httptest.Server, path string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL(ts, path), &websocket.DialOptions{HTTPHeader: header})
}

func readFrame(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mt, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return mt, data
}

func readFrameCtx(ctx context.Context, conn *websocket.Conn) (websocket.MessageType, []byte, error) {
	return conn.Read(ctx)
}

func frameType(t *testing.T, data []byte) string {
	t.Helper()
	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
	return m.Type
}

// writeText sends a JSON control frame.
func writeText(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write text: %v", err)
	}
}

func TestChildEnvScrubbed(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TMUX_PANE", "%0")
	t.Setenv("VISUAL_TMUX_CLIENT_TOKEN", "sekret")
	t.Setenv("PATH", "/usr/bin") // must survive
	env := childEnv()
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if key == "TMUX" || key == "TMUX_PANE" || key == "VISUAL_TMUX_CLIENT_TOKEN" {
			t.Fatalf("scrubbed env still contains %q", kv)
		}
	}
	foundTerm := false
	for _, kv := range env {
		if kv == "TERM=xterm-256color" {
			foundTerm = true
		}
	}
	if !foundTerm {
		t.Fatal("expected TERM=xterm-256color in child env")
	}
}

func TestAttachReadyAndSize(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "")
	id, _ := s.tickets.issue("probe")

	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=100&rows=40", id), nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	mt, data := readFrame(t, conn)
	if mt != websocket.MessageText {
		t.Fatalf("expected text ready frame, got type %d", mt)
	}
	var ready readyMessage
	if err := json.Unmarshal(data, &ready); err != nil {
		t.Fatalf("unmarshal ready: %v", err)
	}
	if ready.Type != "ready" || ready.Cols != 100 || ready.Rows != 40 {
		t.Fatalf("unexpected ready: %+v", ready)
	}
}

func TestAttachTicketRefusal(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "")

	reusedID, _ := s.tickets.issue("probe")
	_ = s.tickets.redeem(reusedID, "probe")

	mismatchID, _ := s.tickets.issue("other")

	cases := []struct {
		name    string
		path    string
		wantMsg string
	}{
		{"missing", "/ws/local/probe?cols=80&rows=24", "invalid ticket"},
		{"reused", fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", reusedID), "invalid ticket"},
		{"mismatch", fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", mismatchID), "ticket does not match session"},
	}

	for _, c := range cases {
		conn := dialWS(t, ts, c.path, nil)
		mt, data := readFrame(t, conn)
		if mt != websocket.MessageText {
			t.Fatalf("%s: expected text error frame, got %d", c.name, mt)
		}
		var em errorMessage
		if err := json.Unmarshal(data, &em); err != nil {
			t.Fatalf("%s: unmarshal error frame: %v", c.name, err)
		}
		if em.Type != "error" || em.Retryable {
			t.Fatalf("%s: expected non-retryable error, got %+v", c.name, em)
		}
		if em.Message != c.wantMsg {
			t.Fatalf("%s: expected message %q, got %q", c.name, c.wantMsg, em.Message)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}
}

func TestAttachByteFidelity(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	needle := "日本語🙂"
	mkSessionCmd(t, tmux, sock, "probe", fmt.Sprintf("printf '%s'; sleep 30", needle))
	id, _ := s.tickets.issue("probe")

	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id), nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readFrame(t, conn) // ready

	var all []byte
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for !bytes.Contains(all, []byte(needle)) {
		mt, data, err := readFrameCtx(ctx, conn)
		if err != nil {
			t.Fatalf("did not receive %q before timeout; got %q", needle, all)
		}
		if mt == websocket.MessageBinary {
			all = append(all, data...)
		}
	}
}

func TestAttachInputReachesSession(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	out := fmt.Sprintf("/tmp/hub-input-%d", time.Now().UnixNano())
	mkSessionCmd(t, tmux, sock, "probe", fmt.Sprintf("cat > %s", out))
	t.Cleanup(func() { os.Remove(out) })
	id, _ := s.tickets.issue("probe")

	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id), nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readFrame(t, conn) // ready

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// "hello" + Enter (\r): the pane's pty is in canonical mode, so a bare
	// "hello" would sit in the line buffer until a newline arrives.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(out)
		if strings.Contains(string(b), "hello") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(out)
	t.Fatalf("input did not reach session; file=%q", string(b))
}

func TestAttachExitOnKill(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "sh -c 'sleep 60'")
	id, _ := s.tickets.issue("probe")
	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id), nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readFrame(t, conn) // ready

	if _, _, code, err := runCommand(tmux, "-L", sock, "kill-session", "-t", "=probe"); err != nil || code != 0 {
		t.Fatalf("kill-session: code=%d err=%v", code, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		mt, data, err := readFrameCtx(ctx, conn)
		if err != nil {
			t.Fatalf("expected exit frame, got read error %v", err)
		}
		if mt == websocket.MessageText && frameType(t, data) == "exit" {
			return
		}
	}
}

func TestAttachOriginRejected(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "")
	id, _ := s.tickets.issue("probe")

	_, resp, err := dialWSResp(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id),
		http.Header{"Origin": []string{"http://evil.example"}})
	if err == nil {
		t.Fatal("expected origin rejection to fail the dial")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 forbidden, got resp=%v", resp)
	}
}

func TestAttachResizeKeepsConnectionUsable(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "sh -c 'sleep 60'")
	id, _ := s.tickets.issue("probe")
	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id), nil)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readFrame(t, conn) // ready

	// valid resize
	writeText(t, conn, clientMessage{Type: "resize", Cols: 120, Rows: 30})
	// out-of-range resizes are rejected but keep the connection usable
	writeText(t, conn, clientMessage{Type: "resize", Cols: 0, Rows: 30})
	writeText(t, conn, clientMessage{Type: "resize", Cols: 5000, Rows: 30})

	// ping → pong proves the connection is still usable
	writeText(t, conn, clientMessage{Type: "ping"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		mt, data, err := readFrameCtx(ctx, conn)
		if err != nil {
			t.Fatalf("connection not usable after resizes: %v", err)
		}
		if mt == websocket.MessageText && frameType(t, data) == "pong" {
			return
		}
	}
}

func TestAttachConcurrent(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "probe", "sh -c 'sleep 60'")
	id1, _ := s.tickets.issue("probe")
	id2, _ := s.tickets.issue("probe")

	c1 := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id1), nil)
	defer c1.Close(websocket.StatusNormalClosure, "")
	c2 := dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s&cols=80&rows=24", id2), nil)
	defer c2.Close(websocket.StatusNormalClosure, "")

	_, d1 := readFrame(t, c1)
	_, d2 := readFrame(t, c2)
	if frameType(t, d1) != "ready" || frameType(t, d2) != "ready" {
		t.Fatalf("both attachments should receive ready; got %q and %q", d1, d2)
	}
}
