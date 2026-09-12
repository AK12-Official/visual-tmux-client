package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestEnsureGlobalOptionsConfiguresMouse(t *testing.T) {
	s, _, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "opt-test", "")

	// Initially, tmux default has mouse off
	out, _, _, _ := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("expected initial mouse to be off, got %q", out)
	}

	s.ensureGlobalOptions()

	out, _, code, err := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if err != nil || code != 0 || strings.TrimSpace(out) != "on" {
		t.Fatalf("expected mouse to be on, got code=%d out=%q err=%v", code, out, err)
	}

	outWin, _, codeWin, errWin := runCommand(tmux, "-L", sock, "show-options", "-gv", "window-size")
	if errWin != nil || codeWin != 0 || strings.TrimSpace(outWin) != "latest" {
		t.Fatalf("expected window-size to be latest, got code=%d out=%q err=%v", codeWin, outWin, errWin)
	}

	// Calling it a second time is idempotent and safe
	s.ensureGlobalOptions()

	out, _, code, err = runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if err != nil || code != 0 || strings.TrimSpace(out) != "on" {
		t.Fatalf("expected mouse to stay on, got code=%d out=%q err=%v", code, out, err)
	}
}

func TestCreateSessionConfiguresMouse(t *testing.T) {
	_, ts, tmux, sock := attachFixture(t)

	body, _ := json.Marshal(map[string]string{"name": "mouse-test"})
	req, _ := http.NewRequest("POST", ts.URL+"/api/hosts/local/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/hosts/local/sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	out, _, code, err := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if err != nil || code != 0 || strings.TrimSpace(out) != "on" {
		t.Fatalf("expected mouse to be on after createSession, got code=%d out=%q err=%v", code, out, err)
	}
}

func TestAttachConfiguresMouse(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)

	// Create session directly via tmux so mouse starts off
	mkSessionCmd(t, tmux, sock, "attach-mouse-test", "")

	out, _, _, _ := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("expected initial mouse to be off, got %q", out)
	}

	ticket, _, _ := s.tickets.issue("attach-mouse-test")
	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/attach-mouse-test?ticket=%s&cols=80&rows=24", ticket), nil)
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Read ready frame
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	out, _, code, err := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if err != nil || code != 0 || strings.TrimSpace(out) != "on" {
		t.Fatalf("expected mouse to be on after attach, got code=%d out=%q err=%v", code, out, err)
	}
}

func TestEndToEndMouseWheelScroll(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)

	// Create session via API
	body, _ := json.Marshal(map[string]string{"name": "scroll-test"})
	req, _ := http.NewRequest("POST", ts.URL+"/api/hosts/local/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	resp.Body.Close()

	// Produce 100 lines so output exceeds the 24-row viewport
	runCommand(tmux, "-L", sock, "send-keys", "-t", "=scroll-test", "seq 1 100", "Enter")
	time.Sleep(300 * time.Millisecond)

	ticket, _, _ := s.tickets.issue("scroll-test")
	conn := dialWS(t, ts, fmt.Sprintf("/ws/local/scroll-test?ticket=%s&cols=80&rows=24", ticket), nil)
	defer conn.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Read ready frame
	if _, _, err := conn.Read(ctx); err != nil {
		t.Fatalf("read ready frame: %v", err)
	}

	// Wait for output to settle
	time.Sleep(200 * time.Millisecond)

	// Verify pane is not in copy-mode initially
	out, _, _, _ := runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "scroll-test", "#{pane_in_mode}")
	if strings.TrimSpace(out) != "0" {
		t.Fatalf("expected pane_in_mode to be 0 initially, got %q", out)
	}

	// Send Wheel Up event: SGR mouse code \x1b[<64;10;10M
	wheelUp := []byte("\x1b[<64;10;10M")
	if err := conn.Write(ctx, websocket.MessageBinary, wheelUp); err != nil {
		t.Fatalf("write wheel up: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	// Verify tmux has entered copy-mode
	out, _, _, _ = runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "scroll-test", "#{pane_in_mode}")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("expected pane_in_mode to be 1 after wheel up, got %q", out)
	}

	outMode, _, _, _ := runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "scroll-test", "#{pane_mode}")
	if strings.TrimSpace(outMode) != "copy-mode" {
		t.Fatalf("expected pane_mode to be copy-mode, got %q", outMode)
	}

	// Send Wheel Down events back to the bottom: SGR mouse code \x1b[<65;10;10M
	wheelDown := []byte("\x1b[<65;10;10M")
	for i := 0; i < 10; i++ {
		_ = conn.Write(ctx, websocket.MessageBinary, wheelDown)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(300 * time.Millisecond)

	// Verify tmux has exited copy-mode automatically when reaching the bottom
	out, _, _, _ = runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "scroll-test", "#{pane_in_mode}")
	if strings.TrimSpace(out) != "0" {
		t.Fatalf("expected pane_in_mode to be 0 after wheel down to bottom, got %q", out)
	}
}
