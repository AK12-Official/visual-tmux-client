package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

type mockSessionService struct {
	sessions []session.Session
	listErr  error
	createFn func(name string) (*session.Session, error)
	renameFn func(oldName, newName string) error
	killFn   func(name string) error
	calls    int
}

func (m *mockSessionService) ListSessions(ctx context.Context) ([]session.Session, error) {
	m.calls++
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.sessions, nil
}

func (m *mockSessionService) CreateSession(ctx context.Context, name string) (*session.Session, error) {
	m.calls++
	if m.createFn != nil {
		return m.createFn(name)
	}
	sess := session.Session{Name: name, Windows: 1}
	m.sessions = append(m.sessions, sess)
	return &sess, nil
}

func (m *mockSessionService) RenameSession(ctx context.Context, oldName, newName string) error {
	m.calls++
	if m.renameFn != nil {
		return m.renameFn(oldName, newName)
	}
	for i := range m.sessions {
		if m.sessions[i].Name == oldName {
			m.sessions[i].Name = newName
			return nil
		}
	}
	return session.ErrNotFound
}

func (m *mockSessionService) KillSession(ctx context.Context, name string) error {
	m.calls++
	if m.killFn != nil {
		return m.killFn(name)
	}
	for i := range m.sessions {
		if m.sessions[i].Name == name {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			return nil
		}
	}
	return session.ErrNotFound
}

func (m *mockSessionService) GetSession(ctx context.Context, name string) (*session.Session, error) {
	for i := range m.sessions {
		if m.sessions[i].Name == name {
			return &m.sessions[i], nil
		}
	}
	return nil, session.ErrNotFound
}

func (m *mockSessionService) HasSession(ctx context.Context, name string) bool {
	s, err := m.GetSession(ctx, name)
	return err == nil && s != nil
}

type mockTicketIssuer struct {
	issueFn func(session string) (string, time.Time, error)
}

func (m *mockTicketIssuer) Issue(session string) (string, time.Time, error) {
	if m.issueFn != nil {
		return m.issueFn(session)
	}
	return "mock-ticket", time.Now().Add(time.Minute), nil
}

func testRouterConfig(token string, staticFS fstest.MapFS) RouterConfig {
	return RouterConfig{
		Token:               token,
		MaxRequestBodyBytes: 64 * 1024,
		WebConfig: config.WebConfig{
			SessionPollInterval: config.Duration(5 * time.Second),
			ActivityDecay:       config.Duration(2 * time.Second),
			ActivityThrottle:    config.Duration(500 * time.Millisecond),
			ResizeDebounce:      config.Duration(100 * time.Millisecond),
			Reconnect: config.ReconnectConfig{
				InitialDelay: config.Duration(500 * time.Millisecond),
				MaxDelay:     config.Duration(3 * time.Second),
			},
			Terminal: config.WebTerminalConfig{
				Scrollback:  5000,
				FontSize:    13,
				MinFontSize: 8,
				MaxFontSize: 24,
			},
			Notifications: config.NotificationsConfig{
				MaxToasts:       5,
				ErrorLifetime:   config.Duration(8 * time.Second),
				WarningLifetime: config.Duration(5 * time.Second),
				InfoLifetime:    config.Duration(3 * time.Second),
			},
		},
		StaticFS: staticFS,
	}
}

func TestAuthMiddleware(t *testing.T) {
	svc := &mockSessionService{}
	router := NewRouter(testRouterConfig("valid-secret", nil), svc, &mockTicketIssuer{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth header, got %d", rec.Code)
	}
	if svc.calls != 0 {
		t.Fatalf("service must not be invoked on 401, calls=%d", svc.calls)
	}

	reqBad := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	reqBad.Header.Set("Authorization", "Bearer wrong-secret")
	recBad := httptest.NewRecorder()
	router.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong auth header, got %d", recBad.Code)
	}

	reqGood := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	reqGood.Header.Set("Authorization", "Bearer valid-secret")
	recGood := httptest.NewRecorder()
	router.ServeHTTP(recGood, reqGood)
	if recGood.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid auth header, got %d", recGood.Code)
	}
	if svc.calls != 1 {
		t.Fatalf("service must be invoked once on 200, calls=%d", svc.calls)
	}
}

