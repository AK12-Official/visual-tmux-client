package main

import (
	"errors"
	"testing"
	"time"
)

func TestTicketRedeemOnce(t *testing.T) {
	s := newTicketStore()
	id, _ := s.issue("probe")
	if err := s.redeem(id, "probe"); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if err := s.redeem(id, "probe"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("second redeem: expected ErrTicketInvalid, got %v", err)
	}
}

func TestTicketExpired(t *testing.T) {
	s := newTicketStore()
	base := time.Now()
	s.now = func() time.Time { return base }
	id, _ := s.issue("probe")
	s.now = func() time.Time { return base.Add(ticketTTL + time.Second) }
	if err := s.redeem(id, "probe"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expected ErrTicketExpired, got %v", err)
	}
}

func TestTicketSessionMismatch(t *testing.T) {
	s := newTicketStore()
	id, _ := s.issue("a")
	if err := s.redeem(id, "b"); !errors.Is(err, ErrTicketMismatch) {
		t.Fatalf("expected ErrTicketMismatch, got %v", err)
	}
}

// TestTicketDeletedBeforeValidityCheck verifies a wrong-session redemption still
// consumes the ticket: the ticket is gone regardless of outcome.
func TestTicketDeletedBeforeValidityCheck(t *testing.T) {
	s := newTicketStore()
	id, _ := s.issue("a")
	// redeem for the wrong session -> mismatch, but ticket consumed
	_ = s.redeem(id, "b")
	// redeeming again (even for the right session) fails as invalid/used
	if err := s.redeem(id, "a"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("expected ErrTicketInvalid after consumed ticket, got %v", err)
	}
}

func TestTicketSweep(t *testing.T) {
	s := newTicketStore()
	base := time.Now()
	s.now = func() time.Time { return base }
	s.issue("a")
	s.issue("b")
	if s.len() != 2 {
		t.Fatalf("expected 2 live tickets, got %d", s.len())
	}
	s.now = func() time.Time { return base.Add(ticketTTL + time.Second) }
	s.sweep()
	if s.len() != 0 {
		t.Fatalf("expected 0 tickets after sweep, got %d", s.len())
	}
}

func TestTicketSweepKeepsFresh(t *testing.T) {
	s := newTicketStore()
	base := time.Now()
	s.now = func() time.Time { return base }
	s.issue("a") // expires base + 30s
	s.now = func() time.Time { return base.Add(40 * time.Second) }
	s.issue("b") // expires base + 40s + 30s = base + 70s
	s.sweep()    // now = base + 40s: "a" (base+30s) expired, "b" still fresh
	if s.len() != 1 {
		t.Fatalf("expected 1 fresh ticket to survive sweep, got %d", s.len())
	}
}
