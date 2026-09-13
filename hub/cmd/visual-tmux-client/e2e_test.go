package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func runCmd(bin string, env []string, args ...string) (string, string, int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
		err = nil
	}
	return out.String(), errBuf.String(), code, err
}

func waitReady(t *testing.T, baseURL, token string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/api/hosts/local/sessions", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := client.Do(req); err == nil {
			_ = resp.Body.Close() //nolint:errcheck // test polling
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("hub did not become ready")
}

func fetchTicket(t *testing.T, baseURL, token, session string) string {
	t.Helper()
	body := fmt.Sprintf(`{"hostId":"local","session":%q}`, session)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/ws-ticket", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // test cleanup
	var out struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Ticket == "" {
		t.Fatalf("no ticket: status %d", resp.StatusCode)
	}
	return out.Ticket
}

func TestE2ERestartPreservesSession(t *testing.T) {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}

	// Build hub binary from ./cmd/visual-tmux-client
	hubBin := filepath.Join(t.TempDir(), "visual-tmux-client")
	build := exec.Command("go", "build", "-o", hubBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hub: %v\n%s", err, out)
	}

	tmpdir, err := os.MkdirTemp("", "vtc-e2e-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpdir) }) //nolint:errcheck // test cleanup

	socket := filepath.Join(tmpdir, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatalf("create socket dir: %v", err)
	}

	tmuxEnv := append([]string{"TMUX_TMPDIR=" + tmpdir}, os.Environ()...)
	_, _, code, err := runCmd(tmux, tmuxEnv, "-S", socket, "new-session", "-d", "-s", "e2e-sess")
	if err != nil || code != 0 {
		t.Fatalf("new-session failed: code=%d err=%v", code, err)
	}
	t.Cleanup(func() {
		_, _, _, _ = runCmd(tmux, tmuxEnv, "-S", socket, "kill-server") //nolint:errcheck // test cleanup
	})

	startHub := func() (*exec.Cmd, string, string) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		_ = l.Close() //nolint:errcheck // close free port listener

		const token = "e2e-secret-token"
		cmd := exec.Command(hubBin, "--addr", addr)
		cmd.Env = append(tmuxEnv, "VISUAL_TMUX_CLIENT_TOKEN="+token)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		baseURL := "http://" + addr
		waitReady(t, baseURL, token)
		return cmd, baseURL, token
	}

	// 1. Start hub instance 1
	hub1, base1, tok1 := startHub()

	// 2. Attach WebSocket to e2e-sess on hub 1
	tkt1 := fetchTicket(t, base1, tok1, "e2e-sess")
	wsURL1 := "ws" + strings.TrimPrefix(base1, "http") + "/ws/local/e2e-sess?ticket=" + tkt1
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn1, _, err := websocket.Dial(ctx, wsURL1, nil)
	if err != nil {
		t.Fatalf("dial hub 1 failed: %v", err)
	}
	_, _, err = conn1.Read(ctx)
	if err != nil {
		t.Fatalf("read ready frame hub 1 failed: %v", err)
	}

	// 3. Stop hub instance 1 gracefully
	_ = hub1.Process.Signal(syscall.SIGTERM)           //nolint:errcheck // graceful stop
	_ = hub1.Wait()                                    //nolint:errcheck // wait exit
	_ = conn1.Close(websocket.StatusNormalClosure, "") //nolint:errcheck // close conn

	// 4. Start hub instance 2 (simulating hub restart)
	hub2, base2, tok2 := startHub()
	t.Cleanup(func() {
		if hub2.Process != nil {
			_ = hub2.Process.Kill() //nolint:errcheck // test cleanup
			_ = hub2.Wait()         //nolint:errcheck // test cleanup
		}
	})

	// 5. Reconnect to the same tmux session through hub instance 2
	tkt2 := fetchTicket(t, base2, tok2, "e2e-sess")
	wsURL2 := "ws" + strings.TrimPrefix(base2, "http") + "/ws/local/e2e-sess?ticket=" + tkt2
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	conn2, _, err := websocket.Dial(ctx2, wsURL2, nil)
	if err != nil {
		t.Fatalf("reconnect to e2e-sess via hub 2 failed: %v", err)
	}
	defer func() { _ = conn2.Close(websocket.StatusNormalClosure, "") }() //nolint:errcheck // test cleanup

	msgType, data, err := conn2.Read(ctx2)
	if err != nil {
		t.Fatalf("read ready frame hub 2 failed: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("expected text ready message, got: %v", msgType)
	}
	var ready map[string]any
	if err := json.Unmarshal(data, &ready); err != nil {
		t.Fatal(err)
	}
	if ready["session"] != "e2e-sess" {
		t.Errorf("expected reconnected session e2e-sess, got: %v", ready["session"])
	}
}