func TestSessionEndpoints(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "alpha", Windows: 1}},
	}
	router := NewRouter(testRouterConfig("tok", nil), svc, &mockTicketIssuer{}, nil)

	// 1. List
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// 2. Create valid
	body, err := json.Marshal(map[string]string{"name": "beta"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/hosts/local/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	// 2b. Create nameless (empty JSON body {})
	var receivedName string
	svc.createFn = func(name string) (*session.Session, error) {
		receivedName = name
		return &session.Session{Name: "session-auto-1", Windows: 1}, nil
	}
	req = httptest.NewRequest(http.MethodPost, "/api/hosts/local/sessions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for nameless create, got %d", rec.Code)
	}
	if receivedName != "" {
		t.Errorf("expected empty name for nameless create, got %q", receivedName)
	}
	svc.createFn = nil

	// 3. Create invalid name
	svc.createFn = func(name string) (*session.Session, error) {
		return nil, session.ErrInvalidName
	}
	req = httptest.NewRequest(http.MethodPost, "/api/hosts/local/sessions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	svc.createFn = nil

	// 4. Rename
	renameBody, err := json.Marshal(map[string]string{"name": "gamma"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPatch, "/api/hosts/local/sessions/beta", bytes.NewReader(renameBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on rename, got %d", rec.Code)
	}

	// 5. Kill
	req = httptest.NewRequest(http.MethodDelete, "/api/hosts/local/sessions/gamma", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 on kill, got %d", rec.Code)
	}

	// 6. Host not found
	req = httptest.NewRequest(http.MethodGet, "/api/hosts/remote/sessions", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on invalid host, got %d", rec.Code)
	}
}

func TestWSTicketEndpoint(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "active-session", Windows: 1}},
	}
	router := NewRouter(testRouterConfig("tok", nil), svc, &mockTicketIssuer{}, nil)

	// Valid ticket request
	reqBody, err := json.Marshal(map[string]string{"hostId": "local", "session": "active-session"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", bytes.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Nonexistent session
	badReq, err := json.Marshal(map[string]string{"hostId": "local", "session": "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/ws-ticket", bytes.NewReader(badReq))
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestClientConfigEndpoint(t *testing.T) {
	svc := &mockSessionService{}
	router := NewRouter(testRouterConfig("secret-token", nil), svc, &mockTicketIssuer{}, nil)

	// Unauthenticated request
	req := httptest.NewRequest(http.MethodGet, "/api/client-config", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /api/client-config, got %d", rec.Code)
	}
	if svc.calls != 0 {
		t.Fatalf("must not invoke session service or tmux for /api/client-config, calls=%d", svc.calls)
	}

	cacheControl := rec.Header().Get("Cache-Control")
	if cacheControl != "no-store" {
		t.Errorf("expected Cache-Control: no-store, got %q", cacheControl)
	}

	var conf PublicClientConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &conf); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if conf.Version != 1 {
		t.Errorf("expected version 1, got %d", conf.Version)
	}
	if conf.Web.SessionPollInterval != 5000 {
		t.Errorf("expected session_poll_interval 5000ms, got %d", conf.Web.SessionPollInterval)
	}
	if conf.Web.Terminal.Scrollback != 5000 {
		t.Errorf("expected scrollback 5000, got %d", conf.Web.Terminal.Scrollback)
	}

	// Ensure sensitive fields are not in JSON
	bodyStr := rec.Body.String()
	if bytes.Contains([]byte(bodyStr), []byte("secret-token")) {
		t.Fatal("secret token leaked in public client config!")
	}
}

func TestStaticSPAHandler(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/bundle.12345.js": &fstest.MapFile{Data: []byte("console.log('test');")},
		"tmux-guide.md":          &fstest.MapFile{Data: []byte("# Tmux Guide")},
	}

	router := NewRouter(testRouterConfig("tok", mockFS), &mockSessionService{}, &mockTicketIssuer{}, nil)

	// 1. Assets immutable caching
	req := httptest.NewRequest(http.MethodGet, "/assets/bundle.12345.js", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for assets, got %d", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("expected immutable cache header, got %s", rec.Header().Get("Cache-Control"))
	}

	// 2. Markdown MIME type
	req = httptest.NewRequest(http.MethodGet, "/tmux-guide.md", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for guide, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Errorf("expected text/markdown; charset=utf-8, got %s", rec.Header().Get("Content-Type"))
	}

	// 3. SPA fallback for frontend path
	req = httptest.NewRequest(http.MethodGet, "/sessions/my-session", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for SPA fallback, got %d", rec.Code)
	}
	if rec.Body.String() != "<html>SPA</html>" {
		t.Errorf("expected SPA index.html content, got %s", rec.Body.String())
	}

	// 4. Unknown API/WS route MUST return 404 (not index.html)
	req = httptest.NewRequest(http.MethodGet, "/api/unknown-route", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown api path, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/ws/unknown-ws", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown ws path, got %d", rec.Code)
	}
}

func TestMapSessionErrors(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
		wantMsg    string
	}{
		{session.ErrInvalidName, http.StatusBadRequest, "invalid session name"},
		{session.ErrNameInUse, http.StatusConflict, "name_in_use"},
		{session.ErrNotFound, http.StatusNotFound, "session_not_found"},
		{session.ErrTmuxNotFound, http.StatusServiceUnavailable, "tmux_unavailable"},
		{errors.New("generic error"), http.StatusInternalServerError, "generic error"},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		mapSessionError(rec, tt.err)
		if rec.Code != tt.wantStatus {
			t.Errorf("err %v: want status %d, got %d", tt.err, tt.wantStatus, rec.Code)
		}
	}
}

