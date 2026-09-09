package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveTokenFromEnv(t *testing.T) {
	t.Setenv("VISUAL_TMUX_CLIENT_TOKEN", "sekret")
	tok, generated, err := resolveToken()
	if err != nil {
		t.Errorf("resolveToken: %v", err)
	}
	if tok != "sekret" {
		t.Fatalf("expected env token, got %q", tok)
	}
	if generated {
		t.Fatal("an env-provided token must not be reported as generated")
	}
}

func TestResolveTokenGenerates(t *testing.T) {
	t.Setenv("VISUAL_TMUX_CLIENT_TOKEN", "")
	tok, generated, err := resolveToken()
	if err != nil {
		t.Errorf("resolveToken: %v", err)
	}
	if tok == "" {
		t.Fatal("expected generated token")
	}
	if !generated {
		t.Fatal("a token with no env value must be reported as generated")
	}
	// 32 random bytes -> base64url (no padding) is 43 chars.
	if len(tok) != 43 {
		t.Fatalf("expected 43-char token, got %d (%q)", len(tok), tok)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Fatal("expected equal")
	}
	if constantTimeEqual("abc", "abd") {
		t.Fatal("expected not equal")
	}
	if constantTimeEqual("abc", "a") {
		t.Fatal("different lengths should not be equal")
	}
}

func TestRequireAuthMiddleware(t *testing.T) {
	calls := 0
	handler := requireAuth("sekret", func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})

	do := func(authHeader string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}

	if rec := do(""); rec.Code != http.StatusUnauthorized {
		t.Errorf("missing header: expected 401, got %d", rec.Code)
	}
	if rec := do("Bearer wrong"); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", rec.Code)
	}
	if calls != 0 {
		t.Fatalf("handler must not be called on 401; called %d times", calls)
	}
	if rec := do("Bearer sekret"); rec.Code != http.StatusOK {
		t.Errorf("correct token: expected 200, got %d", rec.Code)
	}
	if calls != 1 {
		t.Fatalf("handler should be called once on 200, got %d", calls)
	}
}

func TestCheckOrigin(t *testing.T) {
	if !checkOrigin("", "http://localhost:7690") {
		t.Error("empty origin should be allowed")
	}
	if !checkOrigin("http://localhost:7690", "http://localhost:7690") {
		t.Error("matching origin should be allowed")
	}
	if checkOrigin("http://evil.example", "http://localhost:7690") {
		t.Error("foreign origin should be rejected")
	}
}
