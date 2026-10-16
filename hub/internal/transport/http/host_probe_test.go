package http

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

type spyProbeDialer struct {
	calls int
	key   ssh.PublicKey
	err   error
}

func (s *spyProbeDialer) Dial(ctx context.Context, address string, port uint16, timeout time.Duration) (ssh.PublicKey, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.key, nil
}

type mockCapabilitiesChecker struct {
	caps map[host.ID]host.HostCapabilities
	err  error
}

func (m *mockCapabilitiesChecker) CheckCapabilities(ctx context.Context, id host.ID) (host.HostCapabilities, error) {
	if m.err != nil {
		return host.HostCapabilities{}, m.err
	}
	if c, ok := m.caps[id]; ok {
		return c, nil
	}
	return host.HostCapabilities{}, host.ErrNotFound
}

func generateTestPubKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestUnauthenticatedEndpointsInitiateNoRemoteWork(t *testing.T) {
	spyDialer := &spyProbeDialer{}
	mockCaps := &mockCapabilitiesChecker{}
	registry, err := host.NewRegistry(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	router := NewRouter(RouterConfig{
		Token:        "secret-token",
		Hosts:        registry,
		Capabilities: mockCaps,
		ProbeDialer:  spyDialer.Dial,
	}, &mockSessionService{}, nil, nil, nil)

	routes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/hosts/probe", `{"address":"1.2.3.4","port":22}`},
		{http.MethodPost, "/api/hosts/r1/probe", ""},
		{http.MethodPost, "/api/hosts/r1/trust", `{"challengeToken":"abc"}`},
		{http.MethodGet, "/api/hosts/r1/capabilities", ""},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", w.Code)
			}
			if spyDialer.calls != 0 {
				t.Fatalf("unauthenticated request initiated remote work (dialer called %d times)", spyDialer.calls)
			}
		})
	}
}

func TestPublicClientConfigContainsNoHostTopology(t *testing.T) {
	router := NewRouter(RouterConfig{
		Token: "secret-token",
	}, &mockSessionService{}, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/client-config", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "hosts") || strings.Contains(body, "ssh") || strings.Contains(body, "token") {
		t.Fatalf("client config leaked host topology or secrets: %s", body)
	}
}