func TestPathTraversalProtection(t *testing.T) {
	mockFS := fstest.MapFS{
		"index.html":             &fstest.MapFile{Data: []byte("<html>SPA</html>")},
		"assets/bundle.12345.js": &fstest.MapFile{Data: []byte("console.log('test');")},
		"secret.txt":             &fstest.MapFile{Data: []byte("topsecret")},
	}

	router := NewRouter(testRouterConfig("tok", mockFS), &mockSessionService{}, &mockTicketIssuer{}, nil)

	traversalPaths := []string{
		"/../secret.txt",
		"/assets/../secret.txt",
		"/../../secret.txt",
		"/assets/../../secret.txt",
		"/..%2fsecret.txt",
		"/%2e%2e/secret.txt",
		"/./secret.txt",
	}

	for _, p := range traversalPaths {
		t.Run("path_"+p, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if strings.Contains(rec.Body.String(), "topsecret") {
				t.Fatalf("path traversal leaked secret content on %s: %s", p, rec.Body.String())
			}
		})
	}
}

func TestStaticSPAHandlerMissingIndex(t *testing.T) {
	mockFS := fstest.MapFS{
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1);")},
	}
	handler := NewStaticSPAHandler(mockFS)

	req := httptest.NewRequest(http.MethodGet, "/unknown-page", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 when index.html is missing, got %d", rec.Code)
	}
}

func TestContentTypeFor(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"doc.md", "text/markdown; charset=utf-8"},
		{"GUIDE.MD", "text/markdown; charset=utf-8"},
		{"app.js", "text/javascript; charset=utf-8"},
		{"style.css", "text/css; charset=utf-8"},
		{"index.html", "text/html; charset=utf-8"},
		{"logo.svg", "image/svg+xml"},
		{"unknown.binxyz", "application/octet-stream"},
	}

	for _, tc := range tests {
		ct := ContentTypeFor(tc.filename)
		if !strings.HasPrefix(ct, strings.Split(tc.expected, ";")[0]) {
			t.Errorf("ContentTypeFor(%q) = %q; want prefix %q", tc.filename, ct, tc.expected)
		}
	}
}

