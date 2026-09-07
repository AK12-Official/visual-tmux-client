package tmuxconn

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"visual-tmux-client/engine/domain"
	"visual-tmux-client/engine/eventbus"
	"visual-tmux-client/engine/ringbuffer"
	"visual-tmux-client/engine/tmuxcm"
)

// paneRingBufferSize is the per-pane raw-byte retention bound (design.md's
// "per-pane raw byte ring buffer" decision). Exact sizing is deferred per
// design.md's open questions; this is a conservative starting constant.
const paneRingBufferSize = 64 * 1024

// Options configures a HostConn.
type Options struct {
	HostID domain.HostID
	// SocketName, if non-empty, is passed as `tmux -L <SocketName>`. If
	// empty, the connection uses tmux's default local socket.
	SocketName string
}

// trackedSession is the engine's bookkeeping for one session discovered on
// the host: which sessionConn carries its output (its own dedicated
// subprocess, or the shared discovery/bootstrap connection if this happens
// to be the session that connection is attached to).
type trackedSession struct {
	key    domain.SessionKey
	name   string
	conn   *sessionConn
	isCtrl bool
}

// HostConn manages every control-mode connection for one local tmux server:
// one bootstrap connection (which doubles as the attached-session's output
// connection and as the host-wide session-discovery listener, per
// design.md's "One control-mode subprocess per tracked session" decision
// and its correction regarding host-wide discovery), plus one dedicated
// subprocess per additional tracked session.
type HostConn struct {
	hostID     domain.HostID
	socketArgs []string

	model *domain.Model
	bus   *eventbus.Bus

	mu              sync.Mutex
	closed          bool
	wg              sync.WaitGroup // tracks every goroutine spawned via h.spawn, so Close can wait for them
	syncMu          sync.Mutex     // serializes syncSessions: %sessions-changed arrives redundantly (see spike-notes.md), and concurrent calls would otherwise race on the same diff-and-add, each spawning its own duplicate connection for the same new session name
	ctrl            *sessionConn
	ctrlSessionName string
	sessions        map[string]*trackedSession // by session name
	connSession     map[*sessionConn]string    // reverse lookup: conn -> session name
	windowPanes     map[string]map[string]bool // windowID -> set of pane IDs, for %layout-change diffing
	ringBuffers     map[string]*ringbuffer.Buffer
}

// Connect establishes a HostConn: spawns the bootstrap control-mode
// connection, discovers every session already on the server (Scenario:
// "Sessions present before connection"), and spawns a dedicated connection
// for each session beyond the one the bootstrap connection attached to.
func Connect(opts Options, model *domain.Model, bus *eventbus.Bus) (*HostConn, error) {
	var socketArgs []string
	if opts.SocketName != "" {
		socketArgs = []string{"-L", opts.SocketName}
	}

	h := &HostConn{
		hostID:      opts.HostID,
		socketArgs:  socketArgs,
		model:       model,
		bus:         bus,
		sessions:    make(map[string]*trackedSession),
		connSession: make(map[*sessionConn]string),
		windowPanes: make(map[string]map[string]bool),
		ringBuffers: make(map[string]*ringbuffer.Buffer),
	}

	ctrl, err := spawnSessionConn(socketArgs, "")
	if err != nil {
		return nil, err
	}
	h.ctrl = ctrl

	sessionChanged := make(chan string, 1)
	h.spawn(func() {
		ctrl.readLoop(func(n *tmuxcm.Notification) {
			if n.Type == tmuxcm.NotifSessionChanged {
				h.mu.Lock()
				firstTime := h.ctrlSessionName == ""
				h.ctrlSessionName = n.Name
				h.mu.Unlock()
				if firstTime {
					select {
					case sessionChanged <- n.Name:
					default:
					}
					return
				}
			}
			h.handleNotification(ctrl, n)
		})
	})

	select {
	case <-sessionChanged:
	case <-time.After(5 * time.Second):
		_ = ctrl.Close()
		return nil, fmt.Errorf("tmuxconn: timed out waiting for initial session-changed on host %q", opts.HostID)
	}

	model.UpsertHost(&domain.Host{ID: h.hostID, Status: domain.HostStatusConnected})
	bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.HostConnected, Payload: h.hostID})

	if err := h.syncSessions(false); err != nil {
		_ = h.Close()
		return nil, err
	}

	return h, nil
}

