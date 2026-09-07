package tmuxconn

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"visual-tmux-client/engine/domain"
	"visual-tmux-client/engine/eventbus"
)

// These tests drive a real local tmux server (skipped if the `tmux` binary
// isn't available) and exercise the scenarios in
// openspec/changes/bootstrap-tmux-engine/specs/tmux-engine/spec.md end to
// end: HostConn spawns real control-mode subprocesses under a pty and
// mirrors real tmux state, not synthetic fixtures.

func requireTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not found in PATH, skipping end-to-end test")
	}
}

// testSocket returns a socket name unique to the running test and a cleanup
// func that kills the server on that socket.
func testSocket(t *testing.T) string {
	t.Helper()
	name := "engine-test-" + strings.ReplaceAll(strings.ReplaceAll(t.Name(), "/", "-"), " ", "-")
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-L", name, "kill-server").Run()
	})
	return name
}

func tmuxOn(t *testing.T, socket string, args ...string) {
	t.Helper()
	full := append([]string{"-L", socket}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", full, err, out)
	}
}

// waitFor polls cond every 20ms until it returns true or the timeout
// elapses, failing the test on timeout.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func TestHostConn_DiscoversExistingSessionsAtConnect(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)

	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")
	tmuxOn(t, socket, "new-session", "-d", "-s", "beta")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	for _, name := range []string{"alpha", "beta"} {
		key := domain.SessionKey{HostID: hostID, Name: name}
		s := model.Session(key)
		if s == nil {
			t.Fatalf("expected session %q to be discovered, got none", name)
		}
		if len(s.Windows) == 0 {
			t.Errorf("expected session %q to have at least one window", name)
		}
		for _, w := range s.Windows {
			if len(w.Panes) == 0 {
				t.Errorf("expected window %q of session %q to have at least one pane", w.ID, name)
			}
		}
	}
}

func TestHostConn_DetectsSessionCreatedAfterConnection(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	tmuxOn(t, socket, "new-session", "-d", "-s", "gamma")

	waitFor(t, 3*time.Second, "session.discovered for gamma", func() bool {
		return model.Session(domain.SessionKey{HostID: hostID, Name: "gamma"}) != nil
	})

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.SessionDiscovered {
				if key, ok := evt.Payload.(domain.SessionKey); ok && key.Name == "gamma" {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.discovered event for gamma")
		}
	}
}

func TestHostConn_DetectsSessionClosedAfterConnection(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")
	tmuxOn(t, socket, "new-session", "-d", "-s", "doomed")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	tmuxOn(t, socket, "kill-session", "-t", "doomed")

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.SessionClosed {
				if key, ok := evt.Payload.(domain.SessionKey); ok && key.Name == "doomed" {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.closed event for doomed")
		}
	}

	if model.Session(domain.SessionKey{HostID: hostID, Name: "doomed"}) != nil {
		t.Error("expected doomed session to be removed from the domain model")
	}
}

func TestHostConn_PaneOutputStreaming(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	subID, output := bus.SubscribeOutput()
	t.Cleanup(func() { bus.UnsubscribeOutput(subID) })

	marker := "hello-from-engine-test"
	tmuxOn(t, socket, "send-keys", "-t", "alpha", fmt.Sprintf("echo %s", marker), "Enter")

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-output:
			if strings.Contains(string(evt.Data), marker) {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.output containing marker")
		}
	}
}

func TestHostConn_PaneSplitEmitsLayoutChanged(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID string
	for id := range model.Session(key).Windows {
		windowID = id
	}

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	tmuxOn(t, socket, "split-window", "-t", "alpha")

	deadline := time.After(3 * time.Second)
	sawEvent := false
	for !sawEvent {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.WindowLayoutChanged {
				sawEvent = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for window.layout-changed after split-window")
		}
	}

	waitFor(t, 3*time.Second, "two panes in the split window", func() bool {
		w := model.Window(key, windowID)
		return w != nil && len(w.Panes) == 2
	})

	w := model.Window(key, windowID)
	seenX0, seenNonZeroY := false, false
	for _, p := range w.Panes {
		if p.Width == 0 || p.Height == 0 {
			t.Errorf("expected pane %q to have non-zero Width/Height, got %dx%d", p.ID, p.Width, p.Height)
		}
		if p.X == 0 {
			seenX0 = true
		}
		if p.Y != 0 {
			seenNonZeroY = true
		}
	}
	if !seenX0 || !seenNonZeroY {
		t.Errorf("expected split panes to have distinct X/Y geometry (one at X=0, one at Y!=0 for a vertical split), got panes %+v", w.Panes)
	}
}

