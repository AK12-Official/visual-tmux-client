// Package domain defines the engine's tmux domain model: Host > Session >
// Window > Pane, plus a thread-safe registry (Model) for querying and
// mutating that hierarchy as control-mode events arrive.
package domain

import (
	"fmt"
	"maps"
	"sync"
)

// HostID identifies a Host. For local connections this is an arbitrary
// stable identifier chosen by the caller (e.g. the socket path).
type HostID string

// HostStatus describes the reachability of a Host's underlying transport
// (local socket, or later SSH), independent of any individual Session's
// control-mode subprocess state.
type HostStatus int

const (
	HostStatusUnknown HostStatus = iota
	HostStatusConnected
	HostStatusDisconnected
	HostStatusDegraded
)

// Host wraps a connection to a tmux server, local or (in a later change)
// remote via SSH.
type Host struct {
	ID      HostID
	Address string
	Status  HostStatus
}

// SessionKey uniquely identifies a Session across all hosts. tmux session
// names are only unique per-server, so the key is compound from the start.
type SessionKey struct {
	HostID HostID
	Name   string
}

func (k SessionKey) String() string {
	return fmt.Sprintf("%s/%s", k.HostID, k.Name)
}

// Session mirrors a tmux session's windows.
type Session struct {
	Key     SessionKey
	ID      string // tmux session_id, e.g. "$0"
	Windows map[string]*Window // keyed by tmux window_id, e.g. "@1"
}

// Window mirrors a tmux window's panes.
type Window struct {
	ID         string // tmux window_id, e.g. "@1"
	SessionKey SessionKey
	Name       string
	Layout     string
	Active     bool
	Panes      map[string]*Pane // keyed by tmux pane_id, e.g. "%3"
}

// Pane mirrors a single tmux pane.
type Pane struct {
	ID         string // tmux pane_id, e.g. "%3"
	WindowID   string
	SessionKey SessionKey
	Active     bool
	Dead       bool
	X          int // position within its window's layout, in terminal cells
	Y          int
	Width      int
	Height     int
	Command    string
}

// paneLocation records where a pane lives, for O(1) lookup by bare pane ID
// (control-mode events like %output identify panes only by ID, not by
// session/window).
type paneLocation struct {
	sessionKey SessionKey
	windowID   string
}

// Model is a thread-safe registry of the engine's current view of every
// connected host's tmux state.
type Model struct {
	mu sync.RWMutex

	hosts    map[HostID]*Host
	sessions map[SessionKey]*Session
	panes    map[string]paneLocation // pane_id -> location, for direct lookup
}

// NewModel returns an empty Model.
func NewModel() *Model {
	return &Model{
		hosts:    make(map[HostID]*Host),
		sessions: make(map[SessionKey]*Session),
		panes:    make(map[string]paneLocation),
	}
}

// UpsertHost adds or replaces a Host.
func (m *Model) UpsertHost(h *Host) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hosts[h.ID] = h
}

// Host returns the Host with the given ID, or nil if not found.
func (m *Model) Host(id HostID) *Host {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hosts[id]
}

// RemoveHost removes a Host by ID. It does not cascade to sessions; callers
// are expected to remove sessions explicitly as they are closed.
func (m *Model) RemoveHost(id HostID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hosts, id)
}

// UpsertSession adds or replaces a Session. If the session already exists,
// its Windows map is preserved unless the caller has set one explicitly.
func (m *Model) UpsertSession(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.Windows == nil {
		s.Windows = make(map[string]*Window)
	}
	m.sessions[s.Key] = s
}

// Session returns a snapshot of the Session for the given key, or nil if not
// found. The snapshot's Windows map (and each window's Panes map) is copied,
// so ranging over it is safe even while further structural mutations
// (UpsertWindow, UpsertPane, ...) happen concurrently on the live model —
// see snapshotSession.
func (m *Model) Session(key SessionKey) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[key]
	if !ok {
		return nil
	}
	return snapshotSession(s)
}

// Sessions returns a snapshot slice of all currently tracked sessions (see
// Session's doc comment on what "snapshot" guarantees here).
func (m *Model) Sessions() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, snapshotSession(s))
	}
	return out
}

// snapshotWindow copies w and its Panes map (but not the *Pane values
// themselves, which are never mutated in place after creation — see
// UpsertPane) so a caller can range over the result while the live window's
// Panes map is concurrently mutated.
func snapshotWindow(w *Window) *Window {
	cp := *w
	cp.Panes = make(map[string]*Pane, len(w.Panes))
	maps.Copy(cp.Panes, w.Panes)
	return &cp
}

// snapshotSession copies s, its Windows map, and (via snapshotWindow) each
// window's Panes map, isolating the result from concurrent structural
// mutation on the live model.
func snapshotSession(s *Session) *Session {
	cp := *s
	cp.Windows = make(map[string]*Window, len(s.Windows))
	for id, w := range s.Windows {
		cp.Windows[id] = snapshotWindow(w)
	}
	return &cp
}

