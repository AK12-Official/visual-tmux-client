package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Ticket errors returned on redemption failures.
var (
	ErrTicketInvalid  = errors.New("ticket invalid or already used")
	ErrTicketExpired  = errors.New("ticket expired")
	ErrTicketMismatch = errors.New("ticket does not match session")
)

type ticket struct {
	session string
	expires time.Time
}

// TicketStore manages in-memory single-use, TTL-bounded tickets.
type TicketStore struct {
	mu   sync.Mutex
	byID map[string]ticket
	ttl  time.Duration
	now  func() time.Time
}

// NewTicketStore constructs a TicketStore with configurable TTL and time source.
func NewTicketStore(ttl time.Duration, now func() time.Time) *TicketStore {
	if now == nil {
		now = time.Now
	}
	return &TicketStore{
		byID: make(map[string]ticket),
		ttl:  ttl,
		now:  now,
	}
}

// Issue creates a single-use ticket bound to session, returning its ID and expiry.
func (s *TicketStore) Issue(session string) (string, time.Time, error) {
	id, err := RandomToken(TicketEntropyBytes)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issue ticket token: %w", err)
	}
	expiresAt := s.now().Add(s.ttl)

	s.mu.Lock()
	s.byID[id] = ticket{session: session, expires: expiresAt}
	s.mu.Unlock()

	return id, expiresAt, nil
}

// Redeem single-use consumes a ticket. It deletes the ticket BEFORE checking validity,
// preventing replay attacks even if the check fails.
func (s *TicketStore) Redeem(id, session string) error {
	if id == "" {
		return ErrTicketInvalid
	}

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

// Sweep removes expired tickets from memory.
func (s *TicketStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, t := range s.byID {
		if now.After(t.expires) {
			delete(s.byID, id)
		}
	}
}

// Len returns the number of active tickets (primarily for tests).
func (s *TicketStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// RunSweeper periodically sweeps expired tickets until ctx is canceled.
func (s *TicketStore) RunSweeper(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep()
		}
	}
}
