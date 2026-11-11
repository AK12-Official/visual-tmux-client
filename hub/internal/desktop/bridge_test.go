package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "http://127.0.0.1:12345"

func bridgeRequest(b *Bridge, path, body, origin, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, testOrigin+desktopPrefix+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	b.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
	return w
}

func launchBody(t *testing.T, b *Bridge) string {
	t.Helper()
	address, err := b.LaunchURL()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(u.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(map[string]string{"code": fragment.Get("launch")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(address, "test-bearer") {
		t.Fatal("bearer in address")
	}
	return string(encoded)
}

func TestLaunchAuthorization(t *testing.T) {
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	body := launchBody(t, b)
	for _, origin := range []string{"", "null", "http://evil.test", "http://127.0.0.1:12346"} {
		resp := bridgeRequest(b, "bootstrap", body, origin, "")
		if resp.Code != http.StatusForbidden || strings.Contains(resp.Body.String(), "test-bearer") {
			t.Fatalf("foreign bootstrap leaked: %d", resp.Code)
		}
	}
	if resp := bridgeRequest(b, "bootstrap", `{"code":"wrong"}`, testOrigin, ""); resp.Code != http.StatusUnauthorized {
		t.Fatal(resp.Code)
	}
	resp := bridgeRequest(b, "bootstrap", body, testOrigin, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "test-bearer") ||
		resp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange: %d %s", resp.Code, resp.Body)
	}
	if resp := bridgeRequest(b, "bootstrap", body, testOrigin, ""); resp.Code != http.StatusUnauthorized {
		t.Fatal("replayed launch accepted")
	}
	body = launchBody(t, b)
	b.now = func() time.Time { return time.Now().Add(launchLifetime) }
	if resp := bridgeRequest(b, "bootstrap", body, testOrigin, ""); resp.Code != http.StatusUnauthorized {
		t.Fatal("expired launch accepted")
	}
}

func TestNativeHTTPActionBoundary(t *testing.T) {
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	var called atomic.Int32
	b.Register("native", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value string `json:"value"`
		}
		if DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
		called.Add(1)
		WriteJSON(w, map[string]bool{"ok": true})
	}))
	for _, tc := range []struct {
		origin, token string
		want          int
	}{
		{testOrigin, "", http.StatusUnauthorized}, {testOrigin, "wrong", http.StatusUnauthorized},
		{"", "test-bearer", http.StatusForbidden}, {"http://evil.test", "test-bearer", http.StatusForbidden},
		{testOrigin, "test-bearer", http.StatusOK},
	} {
		resp := bridgeRequest(b, "native", `{"value":"ok"}`, tc.origin, tc.token)
		if resp.Code != tc.want {
			t.Fatalf("boundary status: got %d want %d", resp.Code, tc.want)
		}
		if resp.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS enabled")
		}
	}
	if called.Load() != 1 {
		t.Fatal("unauthorized native action invoked")
	}
	bodies := []string{
		`{} {}`, `{"unknown":true}`, `{"value":"` + strings.Repeat("x", bridgeBodyLimit) + `"}`,
	}
	for _, body := range bodies {
		if resp := bridgeRequest(b, "native", body, testOrigin, "test-bearer"); resp.Code != http.StatusBadRequest {
			t.Fatal("invalid body accepted")
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		r := httptest.NewRequest(method, testOrigin+desktopPrefix+"native", nil)
		r.Header.Set("Origin", testOrigin)
		w := httptest.NewRecorder()
		b.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatal("method allowed")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://attacker.test"+desktopPrefix+"native", strings.NewReader(`{}`))
	r.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	b.Wrap(http.NotFoundHandler()).ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("foreign Host allowed")
	}
}

func TestConcurrentRedemptionAndSupersededCode(t *testing.T) {
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	old := launchBody(t, b)
	body := launchBody(t, b)
	if bridgeRequest(b, "bootstrap", old, testOrigin, "").Code != http.StatusUnauthorized {
		t.Fatal("superseded code accepted")
	}
	var successes atomic.Int32
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if bridgeRequest(b, "bootstrap", body, testOrigin, "").Code == http.StatusOK {
				successes.Add(1)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("redemptions: %d", successes.Load())
	}
}