// RemoveSession removes a Session and all of its windows/panes, including
// their entries in the pane lookup index.
func (m *Model) RemoveSession(key SessionKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	for _, w := range s.Windows {
		for paneID := range w.Panes {
			delete(m.panes, paneID)
		}
	}
	delete(m.sessions, key)
}

// RenameSession moves the Session stored under oldKey to a new key with the
// same HostID and Name newName, updating the SessionKey stamped on every one
// of its windows and panes (including their pane-index entries) to match.
// Returns the renamed Session, or nil if oldKey is not tracked.
func (m *Model) RenameSession(oldKey SessionKey, newName string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[oldKey]
	if !ok {
		return nil
	}
	delete(m.sessions, oldKey)

	newKey := SessionKey{HostID: oldKey.HostID, Name: newName}
	ns := *s
	ns.Key = newKey
	ns.Windows = make(map[string]*Window, len(s.Windows))
	for id, w := range s.Windows {
		nw := *w
		nw.SessionKey = newKey
		nw.Panes = make(map[string]*Pane, len(w.Panes))
		for pid, p := range w.Panes {
			np := *p
			np.SessionKey = newKey
			nw.Panes[pid] = &np
			m.panes[pid] = paneLocation{sessionKey: newKey, windowID: id}
		}
		ns.Windows[id] = &nw
	}
	m.sessions[newKey] = &ns
	return &ns
}

// UpsertWindow adds or replaces a Window under the session identified by
// key. It is a no-op if the session does not exist.
func (m *Model) UpsertWindow(key SessionKey, w *Window) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	if w.Panes == nil {
		w.Panes = make(map[string]*Pane)
	}
	w.SessionKey = key
	s.Windows[w.ID] = w
}

// Window returns a snapshot of the Window with the given ID under the
// session identified by key, or nil if not found (see Session's doc comment
// on what "snapshot" guarantees here).
func (m *Model) Window(key SessionKey, windowID string) *Window {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[key]
	if !ok {
		return nil
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return nil
	}
	return snapshotWindow(w)
}

// SetWindowName updates the Name of the window identified by key/windowID,
// replacing the stored Window with a fresh copy rather than mutating it in
// place, so a Window a caller obtained earlier via Window/Session is
// unaffected. No-op if the window is not tracked.
func (m *Model) SetWindowName(key SessionKey, windowID, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return
	}
	nw := *w
	nw.Name = name
	s.Windows[windowID] = &nw
}

// SetWindowLayout is the Layout analogue of SetWindowName.
func (m *Model) SetWindowLayout(key SessionKey, windowID, layout string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return
	}
	nw := *w
	nw.Layout = layout
	s.Windows[windowID] = &nw
}

// RemoveWindow removes a Window and its panes' pane-index entries.
func (m *Model) RemoveWindow(key SessionKey, windowID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return
	}
	for paneID := range w.Panes {
		delete(m.panes, paneID)
	}
	delete(s.Windows, windowID)
}

// SetActivePane marks the pane identified by windowID/paneID as the active
// pane of its window, clearing Active on every other pane of that window.
// No-op if the session, window, or pane is not tracked.
func (m *Model) SetActivePane(key SessionKey, windowID, paneID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return
	}
	if _, ok := w.Panes[paneID]; !ok {
		return
	}
	for id, p := range w.Panes {
		if p.Active == (id == paneID) {
			continue
		}
		np := *p
		np.Active = id == paneID
		w.Panes[id] = &np
	}
}

// UpsertPane adds or replaces a Pane under the given session/window, and
// records its location in the pane index for direct lookup by ID.
func (m *Model) UpsertPane(key SessionKey, windowID string, p *Pane) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[key]
	if !ok {
		return
	}
	w, ok := s.Windows[windowID]
	if !ok {
		return
	}
	p.SessionKey = key
	p.WindowID = windowID
	w.Panes[p.ID] = p
	m.panes[p.ID] = paneLocation{sessionKey: key, windowID: windowID}
}

// Pane looks up a Pane directly by its tmux pane ID, without the caller
// needing to know its session/window — this matches how control-mode
// events (e.g. %output) identify panes.
func (m *Model) Pane(paneID string) *Pane {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loc, ok := m.panes[paneID]
	if !ok {
		return nil
	}
	s, ok := m.sessions[loc.sessionKey]
	if !ok {
		return nil
	}
	w, ok := s.Windows[loc.windowID]
	if !ok {
		return nil
	}
	return w.Panes[paneID]
}

// RemovePane removes a Pane from its window and the pane index.
func (m *Model) RemovePane(paneID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	loc, ok := m.panes[paneID]
	if !ok {
		return
	}
	if s, ok := m.sessions[loc.sessionKey]; ok {
		if w, ok := s.Windows[loc.windowID]; ok {
			delete(w.Panes, paneID)
		}
	}
	delete(m.panes, paneID)
}
