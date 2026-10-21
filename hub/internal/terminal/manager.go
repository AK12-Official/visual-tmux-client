package terminal

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrManagerClosing is returned when an attachment attempts to register while the manager is shutting down.
var ErrManagerClosing = errors.New("terminal manager is closing")

// ErrHostClosed is returned when an attachment attempts to register for a host that has been closed or removed.
var ErrHostClosed = errors.New("host is closed or removed")

// Manager manages the set of active terminal attachments and coordinates shutdown.
type Manager struct {
	mu              sync.Mutex
	attachments     map[*Attachment]struct{}
	hostEpochs      map[string]uint64
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
		hostEpochs:      make(map[string]uint64),
		shutdownTimeout: shutdownTimeout,
	}
}

// CurrentEpoch returns the current generation epoch for a host ID.
func (m *Manager) CurrentEpoch(hostID string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hostEpochs[hostID]
}

// Register registers an active attachment. If the manager is already closing,
// or if the host was closed after the attachment's epoch was captured,
// it rejects registration and returns an error.
func (m *Manager) Register(a *Attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return ErrManagerClosing
	}
	if a.hostID != "" && a.epoch != m.hostEpochs[a.hostID] {
		return ErrHostClosed
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

// CloseHost closes all active attachments for a specific host ID and increments its epoch,
// atomically invalidating any in-flight registrations for this host.
func (m *Manager) CloseHost(hostID string) {
	m.mu.Lock()
	m.hostEpochs[hostID]++
	var list []*Attachment
	for a := range m.attachments {
		if a.hostID == hostID {
			list = append(list, a)
		}
	}
	m.mu.Unlock()

	for _, a := range list {
		_ = a.ForceClose() //nolint:errcheck // Best-effort forced close during host invalidation
	}
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
