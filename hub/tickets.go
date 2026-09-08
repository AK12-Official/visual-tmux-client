package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Ticket redemption outcomes. Each is distinguishable so the WebSocket handler
// can send an accurate error message.
var (
	ErrTicketInvalid  = errors.New("ticket invalid or already used")
	ErrTicketExpired  = errors.New("ticket expired")
	ErrTicketMismatch = errors.New("ticket does not match session")
)

// ticketTTL is how long a single-use ticket remains valid (30 seconds).
const ticketTTL = 30 * time.Second

type ticket struct {
	session string
	expires time.Time
}

// ticketStore is an in-memory, single-use, TTL-bounded ticket store. All state
// is disposable; nothing is persisted.
type ticketStore struct {
	mu   sync.Mutex
	byID map[string]ticket
	now  func() time.Time // injectable clock for tests
}

func newTicketStore() *ticketStore {
	return &ticketStore{
		byID: make(map[string]ticket),
		now:  time.Now,
	}
}

// issue creates a single-use ticket bound to session, returning its id and
// expiry time.
func (s *ticketStore) issue(session string) (id string, expiresAt time.Time) {
	id, _ = randomToken(24)
	expiresAt = s.now().Add(ticketTTL)
	s.mu.Lock()
	s.byID[id] = ticket{session: session, expires: expiresAt}
	s.mu.Unlock()
	return id, expiresAt
}

// redeem consumes a ticket. It deletes the ticket from the store BEFORE
// checking validity, so a replay can never succeed even if the first redeem
// failed for any reason (single-use regardless of outcome).
func (s *ticketStore) redeem(id, session string) error {
	s.mu.Lock()
	t, ok := s.byID[id]
	delete(s.byID, id)
	s.mu.Unlock()

	if !ok {
		return ErrTicketInvalid
	}
	if s.now().After(t.expires) {
		return ErrTicketExpired
	}
	if t.session != session {
		return ErrTicketMismatch
	}
	return nil
}

// sweep removes expired tickets.
func (s *ticketStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, t := range s.byID {
		if now.After(t.expires) {
			delete(s.byID, id)
		}
	}
}

// len returns the number of live tickets (for tests).
func (s *ticketStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// runSweeper periodically discards expired tickets until ctx is cancelled.
func (s *ticketStore) runSweeper(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep()
		}
	}
}
