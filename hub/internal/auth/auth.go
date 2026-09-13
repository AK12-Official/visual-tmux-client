package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// Standard entropy lengths for generated tokens.
const (
	BearerTokenEntropyBytes = 32
	TicketEntropyBytes      = 24
)

// ErrEmptyCredential indicates that a provided credential or expected token is empty.
var ErrEmptyCredential = errors.New("empty credential")

// RandomToken returns n random bytes encoded as base64url without padding.
func RandomToken(n int) (string, error) {
	if n <= 0 {
		return "", ErrEmptyCredential
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateBearerToken generates a secure random bearer token.
func GenerateBearerToken() (string, error) {
	return RandomToken(BearerTokenEntropyBytes)
}

// ConstantTimeEqual compares two secrets without leaking length or content through timing.
// Hashing first makes the comparison length-independent, since subtle.ConstantTimeCompare
// returns immediately on a length mismatch.
func ConstantTimeEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