// Close tears down every control-mode connection for this host, and waits
// for every goroutine spawned via h.spawn (read loops, monitors, and
// notification follow-ups) to actually exit before returning — otherwise
// they can keep running past Close and touch domain-model/event-bus state
// for a host that's supposed to be gone (observed as data races between one
// test's leftover goroutines and the next test's fresh allocations).
func (h *HostConn) Close() error {
	h.mu.Lock()
	h.closed = true
	conns := map[*sessionConn]struct{}{h.ctrl: {}}
	for _, ts := range h.sessions {
		conns[ts.conn] = struct{}{}
	}
	h.mu.Unlock()

	for conn := range conns {
		_ = conn.Close()
	}
	h.wg.Wait()
	h.model.RemoveHost(h.hostID)
	h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.HostDisconnected, Payload: h.hostID})
	return nil
}

// spawn runs f on a new goroutine tracked by h.wg, so Close can wait for it.
func (h *HostConn) spawn(f func()) {
	h.wg.Go(f)
}

// PaneScrollback returns the current contents of paneID's raw-byte ring
// buffer (streamed %output plus, after a reconnect, recovered scrollback —
// see recoverPaneScrollback), or nil if the pane has produced no output and
// never needed recovery.
func (h *HostConn) PaneScrollback(paneID string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	rb, ok := h.ringBuffers[paneID]
	if !ok {
		return nil
	}
	return rb.Bytes()
}

// issueAdminCommand runs cmd on some live control-mode connection, trying
// ctrl first and then every tracked session's connection in turn. Any live
// connection can target any object on the server via -t, so it need not be
// ctrl specifically — which matters because tmux detaches a client (killing
// its subprocess) when the session it is attached to is destroyed, even
// while other sessions remain on the server, and that detachment can start
// (and leave the connection briefly looking "alive") right as it is chosen.
// Retrying across connections, rather than picking one and trusting it,
// tolerates a connection dying mid-command.
func (h *HostConn) issueAdminCommand(cmd string) (*tmuxcm.CommandResult, error) {
	h.mu.Lock()
	conns := make([]*sessionConn, 0, len(h.sessions)+1)
	if h.ctrl != nil {
		conns = append(conns, h.ctrl)
	}
	for _, ts := range h.sessions {
		if ts.conn != h.ctrl {
			conns = append(conns, ts.conn)
		}
	}
	h.mu.Unlock()

	var lastErr error
	for _, c := range conns {
		if !c.Alive() {
			continue
		}
		res, err := c.issueCommand(cmd)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tmuxconn: no live connection available to issue command %q", cmd)
	}
	return nil, lastErr
}

// CreateSession creates a new detached session named name on the host.
// Its appearance in the domain model follows the existing
// %sessions-changed-driven discovery path (see syncSessions), the same as
// a session created by any other client.
func (h *HostConn) CreateSession(name string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`new-session -d -s "%s"`, name))
	return err
}

// KillSession kills the tracked session named name. Its removal from the
// domain model follows the existing %sessions-changed-driven discovery path
// (see syncSessions), the same as a session killed by any other client.
func (h *HostConn) KillSession(name string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`kill-session -t "%s"`, name))
	return err
}

// RenameSession renames the tracked session named name to newName. It only
// issues the rename to tmux; the domain model update and session.renamed
// event happen when the resulting %session-renamed notification arrives on
// that session's own control-mode connection (see handleNotification's
// NotifSessionRenamed case) — this covers both a rename requested through
// this method and one made directly by another tmux client, since
// %session-renamed is delivered to the attached client either way.
func (h *HostConn) RenameSession(name, newName string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`rename-session -t "%s" "%s"`, name, newName))
	return err
}

// NewWindow creates a new window in the session named sessionName. Its
// appearance in the domain model follows the existing %window-add-driven
// discovery path (see handleNotification's NotifWindowAdd case), the same as
// a window created by any other client.
func (h *HostConn) NewWindow(sessionName string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`new-window -t "%s"`, sessionName))
	return err
}

// KillWindow kills the window identified by windowID. Its removal (and its
// panes') from the domain model follows the existing %window-close-driven
// path (see handleNotification's NotifWindowClose case), the same as a
// window killed by any other client.
func (h *HostConn) KillWindow(windowID string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`kill-window -t "%s"`, windowID))
	return err
}

