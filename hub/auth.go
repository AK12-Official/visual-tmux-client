package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// randomToken returns n random bytes encoded as base64url (no padding). Used
// for both the shared bearer token (32 bytes) and single-use tickets (24).
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// resolveToken returns the shared bearer token: VISUAL_TMUX_CLIENT_TOKEN if set,
// otherwise a freshly generated 32-byte token printed once to stderr. The hub
// never serves without a token.
func resolveToken() (string, error) {
	if tok := os.Getenv("VISUAL_TMUX_CLIENT_TOKEN"); tok != "" {
		return tok, nil
	}
	tok, err := randomToken(32)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "visual-tmux-client: generated token: %s\n", tok)
	return tok, nil
}

// constantTimeEqual compares two secrets without leaking length or content
// through timing. Hashing first makes the comparison length-independent, since
// subtle.ConstantTimeCompare returns immediately on a length mismatch.
func constantTimeEqual(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}

// bearerToken extracts the token from an Authorization: Bearer header, or "".
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

// requireAuth returns middleware enforcing the shared bearer token. A request
// that fails authentication never reaches the wrapped handler, so no tmux
// process is ever spawned on a 401.
func requireAuth(expected string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !constantTimeEqual(bearerToken(r), expected) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// checkOrigin reports whether a WebSocket upgrade's Origin header is
// acceptable. Requests without an Origin (non-browser clients such as curl and
// CLI websocket tools) are allowed; a declared origin must equal the configured
// public origin exactly, so a page on an unrelated site cannot open a terminal.
func checkOrigin(origin, configured string) bool {
	if origin == "" {
		return true
	}
	return origin == configured
}
