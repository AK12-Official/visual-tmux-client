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

func TestEnsureGlobalOptionsWhenMouseAlreadyOn(t *testing.T) {
	s, _, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "mouse-already-on-test", "")

	// Simulate user config: mouse on, but window-size smallest
	runCommand(tmux, "-L", sock, "set-option", "-g", "mouse", "on")
	runCommand(tmux, "-L", sock, "set-option", "-g", "window-size", "smallest")

	s.ensureGlobalOptions()

	outWin, _, codeWin, errWin := runCommand(tmux, "-L", sock, "show-options", "-gv", "window-size")
	if errWin != nil || codeWin != 0 || strings.TrimSpace(outWin) != "latest" {
		t.Fatalf("expected window-size to be updated to latest, got code=%d out=%q err=%v", codeWin, outWin, errWin)
	}

	outMouse, _, codeMouse, errMouse := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if errMouse != nil || codeMouse != 0 || strings.TrimSpace(outMouse) != "on" {
		t.Fatalf("expected mouse to remain on, got code=%d out=%q err=%v", codeMouse, outMouse, errMouse)
	}
}

func TestEnsureGlobalOptionsAfterServerRestart(t *testing.T) {
	s, _, tmux, sock := attachFixture(t)
	mkSessionCmd(t, tmux, sock, "first-srv-test", "")

	s.ensureGlobalOptions()

	// Kill server completely
	runCommand(tmux, "-L", sock, "kill-server")
	time.Sleep(100 * time.Millisecond)

	// Start a fresh session on the same socket (new daemon with defaults)
	mkSessionCmd(t, tmux, sock, "second-srv-test", "")

	out, _, _, _ := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if strings.TrimSpace(out) != "off" {
		t.Fatalf("expected fresh daemon to start with mouse off, got %q", out)
	}

	// ensureGlobalOptions must detect that the new server is not configured and set it
	s.ensureGlobalOptions()

	out, _, code, err := runCommand(tmux, "-L", sock, "show-options", "-gv", "mouse")
	if err != nil || code != 0 || strings.TrimSpace(out) != "on" {
		t.Fatalf("expected mouse to be on on restarted daemon, got code=%d out=%q err=%v", code, out, err)
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

func TestAttachDoesNotRepaintExistingAttachment(t *testing.T) {
	s, ts, tmux, sock := attachFixture(t)

	mkSessionCmd(t, tmux, sock, "sess-1", "sleep 3600")
	mkSessionCmd(t, tmux, sock, "sess-2", "sleep 3600")

	// First attach ensures global options
	ticket1, _, _ := s.tickets.issue("sess-1")
	conn1 := dialWS(t, ts, fmt.Sprintf("/ws/local/sess-1?ticket=%s&cols=80&rows=24", ticket1), nil)
	defer conn1.CloseNow()

	ctxReady, cancelReady := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelReady()
	if _, _, err := conn1.Read(ctxReady); err != nil {
		t.Fatalf("read ready frame conn1: %v", err)
	}

	// Drain any initial output from attach
	time.Sleep(300 * time.Millisecond)
	for {
		ctxDrain, cancelDrain := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, _, err := conn1.Read(ctxDrain)
		cancelDrain()
		if err != nil {
			break
		}
	}

	// Now attach second session
	ticket2, _, _ := s.tickets.issue("sess-2")
	conn2 := dialWS(t, ts, fmt.Sprintf("/ws/local/sess-2?ticket=%s&cols=80&rows=24", ticket2), nil)
	defer conn2.CloseNow()

	ctxReady2, cancelReady2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelReady2()
	if _, _, err := conn2.Read(ctxReady2); err != nil {
		t.Fatalf("read ready frame conn2: %v", err)
	}

	// Ensure conn1 receives no spurious output
	ctxCheck, cancelCheck := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelCheck()
	_, data, err := conn1.Read(ctxCheck)
	if err == nil {
		t.Fatalf("expected no spurious repaint on conn1, got frame: %q", string(data))
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
	runCommand(tmux, "-L", sock, "send-keys", "-t", "=scroll-test:", "seq 1 100", "Enter")
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

	// Background drain keeps the WebSocket read buffer from filling up
	readCtx, readCancel := context.WithCancel(context.Background())
	defer readCancel()
	go func() {
		for {
			if _, _, err := conn.Read(readCtx); err != nil {
				return
			}
		}
	}()
	time.Sleep(200 * time.Millisecond)

	// Verify pane is not in copy-mode initially using exact target syntax
	out, _, _, _ := runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "=scroll-test:", "#{pane_in_mode}")
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
	out, _, _, _ = runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "=scroll-test:", "#{pane_in_mode}")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("expected pane_in_mode to be 1 after wheel up, got %q", out)
	}

	outMode, _, _, _ := runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "=scroll-test:", "#{pane_mode}")
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
	out, _, _, _ = runCommand(tmux, "-L", sock, "display-message", "-p", "-t", "=scroll-test:", "#{pane_in_mode}")
	if strings.TrimSpace(out) != "0" {
		t.Fatalf("expected pane_in_mode to be 0 after wheel down to bottom, got %q", out)
	}
}