// RenameWindow renames the window identified by windowID to newName. The
// domain model update and WindowRenamed event happen when the resulting
// %window-renamed notification arrives (see handleNotification's
// NotifWindowRenamed case), the same as a rename made by any other client.
func (h *HostConn) RenameWindow(windowID, newName string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`rename-window -t "%s" "%s"`, windowID, newName))
	return err
}

// SplitPane splits the pane identified by paneID, vertically (panes stacked
// top/bottom, tmux's -v) if vertical is true, or horizontally (panes
// side-by-side, tmux's -h) otherwise. Both resulting panes' appearance (with
// updated layout geometry) in the domain model follows the existing
// %layout-change-driven path (see handleNotification's NotifLayoutChange
// case and refreshWindowPanes), the same as a split made by any other
// client.
func (h *HostConn) SplitPane(paneID string, vertical bool) error {
	flag := "-h"
	if vertical {
		flag = "-v"
	}
	_, err := h.issueAdminCommand(fmt.Sprintf(`split-window -t "%s" %s`, paneID, flag))
	return err
}

// KillPane kills the pane identified by paneID. Its removal from the domain
// model, and the resulting pane.died event, follow the existing
// %layout-change-driven diff path (see refreshWindowPanes), the same as a
// pane killed by any other client.
func (h *HostConn) KillPane(paneID string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`kill-pane -t "%s"`, paneID))
	return err
}

// SelectPane makes the pane identified by paneID the active pane of its
// window. The domain model update and PaneFocusChanged event happen when
// the resulting %window-pane-changed notification arrives (see
// handleNotification's NotifWindowPaneChanged case), the same as a focus
// change made by any other client (e.g. `tmux select-pane`).
func (h *HostConn) SelectPane(paneID string) error {
	_, err := h.issueAdminCommand(fmt.Sprintf(`select-pane -t "%s"`, paneID))
	return err
}

// SendKeys forwards data to the pane identified by paneID as literal input
// bytes, via `send-keys -H` (design.md's hex-byte-forwarding decision),
// which avoids needing to translate bytes into tmux's own key-name syntax.
// Returns an error without issuing any command if paneID is not tracked in
// the domain model, or is tracked but dead.
func (h *HostConn) SendKeys(paneID string, data []byte) error {
	p := h.model.Pane(paneID)
	if p == nil {
		return fmt.Errorf("tmuxconn: pane %q is not tracked", paneID)
	}
	if p.Dead {
		return fmt.Errorf("tmuxconn: pane %q is dead", paneID)
	}
	if len(data) == 0 {
		return nil
	}
	_, err := h.issueAdminCommand(fmt.Sprintf(`send-keys -H %s -t "%s"`, hexEncodeBytes(data), paneID))
	return err
}

// hexEncodeBytes formats data as the space-separated hex-byte-pair sequence
// `send-keys -H` expects, e.g. []byte("hi") -> "68 69".
func hexEncodeBytes(data []byte) string {
	pairs := make([]string, len(data))
	for i, b := range data {
		pairs[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(pairs, " ")
}

// syncSessions issues list-sessions, diffs the result against the sessions
// currently tracked for this host, and adds/removes tracked sessions
// accordingly. When emitEvents is false (used only for the initial
// bootstrap discovery), no session.discovered/session.closed events are
// published — the initial set is a starting snapshot, not a change.
func (h *HostConn) syncSessions(emitEvents bool) error {
	h.syncMu.Lock()
	defer h.syncMu.Unlock()

	res, err := h.issueAdminCommand(`list-sessions -F "#{session_name}"`)
	if err != nil {
		return err
	}

	seen := make(map[string]bool, len(res.Lines))
	for _, name := range res.Lines {
		if name != "" {
			seen[name] = true
		}
	}

	h.mu.Lock()
	var toAdd []string
	for name := range seen {
		if _, ok := h.sessions[name]; !ok {
			toAdd = append(toAdd, name)
		}
	}
	var toRemove []string
	for name := range h.sessions {
		if !seen[name] {
			toRemove = append(toRemove, name)
		}
	}
	h.mu.Unlock()

	for _, name := range toAdd {
		if err := h.addSession(name, emitEvents); err != nil {
			return err
		}
	}
	for _, name := range toRemove {
		h.removeSession(name, emitEvents)
	}
	return nil
}

func (h *HostConn) addSession(name string, emitEvents bool) error {
	key := domain.SessionKey{HostID: h.hostID, Name: name}

	h.mu.Lock()
	isCtrl := name == h.ctrlSessionName
	h.mu.Unlock()

	conn := h.ctrl
	if !isCtrl {
		var err error
		conn, err = spawnSessionConn(h.socketArgs, name)
		if err != nil {
			return fmt.Errorf("tmuxconn: spawning connection for session %q: %w", name, err)
		}
		h.spawn(func() { conn.readLoop(func(n *tmuxcm.Notification) { h.handleNotification(conn, n) }) })
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		if !isCtrl {
			_ = conn.Close()
		}
		return nil
	}
	h.sessions[name] = &trackedSession{key: key, name: name, conn: conn, isCtrl: isCtrl}
	h.connSession[conn] = name
	h.mu.Unlock()

	h.spawn(func() { h.monitorConn(name, conn) })

	h.model.UpsertSession(&domain.Session{Key: key, Windows: map[string]*domain.Window{}})

	if err := h.discoverWindowsAndPanes(key, name); err != nil {
		return err
	}

	if emitEvents {
		h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.SessionDiscovered, Payload: key})
	}
	return nil
}