func TestHostConn_WindowRenamed(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID string
	for id := range model.Session(key).Windows {
		windowID = id
	}

	tmuxOn(t, socket, "rename-window", "-t", "alpha", "renamed-by-test")

	waitFor(t, 3*time.Second, "window renamed in domain model", func() bool {
		w := model.Window(key, windowID)
		return w != nil && w.Name == "renamed-by-test"
	})
}

func TestHostConn_PaneDiesEmitsEvent(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var originalPaneID string
	for _, w := range model.Session(key).Windows {
		for pid := range w.Panes {
			originalPaneID = pid
		}
	}

	tmuxOn(t, socket, "split-window", "-t", "alpha")

	var windowID, secondPaneID string
	waitFor(t, 3*time.Second, "split window to have two panes", func() bool {
		s := model.Session(key)
		for id, w := range s.Windows {
			if len(w.Panes) == 2 {
				windowID = id
				for pid := range w.Panes {
					if pid != originalPaneID {
						secondPaneID = pid
					}
				}
				return true
			}
		}
		return false
	})
	_ = windowID

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	tmuxOn(t, socket, "kill-pane", "-t", secondPaneID)

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.PaneDied {
				if pid, ok := evt.Payload.(string); ok && pid == secondPaneID {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.died event")
		}
	}
}

func TestHostConn_ReconnectRecoversScrollback(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var paneID string
	for _, w := range model.Session(key).Windows {
		for pid := range w.Panes {
			paneID = pid
		}
	}

	preMarker := "before-disconnect-marker"
	tmuxOn(t, socket, "send-keys", "-t", "alpha", fmt.Sprintf("echo %s", preMarker), "Enter")
	waitFor(t, 3*time.Second, "pre-disconnect marker on screen", func() bool {
		out, err := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", "alpha").CombinedOutput()
		return err == nil && strings.Contains(string(out), preMarker)
	})

	// Simulate a dropped connection (e.g. a killed subprocess, or later a
	// severed SSH link) by killing the session's own control-mode client
	// process directly, without touching the tmux session itself, which
	// stays alive on the server the whole time.
	h.mu.Lock()
	ts := h.sessions["alpha"]
	h.mu.Unlock()
	if ts == nil {
		t.Fatal("expected alpha to be tracked")
	}
	if err := ts.conn.cmd.Process.Kill(); err != nil {
		t.Fatalf("killing session connection's process: %v", err)
	}

	waitFor(t, 5*time.Second, "recovered pre-disconnect marker in pane scrollback", func() bool {
		return strings.Contains(string(h.PaneScrollback(paneID)), preMarker)
	})

	// New output should resume streaming on the reconnected connection.
	subID, output := bus.SubscribeOutput()
	t.Cleanup(func() { bus.UnsubscribeOutput(subID) })

	postMarker := "after-reconnect-marker"
	tmuxOn(t, socket, "send-keys", "-t", "alpha", fmt.Sprintf("echo %s", postMarker), "Enter")

	deadline := time.After(5 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-output:
			if strings.Contains(string(evt.Data), postMarker) {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.output containing marker after reconnect")
		}
	}
}

