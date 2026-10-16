package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

type spySessionService struct {
	mockSessionService
	killedSessions []string
}

func (s *spySessionService) KillSession(ctx context.Context, name string) error {
	s.killedSessions = append(s.killedSessions, name)
	return s.mockSessionService.KillSession(ctx, name)
}

func TestHostAdministrationAuthentication(t *testing.T) {
	registry, err := host.NewRegistry(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	router := NewRouter(RouterConfig{
		Token: "secret-token",
		Hosts: registry,
	}, &mockSessionService{}, nil, nil, nil)

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/hosts", ""},
		{http.MethodPost, "/api/hosts", `{"id":"r1","name":"Remote 1"}`},
		{http.MethodGet, "/api/hosts/r1", ""},
		{http.MethodPut, "/api/hosts/r1", `{"revision":1,"name":"Updated"}`},
		{http.MethodDelete, "/api/hosts/r1", ""},
	}

	for _, rt := range routes {
		t.Run(fmt.Sprintf("%s %s without auth", rt.method, rt.path), func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
		})

		t.Run(fmt.Sprintf("%s %s with wrong auth", rt.method, rt.path), func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, bytes.NewBufferString(rt.body))
			req.Header.Set("Authorization", "Bearer wrong-token")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
		})
	}
}

func TestHostCRUDAndLocalIdentity(t *testing.T) {
	ctx := context.Background()
	regDir := t.TempDir()
	registry, err := host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	var changedHosts []host.ID
	sessSpy := &spySessionService{
		mockSessionService: mockSessionService{
			sessions: []session.Session{{Name: "active-session", Windows: 1}},
		},
	}

	cfg := RouterConfig{
		Token: "test-token",
		Hosts: registry,
		OnHostChanged: func(id host.ID) {
			changedHosts = append(changedHosts, id)
		},
	}
	router := NewRouter(cfg, sessSpy, nil, nil, nil)

	authedRequest := func(method, path string, body any) *http.Response {
		var reqBody []byte
		if body != nil {
			var err error
			reqBody, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Result()
	}

	// 1. Initial list should contain local
	resp := authedRequest(http.MethodGet, "/api/hosts", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var listResp struct {
		Hosts []host.Host `json:"hosts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Hosts) != 1 || listResp.Hosts[0].ID != host.LocalID {
		t.Fatalf("expected 1 host with ID local, got %+v", listResp.Hosts)
	}

	// 2. Cannot modify local host
	resp = authedRequest(http.MethodPost, "/api/hosts", map[string]any{
		"id":   "local",
		"name": "Modified Local",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST local: expected 400, got %d", resp.StatusCode)
	}

	resp = authedRequest(http.MethodPut, "/api/hosts/local", map[string]any{
		"revision": 1,
		"name":     "Modified Local",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("PUT local: expected 400, got %d", resp.StatusCode)
	}

	resp = authedRequest(http.MethodDelete, "/api/hosts/local", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("DELETE local: expected 400, got %d", resp.StatusCode)
	}

	// 3. Unknown host rejection
	resp = authedRequest(http.MethodGet, "/api/hosts/unknown", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET unknown: expected 404, got %d", resp.StatusCode)
	}

	resp = authedRequest(http.MethodPut, "/api/hosts/unknown", map[string]any{
		"revision": 1,
		"name":     "New",
		"address":  "127.0.0.1",
		"username": "user",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("PUT unknown: expected 404, got %d", resp.StatusCode)
	}

	resp = authedRequest(http.MethodDelete, "/api/hosts/unknown?revision=1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE unknown: expected 404, got %d", resp.StatusCode)
	}

	// 4. Create remote host with password
	resp = authedRequest(http.MethodPost, "/api/hosts", map[string]any{
		"id":             "remote-1",
		"name":           "Remote Server",
		"address":        "192.168.1.50",
		"port":           22,
		"username":       "alice",
		"credentialType": "password",
		"credentials": map[string]string{
			"password": "super-secret-password",
		},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create host: expected 201, got %d", resp.StatusCode)
	}
	var created host.Host
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID != "remote-1" || created.Revision != 1 || !created.HasCredential {
		t.Fatalf("created host mismatch: %+v", created)
	}

	// Verify persistence: restart registry
	if err := registry.Close(); err != nil {
		t.Fatal(err)
	}
	registry, err = host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	cfg.Hosts = registry
	router = NewRouter(cfg, sessSpy, nil, nil, nil)

	// Verify GET returns persisted host and does not expose secrets
	resp = authedRequest(http.MethodGet, "/api/hosts/remote-1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET remote-1: expected 200, got %d", resp.StatusCode)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	respStr := buf.String()
	if bytes.Contains([]byte(respStr), []byte("super-secret-password")) {
		t.Fatal("response exposed password!")
	}

	// 5. Revision checks on update
	// Missing revision -> 400
	resp = authedRequest(http.MethodPut, "/api/hosts/remote-1", map[string]any{
		"name":    "Renamed",
		"address": "192.168.1.50",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update without revision: expected 400, got %d", resp.StatusCode)
	}

	// Stale revision -> 409
	resp = authedRequest(http.MethodPut, "/api/hosts/remote-1", map[string]any{
		"revision": 99,
		"name":     "Renamed",
		"address":  "192.168.1.50",
		"username": "alice",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("update with stale revision: expected 409, got %d", resp.StatusCode)
	}

	// Correct revision -> 200 with credential retention
	resp = authedRequest(http.MethodPut, "/api/hosts/remote-1", map[string]any{
		"revision": 1,
		"name":     "Renamed Remote",
		"address":  "192.168.1.50",
		"username": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid update: expected 200, got %d", resp.StatusCode)
	}
	var updated host.Host
	if err := json.NewDecoder(resp.Body).Decode(&updated); err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.HasCredential || updated.Name != "Renamed Remote" {
		t.Fatalf("updated host mismatch: %+v", updated)
	}

	// Check credentials still exist in registry after retention
	_, creds, err := registry.GetCredentials(ctx, "remote-1")
	if err != nil {
		t.Fatal(err)
	}
	if creds.Password != "super-secret-password" {
		t.Fatalf("credentials were lost or corrupted: %+v", creds)
	}

	// 6. Delete host with revision check
	// Stale revision -> 409
	resp = authedRequest(http.MethodDelete, "/api/hosts/remote-1?revision=1", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete with stale revision: expected 409, got %d", resp.StatusCode)
	}

	// Matching revision -> 204
	resp = authedRequest(http.MethodDelete, "/api/hosts/remote-1?revision=2", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", resp.StatusCode)
	}

	// Ensure onHostChanged was called for "remote-1"
	foundChanged := false
	for _, id := range changedHosts {
		if id == "remote-1" {
			foundChanged = true
			break
		}
	}
	if !foundChanged {
		t.Fatalf("onHostChanged was not called for remote-1: %v", changedHosts)
	}

	// Ensure sessions were NOT terminated (removal without session termination)
	if len(sessSpy.killedSessions) != 0 {
		t.Fatalf("sessions were killed on host deletion: %v", sessSpy.killedSessions)
	}

	// Ensure host is now gone
	resp = authedRequest(http.MethodGet, "/api/hosts/remote-1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET deleted host: expected 404, got %d", resp.StatusCode)
	}
}