func (h *HostConn) removeSession(name string, emitEvents bool) {
	h.mu.Lock()
	ts, ok := h.sessions[name]
	if ok {
		delete(h.sessions, name)
		delete(h.connSession, ts.conn)
	}
	h.mu.Unlock()
	if !ok {
		return
	}

	// Always close: even the ctrl connection is safe to close here — if
	// its own session was the one being removed, tmux has already
	// detached it (see issueAdminCommand's doc comment), and Close is idempotent.
	_ = ts.conn.Close()

	h.model.RemoveSession(ts.key)
	if emitEvents {
		h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.SessionClosed, Payload: ts.key})
	}
}

// monitorConn waits for conn's read loop to exit, then — unless this
// HostConn is being torn down, or name is no longer tracked (removeSession
// already handled a genuine session closure) — treats the exit as a dropped
// connection for a session that is presumably still alive on the server
// (per spike-notes.md's addendum: tmux only detaches/kills a control-mode
// client when *its own* attached session dies, but a transient disconnect —
// e.g. a killed subprocess, a dropped SSH link in a later change — looks
// identical from here) and attempts to reconnect it.
func (h *HostConn) monitorConn(name string, conn *sessionConn) {
	<-conn.exited

	h.mu.Lock()
	closed := h.closed
	ts, stillTracked := h.sessions[name]
	h.mu.Unlock()
	if closed || !stillTracked || ts.conn != conn {
		return
	}

	h.reconnectSession(name)
}

// reconnectSession spawns a fresh control-mode connection for a session
// whose previous connection died unexpectedly, and — satisfying "Pane
// scrollback recovery after reconnect" — seeds every tracked pane's ring
// buffer with `capture-pane -p -S -` so already-buffered scrollback and the
// current screen are available again, before new output resumes streaming
// on the new connection. If the reattach itself fails (e.g. the session
// actually is gone and this exit was really a session closure racing ahead
// of syncSessions), it gives up quietly; the normal %sessions-changed path
// will clean up the tracked session once it observes the closure.
func (h *HostConn) reconnectSession(name string) {
	conn, err := spawnSessionConn(h.socketArgs, name)
	if err != nil {
		return
	}

	attached := make(chan struct{}, 1)
	h.spawn(func() {
		conn.readLoop(func(n *tmuxcm.Notification) {
			select {
			case attached <- struct{}{}:
			default:
			}
			h.handleNotification(conn, n)
		})
	})

	select {
	case <-attached:
	case <-conn.exited:
		_ = conn.Close()
		return
	case <-time.After(5 * time.Second):
		_ = conn.Close()
		return
	}

	h.mu.Lock()
	ts, ok := h.sessions[name]
	if !ok || h.closed {
		h.mu.Unlock()
		_ = conn.Close()
		return
	}
	oldConn := ts.conn
	delete(h.connSession, oldConn)
	ts.conn = conn
	h.connSession[conn] = name
	if ts.isCtrl {
		h.ctrl = conn
	}
	h.mu.Unlock()

	h.spawn(func() { h.monitorConn(name, conn) })

	h.recoverPaneScrollback(ts.key)
}