func TestHostConn_CreateSession(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if err := h.CreateSession("gamma"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	waitFor(t, 3*time.Second, "created session gamma to appear in the domain model", func() bool {
		return model.Session(domain.SessionKey{HostID: hostID, Name: "gamma"}) != nil
	})
}

func TestHostConn_KillSession(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")
	tmuxOn(t, socket, "new-session", "-d", "-s", "doomed")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if err := h.KillSession("doomed"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	waitFor(t, 3*time.Second, "killed session doomed to be removed from the domain model", func() bool {
		return model.Session(domain.SessionKey{HostID: hostID, Name: "doomed"}) == nil
	})
}

func TestHostConn_RenameSession_EngineInitiated(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	if err := h.RenameSession("alpha", "renamed"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.SessionRenamed {
				if key, ok := evt.Payload.(domain.SessionKey); ok && key.Name == "renamed" {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for session.renamed event")
		}
	}

	if model.Session(domain.SessionKey{HostID: hostID, Name: "alpha"}) != nil {
		t.Error("expected old session key to no longer resolve after rename")
	}
	if model.Session(domain.SessionKey{HostID: hostID, Name: "renamed"}) == nil {
		t.Error("expected session to be found under its new name after rename")
	}
}

func TestHostConn_RenameSession_ExternallyTriggered(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	// Rename via a direct tmux invocation, not through HostConn.RenameSession.
	tmuxOn(t, socket, "rename-session", "-t", "alpha", "renamed-externally")

	waitFor(t, 3*time.Second, "externally renamed session to appear under its new name", func() bool {
		return model.Session(domain.SessionKey{HostID: hostID, Name: "renamed-externally"}) != nil
	})
	if model.Session(domain.SessionKey{HostID: hostID, Name: "alpha"}) != nil {
		t.Error("expected old session key to no longer resolve after external rename")
	}
}

func TestHostConn_NewWindow(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	if len(model.Session(key).Windows) != 1 {
		t.Fatalf("expected exactly one window before NewWindow")
	}

	if err := h.NewWindow("alpha"); err != nil {
		t.Fatalf("NewWindow: %v", err)
	}

	waitFor(t, 3*time.Second, "second window and its initial pane to appear in the domain model", func() bool {
		s := model.Session(key)
		if len(s.Windows) != 2 {
			return false
		}
		for _, w := range s.Windows {
			if len(w.Panes) == 0 {
				return false
			}
		}
		return true
	})
}

func TestHostConn_KillWindow(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")
	tmuxOn(t, socket, "new-window", "-t", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var doomedWindowID string
	waitFor(t, 3*time.Second, "two windows discovered", func() bool {
		s := model.Session(key)
		if len(s.Windows) != 2 {
			return false
		}
		for id, w := range s.Windows {
			if !w.Active {
				doomedWindowID = id
			}
		}
		return doomedWindowID != ""
	})

	if err := h.KillWindow(doomedWindowID); err != nil {
		t.Fatalf("KillWindow: %v", err)
	}

	waitFor(t, 3*time.Second, "killed window and its panes to be removed from the domain model", func() bool {
		return model.Window(key, doomedWindowID) == nil
	})
}

func TestHostConn_RenameWindow(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID string
	for id := range model.Session(key).Windows {
		windowID = id
	}

	if err := h.RenameWindow(windowID, "renamed-by-hostconn"); err != nil {
		t.Fatalf("RenameWindow: %v", err)
	}

	waitFor(t, 3*time.Second, "window renamed in domain model", func() bool {
		w := model.Window(key, windowID)
		return w != nil && w.Name == "renamed-by-hostconn"
	})
}

func TestHexEncodeBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"printable ASCII", []byte("hi"), "68 69"},
		{"control byte", []byte{0x03}, "03"},
		{"multi-byte UTF-8", []byte("é"), "c3 a9"},
		{"empty", []byte{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hexEncodeBytes(c.data); got != c.want {
				t.Errorf("hexEncodeBytes(%v) = %q, want %q", c.data, got, c.want)
			}
		})
	}
}

func TestHostConn_SplitPane(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID, paneID string
	for id, w := range model.Session(key).Windows {
		windowID = id
		for pid := range w.Panes {
			paneID = pid
		}
	}

	if err := h.SplitPane(paneID, true); err != nil {
		t.Fatalf("SplitPane: %v", err)
	}

	waitFor(t, 3*time.Second, "two panes with updated geometry after split", func() bool {
		w := model.Window(key, windowID)
		if w == nil || len(w.Panes) != 2 {
			return false
		}
		for _, p := range w.Panes {
			if p.Width == 0 || p.Height == 0 {
				return false
			}
		}
		return true
	})
}

func TestHostConn_KillPane(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID, originalPaneID string
	for id, w := range model.Session(key).Windows {
		windowID = id
		for pid := range w.Panes {
			originalPaneID = pid
		}
	}

	tmuxOn(t, socket, "split-window", "-t", "alpha")

	var secondPaneID string
	waitFor(t, 3*time.Second, "split window to have two panes", func() bool {
		w := model.Window(key, windowID)
		if w == nil || len(w.Panes) != 2 {
			return false
		}
		for pid := range w.Panes {
			if pid != originalPaneID {
				secondPaneID = pid
			}
		}
		return secondPaneID != ""
	})

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	if err := h.KillPane(secondPaneID); err != nil {
		t.Fatalf("KillPane: %v", err)
	}

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.PaneDied {
				if pid, ok := evt.Payload.(string); ok && pid == secondPaneID {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.died event after KillPane")
		}
	}

	waitFor(t, 3*time.Second, "killed pane removed from domain model", func() bool {
		w := model.Window(key, windowID)
		return w != nil && len(w.Panes) == 1
	})
}

func TestHostConn_SelectPane(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")
	tmuxOn(t, socket, "split-window", "-t", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var windowID string
	var inactivePaneID string
	waitFor(t, 3*time.Second, "two panes discovered, one inactive", func() bool {
		s := model.Session(key)
		for id, w := range s.Windows {
			if len(w.Panes) != 2 {
				continue
			}
			windowID = id
			for pid, p := range w.Panes {
				if !p.Active {
					inactivePaneID = pid
				}
			}
			return inactivePaneID != ""
		}
		return false
	})

	subID, lifecycle := bus.SubscribeLifecycle()
	t.Cleanup(func() { bus.UnsubscribeLifecycle(subID) })

	if err := h.SelectPane(inactivePaneID); err != nil {
		t.Fatalf("SelectPane: %v", err)
	}

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-lifecycle:
			if evt.Type == eventbus.PaneFocusChanged {
				if pid, ok := evt.Payload.(string); ok && pid == inactivePaneID {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.focus-changed event after SelectPane")
		}
	}

	w := model.Window(key, windowID)
	if !w.Panes[inactivePaneID].Active {
		t.Errorf("expected selected pane to be active in the domain model")
	}
}

func TestHostConn_SendKeys(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var paneID string
	for _, w := range model.Session(key).Windows {
		for pid := range w.Panes {
			paneID = pid
		}
	}

	subID, output := bus.SubscribeOutput()
	t.Cleanup(func() { bus.UnsubscribeOutput(subID) })

	marker := "hello-from-sendkeys"
	if err := h.SendKeys(paneID, fmt.Appendf(nil, "echo %s\n", marker)); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	deadline := time.After(3 * time.Second)
	found := false
	for !found {
		select {
		case evt := <-output:
			if strings.Contains(string(evt.Data), marker) {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for pane.output containing marker sent via SendKeys")
		}
	}
}

func TestHostConn_SendKeys_UntrackedPaneReturnsErrorWithoutIssuingCommand(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha")

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	if err := h.SendKeys("%does-not-exist", []byte("x")); err == nil {
		t.Fatal("expected an error for an untracked pane")
	}
}

// TestHostConn_InitialConnectSeedsPreexistingPaneScrollback covers task
// 8.2's "Scrollback shown on first display" scenario for the case flagged in
// xterm-bridge-verification.md: a pane that already existed on the server
// (with content already on screen) before Connect ever ran, and that
// produces no fresh output afterward. Before refreshWindowPanes seeded
// discovery-time panes via capture-pane, PaneScrollback stayed empty for
// such a pane until it happened to emit new output, since %output-driven
// ring buffers only start once output actually streams.
func TestHostConn_InitialConnectSeedsPreexistingPaneScrollback(t *testing.T) {
	requireTmux(t)
	socket := testSocket(t)
	tmuxOn(t, socket, "new-session", "-d", "-s", "alpha", "-x", "80", "-y", "24")

	marker := "preexisting-pane-marker"
	tmuxOn(t, socket, "send-keys", "-t", "alpha", fmt.Sprintf("echo %s", marker), "Enter")
	waitFor(t, 3*time.Second, "marker on screen before connect", func() bool {
		out, err := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", "alpha").CombinedOutput()
		return err == nil && strings.Contains(string(out), marker)
	})

	model := domain.NewModel()
	bus := eventbus.NewBus()
	hostID := domain.HostID("local/" + socket)

	h, err := Connect(Options{HostID: hostID, SocketName: socket}, model, bus)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })

	key := domain.SessionKey{HostID: hostID, Name: "alpha"}
	var paneID string
	for _, w := range model.Session(key).Windows {
		for pid := range w.Panes {
			paneID = pid
		}
	}
	if paneID == "" {
		t.Fatal("expected a discovered pane")
	}

	// No output has streamed via %output since Connect returned -- this
	// must already reflect the pane's pre-existing on-screen content via
	// the discovery-time capture-pane seed, not streamed output.
	if got := string(h.PaneScrollback(paneID)); !strings.Contains(got, marker) {
		t.Fatalf("expected PaneScrollback to already contain %q right after Connect, got %q", marker, got)
	}
}

