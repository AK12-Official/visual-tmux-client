package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

// newTestHTTPServer starts an isolated tmux server and returns a hub server
// wired to it plus an httptest HTTP server and the bearer token.
func newTestHTTPServer(t *testing.T) (*server, *httptest.Server, string) {
	t.Helper()
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	sock := uniqueSocket(t)
	tmux, _ := exec.LookPath("tmux")
	if _, _, code, err := runCommand(tmux, "-L", sock, "start-server"); err != nil || code != 0 {
		t.Fatalf("tmux start-server failed: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { runCommand(tmux, "-L", sock, "kill-server") })

	const token = "test-token"
	s := newServer(&config{addr: "127.0.0.1:0", origin: "http://example.com"}, token)
	s.tmux = &tmuxClient{path: tmux, socket: sock}
	ts := httptest.NewServer(s.handler())
	t.Cleanup(ts.Close)
	return s, ts, token
}

// apiRequest performs an HTTP request against the test server.
func apiRequest(t *testing.T, ts *httptest.Server, method, path, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestHostAddressedRouting(t *testing.T) {
	_, ts, token := newTestHTTPServer(t)
	if resp := apiRequest(t, ts, "GET", "/api/hosts/local/sessions", token, ""); resp.StatusCode != 200 {
		t.Errorf("local: expected 200, got %d", resp.StatusCode)
	}
	resp := apiRequest(t, ts, "GET", "/api/hosts/other/sessions", token, "")
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("other host: expected 404, got %d", resp.StatusCode)
	}
	var e apiError
	decodeJSON(t, resp, &e)
	if e.Error != "host_not_found" {
		t.Fatalf("expected host_not_found, got %q", e.Error)
	}
}

func TestListSessionsEmptyShape(t *testing.T) {
	_, ts, token := newTestHTTPServer(t)
	resp := apiRequest(t, ts, "GET", "/api/hosts/local/sessions", token, "")
	var body struct {
		Sessions []Session `json:"sessions"`
	}
	decodeJSON(t, resp, &body)
	// Verify the raw JSON is `[]` not `null`.
	raw := apiRequest(t, ts, "GET", "/api/hosts/local/sessions", token, "")
	rawBody := readAll(t, raw)
	if !strings.Contains(string(rawBody), `"sessions":[]`) {
		t.Fatalf("expected empty sessions to serialize as [], got %s", rawBody)
	}
	if body.Sessions == nil {
		t.Fatal("expected non-nil empty slice")
	}
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCreateSessionOutcomes(t *testing.T) {
	_, ts, token := newTestHTTPServer(t)

	// explicit name -> 201
	resp := apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{"name":"api"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create explicit: expected 201, got %d", resp.StatusCode)
	}
	var created Session
	decodeJSON(t, resp, &created)
	if created.Name != "api" {
		t.Fatalf("expected name api, got %q", created.Name)
	}

	// empty name -> 201 (generated)
	resp = apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{}`)
	if resp.StatusCode != 201 {
		t.Fatalf("create generated: expected 201, got %d", resp.StatusCode)
	}

	// invalid name -> 400
	resp = apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{"name":"bad:name"}`)
	if resp.StatusCode != 400 {
		t.Fatalf("create invalid: expected 400, got %d", resp.StatusCode)
	}

	// duplicate -> 409
	resp = apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{"name":"api"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("create duplicate: expected 409, got %d", resp.StatusCode)
	}
}

func TestRenameAndKill(t *testing.T) {
	_, ts, token := newTestHTTPServer(t)
	apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{"name":"old"}`)

	// rename -> 200
	resp := apiRequest(t, ts, "PATCH", "/api/hosts/local/sessions/old", token, `{"name":"new"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("rename: expected 200, got %d", resp.StatusCode)
	}

	// rename absent -> 404
	resp = apiRequest(t, ts, "PATCH", "/api/hosts/local/sessions/nope", token, `{"name":"x"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("rename absent: expected 404, got %d", resp.StatusCode)
	}

	// rename onto existing -> 409
	apiRequest(t, ts, "POST", "/api/hosts/local/sessions", token, `{"name":"other"}`)
	resp = apiRequest(t, ts, "PATCH", "/api/hosts/local/sessions/new", token, `{"name":"other"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("rename conflict: expected 409, got %d", resp.StatusCode)
	}

	// kill -> 204
	resp = apiRequest(t, ts, "DELETE", "/api/hosts/local/sessions/new", token, "")
	if resp.StatusCode != 204 {
		t.Fatalf("kill: expected 204, got %d", resp.StatusCode)
	}

	// kill absent -> 404
	resp = apiRequest(t, ts, "DELETE", "/api/hosts/local/sessions/new", token, "")
	if resp.StatusCode != 404 {
		t.Fatalf("kill absent: expected 404, got %d", resp.StatusCode)
	}
}

func TestIssueTicket(t *testing.T) {
	s, ts, token := newTestHTTPServer(t)
	// create a session directly on the client (not via HTTP) so hasSession is true
	if _, err := s.tmux.CreateSession("probe"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	resp := apiRequest(t, ts, "POST", "/api/ws-ticket", token, `{"hostId":"local","session":"probe"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("issue: expected 200, got %d", resp.StatusCode)
	}
	var body struct {
		Ticket    string `json:"ticket"`
		ExpiresAt string `json:"expiresAt"`
	}
	decodeJSON(t, resp, &body)
	if body.Ticket == "" {
		t.Fatal("expected non-empty ticket")
	}

	// absent session -> 404
	resp = apiRequest(t, ts, "POST", "/api/ws-ticket", token, `{"hostId":"local","session":"nope"}`)
	if resp.StatusCode != 404 {
		t.Fatalf("absent session: expected 404, got %d", resp.StatusCode)
	}

	// no auth -> 401
	resp = apiRequest(t, ts, "POST", "/api/ws-ticket", "", `{"hostId":"local","session":"probe"}`)
	if resp.StatusCode != 401 {
		t.Fatalf("no auth: expected 401, got %d", resp.StatusCode)
	}
}

func TestSPAFallback(t *testing.T) {
	_, ts, _ := newTestHTTPServer(t)

	// deep route -> index.html (200, text/html)
	resp := apiRequest(t, ts, "GET", "/some/deep/route", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("deep route: expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("expected text/html, got %q", ct)
	}

	// /api/unknown -> 404, NOT index.html
	resp = apiRequest(t, ts, "GET", "/api/unknown", "", "")
	if resp.StatusCode != 404 {
		t.Fatalf("api unknown: expected 404, got %d", resp.StatusCode)
	}

	// a real root-level file (the embedded guide) serves as itself rather
	// than being swallowed by the SPA fallback
	resp = apiRequest(t, ts, "GET", "/tmux-guide.zh-CN.md", "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("guide: expected 200, got %d", resp.StatusCode)
	}
	if ct = resp.Header.Get("Content-Type"); !strings.Contains(ct, "markdown") {
		t.Fatalf("expected markdown content type, got %q", ct)
	}
	if body := readAll(t, resp); !strings.Contains(string(body), "tmux") {
		t.Fatal("expected guide content, got something else")
	}
}