// recoverPaneScrollback re-seeds every currently known pane of key's session
// with its current screen and scrollback via `capture-pane -p -S -` (spike
// 1.4's confirmed reconnect strategy — pane content lives on the tmux
// server, independent of any control-mode client's connection state).
func (h *HostConn) recoverPaneScrollback(key domain.SessionKey) {
	s := h.model.Session(key)
	if s == nil {
		return
	}
	for _, w := range s.Windows {
		for paneID := range w.Panes {
			res, err := h.issueAdminCommand(fmt.Sprintf(`capture-pane -p -S - -t "%s"`, paneID))
			if err != nil {
				continue
			}
			data := []byte(strings.Join(res.Lines, "\n"))

			h.mu.Lock()
			rb, ok := h.ringBuffers[paneID]
			if !ok {
				rb = ringbuffer.New(paneRingBufferSize)
				h.ringBuffers[paneID] = rb
			}
			h.mu.Unlock()
			_, _ = rb.Write(data)
		}
	}
}

// discoverWindowsAndPanes seeds the domain model with every window and pane
// of a just-tracked session, via list-windows/list-panes rather than
// waiting for notifications (satisfies "Sessions present before
// connection" and the topology-seeding half of a newly discovered session).
func (h *HostConn) discoverWindowsAndPanes(key domain.SessionKey, sessionName string) error {
	res, err := h.issueAdminCommand(fmt.Sprintf(
		`list-windows -t "%s" -F "#{window_id}|#{window_name}|#{window_layout}|#{window_active}"`, sessionName))
	if err != nil {
		return err
	}

	for _, line := range res.Lines {
		fields := strings.Split(line, "|")
		if len(fields) != 4 {
			continue
		}
		windowID, name, layout, activeStr := fields[0], fields[1], fields[2], fields[3]

		h.model.UpsertWindow(key, &domain.Window{
			ID:     windowID,
			Name:   name,
			Layout: layout,
			Active: activeStr == "1",
		})

		if err := h.refreshWindowPanes(key, windowID, false); err != nil {
			return err
		}
	}
	return nil
}

// paneGeometry returns the position of every pane in windowID's current
// layout (per the domain model's already-stored Layout string for that
// window — see discoverWindowsAndPanes and handleNotification's
// NotifLayoutChange case, both of which update the model's Layout before
// calling refreshWindowPanes), keyed by pane ID. Width/Height are sourced
// from list-panes instead (see refreshWindowPanes), since they are already
// tracked there independent of layout-string parsing.
func (h *HostConn) paneGeometry(key domain.SessionKey, windowID string) map[string]tmuxcm.LayoutNode {
	w := h.model.Window(key, windowID)
	if w == nil || w.Layout == "" {
		return nil
	}
	root := tmuxcm.ParseLayout(w.Layout)
	if root == nil {
		return nil
	}
	geo := make(map[string]tmuxcm.LayoutNode)
	collectPaneGeometry(root, geo)
	return geo
}

func collectPaneGeometry(n *tmuxcm.LayoutNode, geo map[string]tmuxcm.LayoutNode) {
	if n.PaneID != "" {
		geo[n.PaneID] = *n
		return
	}
	for _, c := range n.Children {
		collectPaneGeometry(c, geo)
	}
}

