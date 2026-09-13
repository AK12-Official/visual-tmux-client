package terminal

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrManagerClosing is returned when an attachment attempts to register while the manager is shutting down.
var ErrManagerClosing = errors.New("terminal manager is closing")

// Manager manages the set of active terminal attachments and coordinates shutdown.
type Manager struct {
	mu              sync.Mutex
	attachments     map[*Attachment]struct{}
	closing         bool
	shutdownTimeout time.Duration
}

const defaultShutdownTimeout = 3 * time.Second

// NewManager constructs a Manager with the specified attachment shutdown timeout.
func NewManager(shutdownTimeout time.Duration) *Manager {
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	return &Manager{
		attachments:     make(map[*Attachment]struct{}),
		shutdownTimeout: shutdownTimeout,
	}
}

// Register registers an active attachment. If the manager is already closing,
// it rejects registration and returns ErrManagerClosing.
func (m *Manager) Register(a *Attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return ErrManagerClosing
	}
	m.attachments[a] = struct{}{}
	return nil
}

// Untrack unregisters an attachment when it terminates.
func (m *Manager) Untrack(a *Attachment) {
	m.mu.Lock()
	delete(m.attachments, a)
	m.mu.Unlock()
}

// ActiveCount returns the current count of active attachments.
func (m *Manager) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.attachments)
}

// Shutdown closes all active attachments within the shutdown timeout.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	list := make([]*Attachment, 0, len(m.attachments))
	for a := range m.attachments {
		list = append(list, a)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, a := range list {
		wg.Add(1)
		go func(att *Attachment) {
			defer wg.Done()
			att.Shutdown()
		}(a)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(m.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		for _, a := range list {
			_ = a.ForceClose() //nolint:errcheck // Best-effort forced close during shutdown
		}
		return nil
	case <-ctx.Done():
		for _, a := range list {
			_ = a.ForceClose() //nolint:errcheck // Best-effort forced close during shutdown
		}
		return ctx.Err()
	}
}
