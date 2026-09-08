package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// attachPids returns the set of PIDs for tmux attach processes.
func attachPids(t *testing.T) map[string]bool {
	t.Helper()
	out, _ := exec.Command("pgrep", "-f", "tmux.*attach").Output()
	m := map[string]bool{}
	for p := range strings.FieldsSeq(string(out)) {
		m[p] = true
	}
	return m
}

func TestGracefulShutdownNoOrphan(t *testing.T) {
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	tmux, _ := exec.LookPath("tmux")

	// Build the hub binary.
	wd, _ := os.Getwd()
	hubBin := filepath.Join(t.TempDir(), "hub")
	build := exec.Command("go", "build", "-o", hubBin, ".")
	build.Dir = wd
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hub: %v\n%s", err, out)
	}

	// Isolate the default tmux socket via TMUX_TMPDIR so we don't touch any
	// real default-socket sessions. Use a short temp-dir name rather than
	// t.TempDir(): tmux's socket path is TMUX_TMPDIR/tmux-<uid>/default, and
	// t.TempDir() on macOS produces paths long enough to exceed the Unix
	// socket sun_path limit, failing bind() with ENAMETOOLONG.
	tmpdir, err := os.MkdirTemp("", "tmuxhub-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpdir) })
	isolatedEnv := append(os.Environ(), "TMUX_TMPDIR="+tmpdir)

	out, errout, code, err := runEnvCommand(tmux, isolatedEnv, "new-session", "-d", "-s", "probe", "sh -c 'sleep 60'")
	if err != nil || code != 0 {
		t.Fatalf("new-session: code=%d err=%v stdout=%q stderr=%q", code, err, out, errout)
	}
	t.Cleanup(func() { runEnvCommand(tmux, isolatedEnv, "kill-server") })

	// Find a free port and start the hub.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	const token = "test-token"
	hub := exec.Command(hubBin, "--addr", addr)
	hub.Env = append(os.Environ(), "TMUX_HUB_TOKEN="+token, "TMUX_TMPDIR="+tmpdir)
	var stderr bytes.Buffer
	hub.Stderr = &stderr
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hub.Process.Kill()
		_ = hub.Wait()
	})

	baseURL := "http://" + addr
	waitHubReady(t, baseURL, token)

	// Get a ticket and attach.
	ticket := getTicket(t, baseURL, token, "probe")
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/local/probe?ticket=" + ticket + "&cols=80&rows=24"

	// Capture the attach-process baseline BEFORE dialing: the hub spawns
	// `tmux attach-session` synchronously during the WebSocket upgrade, so a
	// baseline taken after Dial would already include it and the diff below
	// would never find a new PID.
	before := attachPids(t)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	dialCancel()
	if err != nil {
		t.Fatalf("dial: %v (stderr=%s)", err, stderr.String())
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Wait until a new attach process appears (spawned on attachment).
	var attachPID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for pid := range attachPids(t) {
			if !before[pid] {
				attachPID = pid
			}
		}
		if attachPID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if attachPID == "" {
		t.Fatal("no attach process appeared after attaching")
	}

	// Send SIGTERM and assert the hub exits within 10s.
	if err := hub.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- hub.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = hub.Process.Kill()
		t.Fatalf("hub did not exit within 10s (stderr=%s)", stderr.String())
	}

	// The attach process must be gone (no orphan).
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !attachPids(t)[attachPID] {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphaned tmux attach process %s still running after shutdown", attachPID)
}

func runEnvCommand(bin string, env []string, args ...string) (string, string, int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
		err = nil
	}
	return out.String(), errBuf.String(), code, err
}

func waitHubReady(t *testing.T, baseURL, token string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/hosts/local/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("hub did not become ready")
}

func getTicket(t *testing.T, baseURL, token, session string) string {
	t.Helper()
	body := fmt.Sprintf(`{"hostId":"local","session":%q}`, session)
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/ws-ticket", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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