func TestProbeAndTrustEndpoints(t *testing.T) {
	ctx := context.Background()
	pubKey := generateTestPubKey(t)
	spyDialer := &spyProbeDialer{key: pubKey}

	registry, err := host.NewRegistry(ctx, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	var invalidated []host.ID
	router := NewRouter(RouterConfig{
		Token: "admin-token",
		Hosts: registry,
		OnHostChanged: func(id host.ID) {
			invalidated = append(invalidated, id)
		},
		ProbeDialer: spyDialer.Dial,
	}, &mockSessionService{}, nil, nil, nil)

	authedRequest := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody []byte
		if body != nil {
			reqBody, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer admin-token")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 1. Probe draft endpoint
	w := authedRequest(http.MethodPost, "/api/hosts/probe", map[string]any{
		"address": "192.168.1.100",
		"port":    22,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("probe draft failed: %d body=%s", w.Code, w.Body.String())
	}
	var probeRes host.ProbeResult
	if err := json.NewDecoder(w.Body).Decode(&probeRes); err != nil {
		t.Fatal(err)
	}
	if probeRes.ChallengeToken == "" || probeRes.Fingerprint == "" || probeRes.KeyType == "" {
		t.Fatalf("incomplete probe result: %+v", probeRes)
	}

	// 2. Enroll host with challenge token
	w = authedRequest(http.MethodPost, "/api/hosts", map[string]any{
		"id":             "host-alpha",
		"name":           "Alpha Server",
		"address":        "192.168.1.100",
		"port":           22,
		"username":       "admin",
		"credentialType": "password",
		"credentials": map[string]string{
			"password": "secret-password",
		},
		"challengeToken": probeRes.ChallengeToken,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("enroll host failed: %d body=%s", w.Code, w.Body.String())
	}
	var created host.Host
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !created.Trusted || !created.HasCredential {
		t.Fatalf("host should be trusted with credentials: %+v", created)
	}

	// 3. Challenge token is single-use: enrolling again with same token must fail
	w = authedRequest(http.MethodPost, "/api/hosts", map[string]any{
		"id":             "host-beta",
		"name":           "Beta Server",
		"address":        "192.168.1.100",
		"port":           22,
		"username":       "admin",
		"challengeToken": probeRes.ChallengeToken,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict for consumed token, got %d", w.Code)
	}

	// 4. Create an untrusted host, then probe and confirm trust
	w = authedRequest(http.MethodPost, "/api/hosts", map[string]any{
		"id":             "host-gamma",
		"name":           "Gamma Server",
		"address":        "192.168.1.101",
		"port":           2222,
		"username":       "gamma",
		"credentialType": "password",
		"credentials":    map[string]string{"password": "pass"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create gamma failed: %d", w.Code)
	}
	var gamma host.Host
	_ = json.NewDecoder(w.Body).Decode(&gamma)
	if gamma.Trusted {
		t.Fatal("gamma should start untrusted")
	}

	// Probe existing host
	w = authedRequest(http.MethodPost, "/api/hosts/host-gamma/probe", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("probe host-gamma failed: %d body=%s", w.Code, w.Body.String())
	}
	var gammaProbe host.ProbeResult
	_ = json.NewDecoder(w.Body).Decode(&gammaProbe)

	// Confirm trust with valid token
	w = authedRequest(http.MethodPost, "/api/hosts/host-gamma/trust", map[string]any{
		"challengeToken": gammaProbe.ChallengeToken,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("confirm trust failed: %d body=%s", w.Code, w.Body.String())
	}
	var trustedGamma host.Host
	_ = json.NewDecoder(w.Body).Decode(&trustedGamma)
	if !trustedGamma.Trusted {
		t.Fatalf("gamma should now be trusted: %+v", trustedGamma)
	}

	// Verify onHostChanged was called
	found := false
	for _, id := range invalidated {
		if id == "host-gamma" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("onHostChanged not called for host-gamma: %v", invalidated)
	}

	// Cannot probe or trust local
	w = authedRequest(http.MethodPost, "/api/hosts/local/probe", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for probe local, got %d", w.Code)
	}
	w = authedRequest(http.MethodPost, "/api/hosts/local/trust", map[string]any{"challengeToken": "abc"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for trust local, got %d", w.Code)
	}

	// Unknown host probe/trust returns 404
	w = authedRequest(http.MethodPost, "/api/hosts/nonexistent/probe", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	w = authedRequest(http.MethodPost, "/api/hosts/nonexistent/trust", map[string]any{"challengeToken": "abc"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}

	// Probe dial failure returns 503 probe_failed
	spyDialer.err = errors.New("connection refused")
	w = authedRequest(http.MethodPost, "/api/hosts/probe", map[string]any{
		"address": "192.168.1.200",
		"port":    22,
	})
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "probe_failed") {
		t.Fatalf("expected 503 probe_failed, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHostCapabilitiesAndRemoteAuthFailure(t *testing.T) {
	ctx := context.Background()
	registry, err := host.NewRegistry(ctx, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()

	mockCaps := &mockCapabilitiesChecker{
		caps: map[host.ID]host.HostCapabilities{
			"h-good": {
				HostID:   "h-good",
				Revision: 1,
				Online:   true,
				SSH:      host.CapabilityStatus{Available: true},
				Tmux:     host.CapabilityStatus{Available: true},
				SFTP:     host.CapabilityStatus{Available: true},
			},
			"h-nosftp": {
				HostID:   "h-nosftp",
				Revision: 1,
				Online:   true,
				SSH:      host.CapabilityStatus{Available: true},
				Tmux:     host.CapabilityStatus{Available: true},
				SFTP:     host.CapabilityStatus{Available: false, Error: "sftp_unavailable"},
			},
			"h-badauth": {
				HostID:   "h-badauth",
				Revision: 1,
				Online:   false,
				SSH:      host.CapabilityStatus{Available: false, Error: "authentication_failed"},
				Tmux:     host.CapabilityStatus{Available: false, Error: "ssh_unavailable"},
				SFTP:     host.CapabilityStatus{Available: false, Error: "ssh_unavailable"},
				Error:    "authentication_failed",
			},
			"h-offline": {
				HostID:   "h-offline",
				Revision: 1,
				Online:   false,
				SSH:      host.CapabilityStatus{Available: false, Error: "host_unavailable"},
				Tmux:     host.CapabilityStatus{Available: false, Error: "ssh_unavailable"},
				SFTP:     host.CapabilityStatus{Available: false, Error: "ssh_unavailable"},
				Error:    "host_unavailable",
			},
		},
	}

	router := NewRouter(RouterConfig{
		Token:        "admin-token",
		Hosts:        registry,
		Capabilities: mockCaps,
	}, &mockSessionService{}, nil, nil, nil)

	authedRequest := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 1. Local capabilities
	w := authedRequest("/api/hosts/local/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("local capabilities: %d", w.Code)
	}
	var localCaps host.HostCapabilities
	_ = json.NewDecoder(w.Body).Decode(&localCaps)
	if !localCaps.Online || !localCaps.SSH.Available || !localCaps.Tmux.Available {
		t.Fatalf("local caps mismatch: %+v", localCaps)
	}

	// 2. Good remote host
	w = authedRequest("/api/hosts/h-good/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("h-good: %d", w.Code)
	}

	// 3. Missing SFTP does NOT disable tmux
	w = authedRequest("/api/hosts/h-nosftp/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("h-nosftp: %d", w.Code)
	}
	var noSftpCaps host.HostCapabilities
	_ = json.NewDecoder(w.Body).Decode(&noSftpCaps)
	if !noSftpCaps.Online || !noSftpCaps.Tmux.Available || noSftpCaps.SFTP.Available {
		t.Fatalf("expected tmux available and sftp unavailable: %+v", noSftpCaps)
	}

	// 4. Remote authentication failure must NOT return HTTP 401!
	// Browser central token is valid, so HTTP status must be 200 OK with capability error details.
	w = authedRequest("/api/hosts/h-badauth/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("remote auth failure returned HTTP %d (must NOT be 401): body=%s", w.Code, w.Body.String())
	}
	var badAuthCaps host.HostCapabilities
	_ = json.NewDecoder(w.Body).Decode(&badAuthCaps)
	if badAuthCaps.Online || badAuthCaps.SSH.Available || badAuthCaps.SSH.Error != "authentication_failed" {
		t.Fatalf("expected authentication_failed in capabilities: %+v", badAuthCaps)
	}

	// 5. Offline host does NOT return 500 or 401; returns 200 with online: false
	w = authedRequest("/api/hosts/h-offline/capabilities")
	if w.Code != http.StatusOK {
		t.Fatalf("offline host: %d", w.Code)
	}
	var offlineCaps host.HostCapabilities
	_ = json.NewDecoder(w.Body).Decode(&offlineCaps)
	if offlineCaps.Online || offlineCaps.SSH.Available {
		t.Fatalf("expected offline host: %+v", offlineCaps)
	}

	// 6. Unknown host returns 404
	w = authedRequest("/api/hosts/unknown/capabilities")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown host capabilities, got %d", w.Code)
	}
}