func TestAuthHeaderEdgeCases(t *testing.T) {
	svc := &mockSessionService{}

	emptyTokenRouter := NewRouter(testRouterConfig("", nil), svc, &mockTicketIssuer{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	emptyTokenRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with empty server token, got %d", rec.Code)
	}

	router := NewRouter(testRouterConfig("supersecret", nil), svc, &mockTicketIssuer{}, nil)

	badHeaders := []string{
		"",
		"Basic dXNlcjpwYXNz",
		"Bearer",
		"Bearer ",
		"bearer supersecret",
		"Bearer supersecret ",
		"Bearer wrongsecret",
		"Token supersecret",
		"CustomScheme supersecret",
	}

	for _, h := range badHeaders {
		t.Run("header_"+h, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
			if h != "" {
				r.Header.Set("Authorization", h)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 for header %q, got %d", h, w.Code)
			}
		})
	}
}

func TestMalformedJSONBodies(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "sess1", Windows: 1}},
	}
	router := NewRouter(testRouterConfig("tok", nil), svc, &mockTicketIssuer{}, nil)

	syntaxErrors := []struct {
		name string
		body string
	}{
		{"truncated", `{"name":`},
		{"trailing comma", `{"name": "sess",}`},
		{"non-object array", `[1, 2, 3]`},
		{"plain string", `"plain text"`},
	}

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/hosts/local/sessions"},
		{http.MethodPatch, "/api/hosts/local/sessions/sess1"},
		{http.MethodPost, "/api/ws-ticket"},
	}

	for _, ep := range endpoints {
		for _, tc := range syntaxErrors {
			t.Run(ep.path+"_"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(ep.method, ep.path, strings.NewReader(tc.body))
				req.Header.Set("Authorization", "Bearer tok")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("expected 400 for %s on %s, got %d: %s",
						tc.name, ep.path, rec.Code, rec.Body.String())
				}
			})
		}
	}

	// Type mismatch tests for specific structs
	typeMismatchTests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create_type_error", http.MethodPost, "/api/hosts/local/sessions", `{"name": 12345}`},
		{"rename_type_error", http.MethodPatch, "/api/hosts/local/sessions/sess1", `{"name": 12345}`},
		{"ticket_type_error_session", http.MethodPost, "/api/ws-ticket", `{"hostId": "local", "session": 123}`},
		{"ticket_type_error_host", http.MethodPost, "/api/ws-ticket", `{"hostId": 123, "session": "sess1"}`},
	}

	for _, tc := range typeMismatchTests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer tok")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for %s, got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPayloadSizeLimits(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "test-sess", Windows: 1}},
	}
	cfg := testRouterConfig("tok", nil)
	cfg.MaxRequestBodyBytes = 128
	router := NewRouter(cfg, svc, &mockTicketIssuer{}, nil)

	oversizedPayload := fmt.Sprintf(`{"name": %q}`, strings.Repeat("A", 256))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/hosts/local/sessions",
		strings.NewReader(oversizedPayload),
	)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on oversized create body, got %d", rec.Code)
	}

	req2 := httptest.NewRequest(
		http.MethodPatch,
		"/api/hosts/local/sessions/test-sess",
		strings.NewReader(oversizedPayload),
	)
	req2.Header.Set("Authorization", "Bearer tok")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on oversized rename body, got %d", rec2.Code)
	}

	oversizedTicket := fmt.Sprintf(`{"hostId":"local","session":%q}`, strings.Repeat("B", 256))
	req3 := httptest.NewRequest(
		http.MethodPost,
		"/api/ws-ticket",
		strings.NewReader(oversizedTicket),
	)
	req3.Header.Set("Authorization", "Bearer tok")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on oversized ticket body, got %d", rec3.Code)
	}
}
