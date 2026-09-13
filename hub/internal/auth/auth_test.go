package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConstantTimeEqual(t *testing.T) {
	if !ConstantTimeEqual("secret123", "secret123") {
		t.Errorf("expected identical secrets to match")
	}
	if ConstantTimeEqual("secret123", "secret124") {
		t.Errorf("expected different secrets not to match")
	}
	if ConstantTimeEqual("secret", "secret123") {
		t.Errorf("expected different length secrets not to match")
	}
	if ConstantTimeEqual("", "secret") {
		t.Errorf("expected empty string comparison to return false")
	}
	if ConstantTimeEqual("secret", "") {
		t.Errorf("expected empty string comparison to return false")
	}
	if ConstantTimeEqual("", "") {
		t.Errorf("expected empty string comparison to return false")
	}
}

func TestGenerateBearerToken(t *testing.T) {
	tok1, err := GenerateBearerToken()
	if err != nil {
		t.Fatalf("GenerateBearerToken failed: %v", err)
	}
	if len(tok1) == 0 {
		t.Fatalf("token is empty")
	}
	tok2, err := GenerateBearerToken()
	if err != nil {
		t.Fatalf("GenerateBearerToken failed: %v", err)
	}
	if tok1 == tok2 {
		t.Errorf("two generated tokens should not be identical")
	}
}

func TestTicketLifecycle(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := baseTime
	nowFunc := func() time.Time { return currentTime }

	ttl := 30 * time.Second
	store := NewTicketStore(ttl, nowFunc)

	// 1. Issue and redeem successfully
	id, expiresAt, err := store.Issue("session-1")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	expectedExpiry := baseTime.Add(ttl)
	if !expiresAt.Equal(expectedExpiry) {
		t.Errorf("expected expiry %v, got %v", expectedExpiry, expiresAt)
	}

	if err := store.Redeem(id, "session-1"); err != nil {
		t.Fatalf("Redeem failed: %v", err)
	}

	// Single-use: cannot redeem second time
	if err := store.Redeem(id, "session-1"); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("expected ErrTicketInvalid on second redeem, got: %v", err)
	}

	// 2. Session mismatch
	id2, _, err := store.Issue("session-alpha")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if err := store.Redeem(id2, "session-beta"); !errors.Is(err, ErrTicketMismatch) {
		t.Errorf("expected ErrTicketMismatch, got: %v", err)
	}
	// Verify it was deleted even after mismatch
	if err := store.Redeem(id2, "session-alpha"); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("expected ErrTicketInvalid after failed redeem, got: %v", err)
	}

	// 3. Expiration
	id3, _, err := store.Issue("session-3")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	currentTime = baseTime.Add(ttl + 1*time.Second) // advance clock past TTL
	if err := store.Redeem(id3, "session-3"); !errors.Is(err, ErrTicketExpired) {
		t.Errorf("expected ErrTicketExpired, got: %v", err)
	}

	// 4. Empty ticket ID
	if err := store.Redeem("", "session-any"); !errors.Is(err, ErrTicketInvalid) {
		t.Errorf("expected ErrTicketInvalid for empty ID, got: %v", err)
	}
}

func TestTicketSweep(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := baseTime
	nowFunc := func() time.Time { return currentTime }

	ttl := 10 * time.Second
	store := NewTicketStore(ttl, nowFunc)

	if _, _, err := store.Issue("s1"); err != nil {
		t.Fatal(err)
	}
	currentTime = baseTime.Add(5 * time.Second)
	if _, _, err := store.Issue("s2"); err != nil {
		t.Fatal(err)
	}

	if store.Len() != 2 {
		t.Errorf("expected 2 tickets, got %d", store.Len())
	}

	// Advance clock to base + 11s (s1 expired, s2 still valid)
	currentTime = baseTime.Add(11 * time.Second)
	store.Sweep()

	if store.Len() != 1 {
		t.Errorf("expected 1 live ticket after sweep, got %d", store.Len())
	}

	// Advance clock to base + 20s (s2 also expired)
	currentTime = baseTime.Add(20 * time.Second)
	store.Sweep()

	if store.Len() != 0 {
		t.Errorf("expected 0 tickets after second sweep, got %d", store.Len())
	}
}