// refreshWindowPanes re-lists the panes of one window and upserts them into
// the domain model. When emitEvents is true, any pane present in the prior
// call's set but absent from this one is inferred dead (no direct
// control-mode notification exists for pane death — see spike-notes.md) and
// a pane.died event is published for it.
func (h *HostConn) refreshWindowPanes(key domain.SessionKey, windowID string, emitEvents bool) error {
	res, err := h.issueAdminCommand(fmt.Sprintf(
		`list-panes -t "%s" -F "#{pane_id}|#{pane_active}|#{pane_width}|#{pane_height}|#{pane_dead}|#{pane_current_command}"`, windowID))
	if err != nil {
		return err
	}

	geo := h.paneGeometry(key, windowID)

	h.mu.Lock()
	oldIDs := h.windowPanes[windowID]
	h.mu.Unlock()

	newIDs := make(map[string]bool, len(res.Lines))
	var newlySeenPaneIDs []string
	for _, line := range res.Lines {
		fields := strings.Split(line, "|")
		if len(fields) != 6 {
			continue
		}
		paneID := fields[0]
		active := fields[1] == "1"
		width, _ := strconv.Atoi(fields[2])
		height, _ := strconv.Atoi(fields[3])
		dead := fields[4] == "1"
		command := fields[5]

		var x, y int
		if g, ok := geo[paneID]; ok {
			x, y = g.X, g.Y
		}

		newIDs[paneID] = true
		if !oldIDs[paneID] {
			newlySeenPaneIDs = append(newlySeenPaneIDs, paneID)
		}
		h.model.UpsertPane(key, windowID, &domain.Pane{
			ID:      paneID,
			Active:  active,
			Dead:    dead,
			X:       x,
			Y:       y,
			Width:   width,
			Height:  height,
			Command: command,
		})
	}

	h.mu.Lock()
	h.windowPanes[windowID] = newIDs
	h.mu.Unlock()

	// Seed each pane seen here for the first time with its current
	// on-server content (screen + scrollback), satisfying "Scrollback shown
	// on first display" (specs/tmux-shell-ui/spec.md) for panes that predate
	// this HostConn's connection (Scenario: "Sessions present before
	// connection") as well as panes just created by a split -- otherwise
	// PaneScrollback would stay empty until the pane happens to produce its
	// own fresh output, since %output-driven ring buffers only start once
	// output actually streams (see xterm-bridge-verification.md's task 6.2
	// finding). Done after releasing h.mu and after the windowPanes update
	// above so this blocking capture-pane round trip per pane can't hold up
	// other callers touching h.mu.
	for _, paneID := range newlySeenPaneIDs {
		h.seedPaneScrollbackIfUnbuffered(paneID)
	}

	if emitEvents {
		for id := range oldIDs {
			if !newIDs[id] {
				h.model.RemovePane(id)
				h.mu.Lock()
				delete(h.ringBuffers, id)
				h.mu.Unlock()
				h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.PaneDied, Payload: id})
			}
		}
	}
	return nil
}

// seedPaneScrollbackIfUnbuffered initializes paneID's ring buffer from a
// fresh `capture-pane -p -S -` snapshot (current screen plus tmux's own
// scrollback history), but only if no ring buffer exists for it yet. The
// existence check -- both before issuing the command and again before
// writing its result -- guards against a race with NotifOutput: if the
// pane's own output already started streaming (and thus already created a
// buffer) by the time this runs or returns, a capture-pane snapshot taken
// now would overlap with what's already buffered, double-counting content
// rather than filling a genuine gap. Mirrors recoverPaneScrollback's
// capture-pane strategy for the reconnect case; this covers the two cases
// that leaves outstanding (pre-existing panes at initial discovery, and
// newly split panes) — see refreshWindowPanes.
func (h *HostConn) seedPaneScrollbackIfUnbuffered(paneID string) {
	h.mu.Lock()
	_, exists := h.ringBuffers[paneID]
	h.mu.Unlock()
	if exists {
		return
	}

	res, err := h.issueAdminCommand(fmt.Sprintf(`capture-pane -p -S - -t "%s"`, paneID))
	if err != nil {
		return
	}
	data := []byte(strings.Join(res.Lines, "\n"))

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.ringBuffers[paneID]; exists {
		return
	}
	rb := ringbuffer.New(paneRingBufferSize)
	h.ringBuffers[paneID] = rb
	_, _ = rb.Write(data)
}

