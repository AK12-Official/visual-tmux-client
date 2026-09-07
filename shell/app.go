package main

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"visual-tmux-client/engine/domain"
	"visual-tmux-client/engine/eventbus"
	"visual-tmux-client/engine/tmuxconn"
)

// localHostID identifies the single local tmux server this shell connects
// to. Multi-host support is explicitly deferred (see this change's
// proposal.md), so a single fixed host ID is sufficient for now.
const localHostID domain.HostID = "local"

// paneOutputPayload is the JSON shape emitted to the frontend for the
// "engine:pane-output" Wails event. Data is base64-encoded because a pane's
// raw output is arbitrary bytes (escape sequences, partial UTF-8 sequences
// split across reads, etc.), not guaranteed-valid JSON string content.
type paneOutputPayload struct {
	PaneID string `json:"paneId"`
	Data   string `json:"data"`
}

// lifecyclePayload is the JSON shape emitted to the frontend for the
// "engine:lifecycle" Wails event. Payload passes through whatever the
// eventbus.LifecycleEvent carried (a domain.SessionKey, a bare ID string,
// etc.) — every concrete type used by the engine is already
// JSON-marshalable.
type lifecyclePayload struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// App is the Go-side binding layer between the engine and the JS frontend.
// It owns the engine's domain model and event bus for the lifetime of the
// shell process, bridges the bus's output/lifecycle streams to Wails
// frontend events, and exposes every engine management operation as an
// exported method — Wails generates a JS binding for each one automatically
// since App is registered via options.App.Bind in main.go.
type App struct {
	ctx context.Context

	model *domain.Model
	bus   *eventbus.Bus

	mu   sync.Mutex
	host *tmuxconn.HostConn
}

// NewApp constructs the App with a fresh domain model and event bus. The
// engine connection itself is established later via Connect, once the
// frontend has had a chance to attach its event listeners.
func NewApp() *App {
	return &App{
		model: domain.NewModel(),
		bus:   eventbus.NewBus(),
	}
}

// startup is called when the Wails app starts. It saves the context needed
// for runtime calls and starts the goroutines that bridge the engine event
// bus to Wails frontend events, for the lifetime of the app.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.bridgeOutput()
	go a.bridgeLifecycle()
}

// shutdown is called when the Wails app is closing. It disconnects any
// active host connection so the tmux control-mode subprocess(es) are
// terminated cleanly rather than left running.
func (a *App) shutdown(ctx context.Context) {
	a.mu.Lock()
	host := a.host
	a.host = nil
	a.mu.Unlock()
	if host != nil {
		_ = host.Close()
	}
}

// bridgeOutput forwards every pane.output event published on the bus to the
// frontend as an "engine:pane-output" Wails event, for the lifetime of the
// app (there is exactly one App/Bus pair per process, so this never needs
// to unsubscribe).
func (a *App) bridgeOutput() {
	_, ch := a.bus.SubscribeOutput()
	for evt := range ch {
		runtime.EventsEmit(a.ctx, "engine:pane-output", paneOutputPayload{
			PaneID: evt.PaneID,
			Data:   base64.StdEncoding.EncodeToString(evt.Data),
		})
	}
}

// bridgeLifecycle forwards every lifecycle event published on the bus to
// the frontend as an "engine:lifecycle" Wails event.
func (a *App) bridgeLifecycle() {
	_, ch := a.bus.SubscribeLifecycle()
	for evt := range ch {
		runtime.EventsEmit(a.ctx, "engine:lifecycle", lifecyclePayload{
			Type:    string(evt.Type),
			Payload: evt.Payload,
		})
	}
}

var errNotConnected = errors.New("not connected to a tmux host")

// Connect establishes the engine's connection to a local tmux server.
// socketName is passed through to tmux as `-L <socketName>`; an empty
// string uses tmux's default socket. It is a no-op error if already
// connected — call Disconnect first to switch sockets.
func (a *App) Connect(socketName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.host != nil {
		return errors.New("already connected")
	}
	host, err := tmuxconn.Connect(tmuxconn.Options{
		HostID:     localHostID,
		SocketName: socketName,
	}, a.model, a.bus)
	if err != nil {
		return err
	}
	a.host = host
	return nil
}

// Disconnect tears down the current host connection, if any.
func (a *App) Disconnect() error {
	a.mu.Lock()
	host := a.host
	a.host = nil
	a.mu.Unlock()
	if host == nil {
		return nil
	}
	return host.Close()
}

// IsConnected reports whether a host connection is currently active.
func (a *App) IsConnected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.host != nil
}

// currentHost returns the active host connection, or errNotConnected if
// none is established. Every management method below goes through this so
// a stray call before Connect (or after Disconnect) fails cleanly instead
// of nil-panicking.
func (a *App) currentHost() (*tmuxconn.HostConn, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.host == nil {
		return nil, errNotConnected
	}
	return a.host, nil
}

// Sessions returns a snapshot of every session currently tracked by the
// domain model, for populating the frontend's navigation tree.
func (a *App) Sessions() []*domain.Session {
	return a.model.Sessions()
}

// PaneScrollback returns the given pane's retained raw-byte scrollback,
// base64-encoded for safe JSON transport (see paneOutputPayload's doc
// comment), for seeding an xterm.js instance when a pane is first shown.
func (a *App) PaneScrollback(paneID string) (string, error) {
	host, err := a.currentHost()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(host.PaneScrollback(paneID)), nil
}

// CreateSession creates a new detached tmux session named name.
func (a *App) CreateSession(name string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.CreateSession(name)
}

// KillSession kills the named tmux session.
func (a *App) KillSession(name string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.KillSession(name)
}

// RenameSession renames session name to newName.
func (a *App) RenameSession(name, newName string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.RenameSession(name, newName)
}

// NewWindow creates a new window in the named session.
func (a *App) NewWindow(sessionName string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.NewWindow(sessionName)
}

// KillWindow kills the window identified by windowID.
func (a *App) KillWindow(windowID string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.KillWindow(windowID)
}

// RenameWindow renames the window identified by windowID to newName.
func (a *App) RenameWindow(windowID, newName string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.RenameWindow(windowID, newName)
}

// SplitPane splits the pane identified by paneID, vertically if vertical is
// true or horizontally otherwise.
func (a *App) SplitPane(paneID string, vertical bool) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.SplitPane(paneID, vertical)
}

// KillPane kills the pane identified by paneID.
func (a *App) KillPane(paneID string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.KillPane(paneID)
}

// SelectPane makes the pane identified by paneID the active pane of its
// window.
func (a *App) SelectPane(paneID string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.SelectPane(paneID)
}

// SendKeys forwards data (raw keystrokes/paste text, as produced by
// xterm.js's onData callback) to the pane identified by paneID. data is
// carried as a Go string but treated as a raw byte sequence via its UTF-8
// encoding: every control sequence and character xterm.js emits from
// onData (arrow keys, Ctrl-C, pasted text, ...) already round-trips
// correctly through UTF-8, since escape/control bytes fall in the ASCII
// range and any multi-byte input is already valid UTF-8 text.
func (a *App) SendKeys(paneID string, data string) error {
	host, err := a.currentHost()
	if err != nil {
		return err
	}
	return host.SendKeys(paneID, []byte(data))
}
