package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
	router := NewRouter(testRouterConfig("tok", nil), svc, &mockTicketIssuer{}, nil)

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
