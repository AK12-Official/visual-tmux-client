package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// A session entry carries an optional pane summary. It must be absent from the
// wire when there is nothing to summarise, so the "no live pane" case and any
// older client look exactly as they did before this feature.
func TestSessionListSerialisesOptionalPaneSummary(t *testing.T) {
	svc := &mockSessionService{sessions: []session.Session{
		{
			Name: "with-summary", Windows: 2, Attached: 1, Created: 1700000000,
			Pane: &session.PaneSummary{
				WindowName:     "zsh",
				Title:          "editor",
				CurrentCommand: "nvim",
				WindowActive:   true,
			},
		},
		{Name: "without-summary", Windows: 1, Attached: 0, Created: 1700000001},
	}}
	router := NewRouter(testRouterConfig("tok", nil), svc, nil, &mockTicketIssuer{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/hosts/local/sessions", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(body.Sessions))
	}

	// The fields the list already relied on must be untouched.
	for _, key := range []string{"name", "windows", "attached", "created"} {
		if _, ok := body.Sessions[0][key]; !ok {
			t.Errorf("session is missing %q", key)
		}
	}

	paneRaw, ok := body.Sessions[0]["pane"]
	if !ok {
		t.Fatalf("expected the first session to carry a pane summary")
	}
	var pane map[string]any
	if err := json.Unmarshal(paneRaw, &pane); err != nil {
		t.Fatalf("decode pane summary: %v", err)
	}
	for _, key := range []string{"window_name", "title", "current_command", "window_active"} {
		if _, ok := pane[key]; !ok {
			t.Errorf("pane summary is missing %q", key)
		}
	}
	if pane["current_command"] != "nvim" {
		t.Errorf("current_command = %v, want nvim", pane["current_command"])
	}
	if pane["window_active"] != true {
		t.Errorf("window_active = %v, want true", pane["window_active"])
	}

	if _, ok := body.Sessions[1]["pane"]; ok {
		t.Errorf("a session without a summary must omit the field entirely")
	}
}

func TestSessionKillExpectedConnectionRevisionConflict(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "sess-to-kill", Windows: 1}},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target:   host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Sessions: svc,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// 1. Matching revision via query parameter -> 204 No Content
	req1 := httptest.NewRequest(
		http.MethodDelete,
		"/api/hosts/remote-box/sessions/sess-to-kill?expected_connection_revision=3",
		nil,
	)
	req1.Header.Set("Authorization", "Bearer tok")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for matching query param, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Re-add session for subsequent tests
	svc.sessions = []session.Session{{Name: "sess-to-kill", Windows: 1}}

	// 2. Mismatched revision via query parameter -> 409 Conflict
	req2 := httptest.NewRequest(
		http.MethodDelete,
		"/api/hosts/remote-box/sessions/sess-to-kill?expected_connection_revision=2",
		nil,
	)
	req2.Header.Set("Authorization", "Bearer tok")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict || !strings.Contains(rec2.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for mismatched query param, got %d: %s",
			rec2.Code, rec2.Body.String(),
		)
	}

	// 3. Matching revision via X-Expected-Connection-Revision header -> 204 No Content
	req3 := httptest.NewRequest(http.MethodDelete, "/api/hosts/remote-box/sessions/sess-to-kill", nil)
	req3.Header.Set("Authorization", "Bearer tok")
	req3.Header.Set("X-Expected-Connection-Revision", "3")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for matching header, got %d: %s", rec3.Code, rec3.Body.String())
	}

	svc.sessions = []session.Session{{Name: "sess-to-kill", Windows: 1}}

	// 4. Mismatched revision via X-Expected-Connection-Revision header -> 409 Conflict
	req4 := httptest.NewRequest(http.MethodDelete, "/api/hosts/remote-box/sessions/sess-to-kill", nil)
	req4.Header.Set("Authorization", "Bearer tok")
	req4.Header.Set("X-Expected-Connection-Revision", "2")
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusConflict || !strings.Contains(rec4.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for mismatched header, got %d: %s",
			rec4.Code, rec4.Body.String(),
		)
	}
}

func TestSessionCreateExpectedConnectionRevisionConflict(t *testing.T) {
	svc := &mockSessionService{}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target:   host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Sessions: svc,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// 1. Matching revision via JSON body -> 200 OK
	bodyJSON := `{"name":"sess-1","expectedConnectionRevision":3}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/hosts/remote-box/sessions", strings.NewReader(bodyJSON))
	req1.Header.Set("Authorization", "Bearer tok")
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("expected 201 for matching body revision, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. Mismatched revision via JSON body -> 409 Conflict
	bodyBad := `{"name":"sess-2","expectedConnectionRevision":2}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/hosts/remote-box/sessions", strings.NewReader(bodyBad))
	req2.Header.Set("Authorization", "Bearer tok")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict || !strings.Contains(rec2.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for mismatched body revision, got %d: %s",
			rec2.Code, rec2.Body.String(),
		)
	}

	// 3. Matching revision via header -> 201 Created
	req3 := httptest.NewRequest(
		http.MethodPost,
		"/api/hosts/remote-box/sessions",
		strings.NewReader(`{"name":"sess-3"}`),
	)
	req3.Header.Set("Authorization", "Bearer tok")
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Expected-Connection-Revision", "3")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("expected 201 for matching header revision, got %d: %s", rec3.Code, rec3.Body.String())
	}

	// 4. Mismatched revision via query parameter -> 409 Conflict
	req4 := httptest.NewRequest(
		http.MethodPost,
		"/api/hosts/remote-box/sessions?expected_connection_revision=1",
		strings.NewReader(`{"name":"sess-4"}`),
	)
	req4.Header.Set("Authorization", "Bearer tok")
	req4.Header.Set("Content-Type", "application/json")
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusConflict || !strings.Contains(rec4.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for mismatched query revision, got %d: %s",
			rec4.Code, rec4.Body.String(),
		)
	}
}