func TestRunSweeper(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	currentTime := baseTime
	nowFunc := func() time.Time { return currentTime }

	ttl := 10 * time.Millisecond
	store := NewTicketStore(ttl, nowFunc)
	if _, _, err := store.Issue("s1"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	currentTime = baseTime.Add(20 * time.Millisecond)
	go store.RunSweeper(ctx, 5*time.Millisecond)

	time.Sleep(20 * time.Millisecond)
	if store.Len() != 0 {
		t.Errorf("expected sweeper to remove expired ticket, got %d", store.Len())
	}
}

func TestConstantTimeEqual_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"both empty", "", "", false},
		{"left empty", "", "tok", false},
		{"right empty", "tok", "", false},
		{"identical null bytes", "secret\x00data", "secret\x00data", true},
		{"differ after null byte", "secret\x00data1", "secret\x00data2", false},
		{"long identical", strings.Repeat("x", 1000), strings.Repeat("x", 1000), true},
		{"long diff at end", strings.Repeat("x", 999) + "a", strings.Repeat("x", 999) + "b", false},
		{"long diff at start", "a" + strings.Repeat("x", 999), "b" + strings.Repeat("x", 999), false},
		{"unicode identical", "🔑🔐令牌-token", "🔑🔐令牌-token", true},
		{"unicode mismatch", "🔑🔐令牌-token1", "🔑🔐令牌-token2", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := ConstantTimeEqual(tc.a, tc.b)
			if res != tc.expected {
				t.Errorf("ConstantTimeEqual(%q, %q) = %v; want %v", tc.a, tc.b, res, tc.expected)
			}
		})
	}
}

func TestRandomToken_EdgeCases(t *testing.T) {
	if _, err := RandomToken(0); !errors.Is(err, ErrEmptyCredential) {
		t.Errorf("expected ErrEmptyCredential for n=0, got %v", err)
	}
	if _, err := RandomToken(-10); !errors.Is(err, ErrEmptyCredential) {
		t.Errorf("expected ErrEmptyCredential for n=-10, got %v", err)
	}
	tok, err := RandomToken(64)
	if err != nil {
		t.Fatalf("RandomToken(64) failed: %v", err)
	}
	if strings.Contains(tok, "=") {
		t.Errorf("expected unpadded base64url token, got %q", tok)
	}
}

func TestTicketStore_ConcurrentRedeem(t *testing.T) {
	ttl := 10 * time.Second
	store := NewTicketStore(ttl, nil)
	id, _, err := store.Issue("concurrent-session")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}

	const workers = 50
	var successCount atomic.Int32
	var invalidCount atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rErr := store.Redeem(id, "concurrent-session")
			if rErr == nil {
				successCount.Add(1)
			} else if errors.Is(rErr, ErrTicketInvalid) {
				invalidCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if successCount.Load() != 1 {
		t.Errorf("expected exactly 1 successful redemption, got %d", successCount.Load())
	}
	if invalidCount.Load() != workers-1 {
		t.Errorf("expected %d invalid ticket errors, got %d", workers-1, invalidCount.Load())
	}
	if store.Len() != 0 {
		t.Errorf("expected store to be empty after redemption, got %d", store.Len())
	}
}

func TestTicketStore_ConcurrentOperations(t *testing.T) {
	store := NewTicketStore(100*time.Millisecond, nil)
	var wg sync.WaitGroup
	const iterations = 50

	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sess := fmt.Sprintf("session-%d", idx)
			id, _, err := store.Issue(sess)
			if err != nil {
				return
			}
			if idx%2 == 0 {
				_ = store.Redeem(id, sess) //nolint:errcheck // intentional concurrent stress test
			} else {
				_ = store.Redeem(id, "wrong-sess") //nolint:errcheck // intentional concurrent stress test
			}
			_ = store.Len()
			store.Sweep()
		}(i)
	}
	wg.Wait()
}

func TestTicketStore_ExactExpiryBoundary(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	currentTime := baseTime
	nowFunc := func() time.Time { return currentTime }

	ttl := 10 * time.Second
	store := NewTicketStore(ttl, nowFunc)

	id, expiresAt, err := store.Issue("sess-boundary")
	if err != nil {
		t.Fatal(err)
	}

	currentTime = expiresAt
	if err := store.Redeem(id, "sess-boundary"); err != nil {
		t.Errorf("ticket should be valid at exact expiry boundary, got: %v", err)
	}

	id2, expiresAt2, err := store.Issue("sess-boundary-2")
	if err != nil {
		t.Fatal(err)
	}
	currentTime = expiresAt2.Add(1 * time.Nanosecond)
	if err := store.Redeem(id2, "sess-boundary-2"); !errors.Is(err, ErrTicketExpired) {
		t.Errorf("expected ErrTicketExpired 1ns past expiry, got: %v", err)
	}
}

func TestTicketStore_NilNowFallback(t *testing.T) {
	store := NewTicketStore(5*time.Second, nil)
	id, exp, err := store.Issue("sess-realtime")
	if err != nil {
		t.Fatalf("Issue failed: %v", err)
	}
	if exp.Before(time.Now()) {
		t.Errorf("expiry %v should be in future", exp)
	}
	if err := store.Redeem(id, "sess-realtime"); err != nil {
		t.Errorf("Redeem failed: %v", err)
	}
}