// handleNotification translates one control-mode notification, received on
// conn, into domain model mutations and event bus publications. It is
// invoked from conn's readLoop goroutine, so any follow-up command it needs
// to issue is dispatched on a separate goroutine to avoid deadlocking that
// same readLoop while it waits for the command's response.
func (h *HostConn) handleNotification(conn *sessionConn, n *tmuxcm.Notification) {
	h.mu.Lock()
	sessionName, known := h.connSession[conn]
	h.mu.Unlock()
	key := domain.SessionKey{HostID: h.hostID, Name: sessionName}

	switch n.Type {
	case tmuxcm.NotifOutput:
		if !known {
			return
		}
		h.mu.Lock()
		rb, ok := h.ringBuffers[n.PaneID]
		if !ok {
			rb = ringbuffer.New(paneRingBufferSize)
			h.ringBuffers[n.PaneID] = rb
		}
		h.mu.Unlock()
		_, _ = rb.Write(n.Value)
		h.bus.PublishOutput(eventbus.PaneOutputEvent{PaneID: n.PaneID, Data: n.Value})

	case tmuxcm.NotifLayoutChange:
		if !known {
			return
		}
		h.model.SetWindowLayout(key, n.WindowID, n.Layout)
		h.spawn(func() {
			if err := h.refreshWindowPanes(key, n.WindowID, true); err == nil {
				h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.WindowLayoutChanged, Payload: n.WindowID})
			}
		})

	case tmuxcm.NotifWindowRenamed:
		if !known {
			return
		}
		h.model.SetWindowName(key, n.WindowID, n.Name)
		h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.WindowRenamed, Payload: n.WindowID})

	case tmuxcm.NotifSessionRenamed:
		if !known {
			return
		}
		renamed := h.model.RenameSession(key, n.Name)
		if renamed == nil {
			return
		}
		h.mu.Lock()
		if ts, ok := h.sessions[sessionName]; ok {
			delete(h.sessions, sessionName)
			ts.key = renamed.Key
			ts.name = n.Name
			h.sessions[n.Name] = ts
			h.connSession[conn] = n.Name
			if ts.isCtrl {
				h.ctrlSessionName = n.Name
			}
		}
		h.mu.Unlock()
		h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.SessionRenamed, Payload: renamed.Key})

	case tmuxcm.NotifWindowClose:
		if !known {
			return
		}
		h.model.RemoveWindow(key, n.WindowID)
		h.mu.Lock()
		delete(h.windowPanes, n.WindowID)
		h.mu.Unlock()

	case tmuxcm.NotifWindowAdd:
		if !known {
			return
		}
		h.spawn(func() {
			_ = h.discoverWindow(key, sessionName, n.WindowID)
		})

	case tmuxcm.NotifWindowPaneChanged:
		if !known {
			return
		}
		h.model.SetActivePane(key, n.WindowID, n.PaneID)
		h.bus.PublishLifecycle(eventbus.LifecycleEvent{Type: eventbus.PaneFocusChanged, Payload: n.PaneID})

	case tmuxcm.NotifUnlinkedWindowClose:
		// A killed window that was linked to only one session (the common
		// case) is reported as %unlinked-window-close, not %window-close —
		// confirmed against tmux 3.7b, contrary to this notification's
		// grouping (purely for %sessions-changed re-sync) inherited from the
		// earlier bootstrap-tmux-engine change. Find and remove it from
		// whichever tracked session still has it before re-syncing.
		h.mu.Lock()
		keys := make([]domain.SessionKey, 0, len(h.sessions))
		for _, ts := range h.sessions {
			keys = append(keys, ts.key)
		}
		h.mu.Unlock()
		for _, k := range keys {
			if h.model.Window(k, n.WindowID) != nil {
				h.model.RemoveWindow(k, n.WindowID)
				h.mu.Lock()
				delete(h.windowPanes, n.WindowID)
				h.mu.Unlock()
				break
			}
		}
		h.spawn(func() {
			_ = h.syncSessions(true)
		})

	case tmuxcm.NotifSessionsChanged, tmuxcm.NotifUnlinkedWindowAdd, tmuxcm.NotifUnlinkedWindowRenamed:
		h.spawn(func() {
			_ = h.syncSessions(true)
		})
	}
}

// discoverWindow seeds the domain model for a single newly added window
// (used for %window-add, where only the window ID is known).
func (h *HostConn) discoverWindow(key domain.SessionKey, sessionName, windowID string) error {
	res, err := h.issueAdminCommand(fmt.Sprintf(
		`list-windows -t "%s" -F "#{window_id}|#{window_name}|#{window_layout}|#{window_active}"`, sessionName))
	if err != nil {
		return err
	}
	for _, line := range res.Lines {
		fields := strings.Split(line, "|")
		if len(fields) != 4 || fields[0] != windowID {
			continue
		}
		h.model.UpsertWindow(key, &domain.Window{
			ID:     fields[0],
			Name:   fields[1],
			Layout: fields[2],
			Active: fields[3] == "1",
		})
		return h.refreshWindowPanes(key, windowID, false)
	}
	return nil
}