func TestSessionRenameExpectedConnectionRevisionConflict(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "sess-old", Windows: 1}},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target:   host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Sessions: svc,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// 1. Matching revision via JSON body -> 200 OK
	bodyJSON := `{"name":"sess-new","expectedConnectionRevision":3}`
	req1 := httptest.NewRequest(
		http.MethodPatch,
		"/api/hosts/remote-box/sessions/sess-old",
		strings.NewReader(bodyJSON),
	)
	req1.Header.Set("Authorization", "Bearer tok")
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 for matching body revision, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Reset session name
	svc.sessions = []session.Session{{Name: "sess-old", Windows: 1}}

	// 2. Mismatched revision via JSON body -> 409 Conflict
	bodyBad := `{"name":"sess-new","expectedConnectionRevision":1}`
	req2 := httptest.NewRequest(
		http.MethodPatch,
		"/api/hosts/remote-box/sessions/sess-old",
		strings.NewReader(bodyBad),
	)
	req2.Header.Set("Authorization", "Bearer tok")
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict || !strings.Contains(rec2.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for mismatched body revision, got %d: %s",
			rec2.Code, rec2.Body.String(),
		)
	}

	// 3. Stale revision passed via both query and header (reproducing R4-1 empirical finding)
	req3 := httptest.NewRequest(
		http.MethodPatch,
		"/api/hosts/remote-box/sessions/sess-old?expected_connection_revision=1",
		strings.NewReader(`{"name":"sess-new"}`),
	)
	req3.Header.Set("Authorization", "Bearer tok")
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Expected-Connection-Revision", "1")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusConflict || !strings.Contains(rec3.Body.String(), "host_revision_conflict") {
		t.Fatalf(
			"expected 409 host_revision_conflict for query+header stale revision, got %d: %s",
			rec3.Code, rec3.Body.String(),
		)
	}
}

func TestExpectedConnectionRevisionInputValidation(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "sess-1", Windows: 1}},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target:   host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Sessions: svc,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// 1. Malformed query parameter -> 400 Bad Request
	req1 := httptest.NewRequest(
		http.MethodDelete,
		"/api/hosts/remote-box/sessions/sess-1?expected_connection_revision=abc",
		nil,
	)
	req1.Header.Set("Authorization", "Bearer tok")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusBadRequest || !strings.Contains(rec1.Body.String(), "invalid_request") {
		t.Fatalf("expected 400 invalid_request for malformed query param, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. Malformed header -> 400 Bad Request
	req2 := httptest.NewRequest(http.MethodDelete, "/api/hosts/remote-box/sessions/sess-1", nil)
	req2.Header.Set("Authorization", "Bearer tok")
	req2.Header.Set("X-Expected-Connection-Revision", "not-a-number")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest || !strings.Contains(rec2.Body.String(), "invalid_request") {
		t.Fatalf("expected 400 invalid_request for malformed header, got %d: %s", rec2.Code, rec2.Body.String())
	}

	// 3. Conflicting values between query and header -> 400 Bad Request
	req3 := httptest.NewRequest(
		http.MethodDelete,
		"/api/hosts/remote-box/sessions/sess-1?expected_connection_revision=2",
		nil,
	)
	req3.Header.Set("Authorization", "Bearer tok")
	req3.Header.Set("X-Expected-Connection-Revision", "3")
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusBadRequest || !strings.Contains(rec3.Body.String(), "invalid_request") {
		t.Fatalf(
			"expected 400 invalid_request for conflicting query and header, got %d: %s",
			rec3.Code, rec3.Body.String(),
		)
	}

	// 4. Conflicting values between body and header -> 400 Bad Request
	req4 := httptest.NewRequest(
		http.MethodPatch,
		"/api/hosts/remote-box/sessions/sess-1",
		strings.NewReader(`{"name":"sess-renamed","expectedConnectionRevision":2}`),
	)
	req4.Header.Set("Authorization", "Bearer tok")
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("X-Expected-Connection-Revision", "3")
	rec4 := httptest.NewRecorder()
	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusBadRequest || !strings.Contains(rec4.Body.String(), "invalid_request") {
		t.Fatalf(
			"expected 400 invalid_request for conflicting body and header, got %d: %s",
			rec4.Code, rec4.Body.String(),
		)
	}

	// 5. Invalid query param paired with expired header -> must return 400, not execute deletion!
	req5 := httptest.NewRequest(
		http.MethodDelete,
		"/api/hosts/remote-box/sessions/sess-1?expected_connection_revision=invalid",
		nil,
	)
	req5.Header.Set("Authorization", "Bearer tok")
	req5.Header.Set("X-Expected-Connection-Revision", "1") // expired header (runtime is 3)
	rec5 := httptest.NewRecorder()
	router.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusBadRequest || !strings.Contains(rec5.Body.String(), "invalid_request") {
		t.Fatalf(
			"expected 400 invalid_request when invalid query pairs with header, got %d: %s",
			rec5.Code, rec5.Body.String(),
		)
	}
	// Verify session was NOT deleted
	if len(svc.sessions) != 1 {
		t.Fatalf("session must not be deleted when request has invalid query, got len %d", len(svc.sessions))
	}
}
