package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
)

func settingsRouter(t *testing.T, configure func(*RouterConfig)) (http.Handler, *host.Registry) {
	t.Helper()
	registry, err := host.NewRegistry(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() }) //nolint:errcheck // test cleanup
	cfg := RouterConfig{Token: "tok", Hosts: registry}
	if configure != nil {
		configure(&cfg)
	}
	return NewRouter(cfg, &mockSessionService{}, nil, nil, nil), registry
}

func currentSettings(t *testing.T, registry *host.Registry) host.Settings {
	t.Helper()
	settings, err := registry.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return settings
}

func TestHostSettingsProxyAddressValidation(t *testing.T) {
	router, registry := settingsRouter(t, nil)

	rejected := []string{
		"http://127.0.0.1:7890",
		"socks5h://127.0.0.1:7890",
		"127.0.0.1",
		"127.0.0.1:0",
		"127.0.0.1:70000",
		"127.0.0.1:abc",
		"user:secret@127.0.0.1:7890",
		"127.0.0.1:7890/path",
		"fe80::1",
		"not a host:7890",
		"localhost",
	}
	for _, value := range rejected {
		body := fmt.Sprintf(`{"revision":1,"proxyAddress":%s}`, mustJSON(t, value))
		rec := authedPut(t, router, "/api/host-settings", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("proxyAddress %q: expected 400, got %d body=%s", value, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "invalid_proxy_address") {
			t.Errorf("proxyAddress %q: expected invalid_proxy_address, got %s", value, rec.Body.String())
		}
	}
	// A value that is not a string is refused the same way.
	rec := authedPut(t, router, "/api/host-settings", `{"revision":1,"proxyAddress":7890}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_proxy_address") {
		t.Fatalf("numeric proxyAddress: %d %s", rec.Code, rec.Body.String())
	}
	if got := currentSettings(t, registry); got.ProxyAddress != "" || got.Revision != 1 {
		t.Fatalf("a refused address changed the settings: %+v", got)
	}
}

func TestHostSettingsAcceptedProxyAddressForms(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"127.0.0.1:7890", "127.0.0.1:7890"},
		{" 127.0.0.1:7890 ", "127.0.0.1:7890"},
		{"socks5://127.0.0.1:7890", "127.0.0.1:7890"},
		{"SOCKS5://proxy.lan:1080", "proxy.lan:1080"},
		{"[::1]:1080", "[::1]:1080"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			router, registry := settingsRouter(t, nil)
			body := fmt.Sprintf(`{"revision":1,"proxyAddress":%s}`, mustJSON(t, tc.input))
			w := authedPut(t, router, "/api/host-settings", body)
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
			}
			var settings host.Settings
			decodeBody(t, w, &settings)
			if settings.ProxyAddress != tc.want {
				t.Fatalf("response proxyAddress %q, want %q", settings.ProxyAddress, tc.want)
			}
			got := currentSettings(t, registry)
			if got.ProxyAddress != tc.want || got.Revision != 2 {
				t.Fatalf("stored %+v, want proxy %q at revision 2", got, tc.want)
			}
			if got.DefaultHostID == nil || *got.DefaultHostID != host.LocalID {
				t.Fatalf("setting the proxy changed the default host: %+v", got)
			}

			rec := authedGet(t, router, "/api/host-settings")
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"proxyAddress":"`+tc.want+`"`) {
				t.Fatalf("GET does not report the proxy: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestHostSettingsPartialUpdates(t *testing.T) {
	ctx := context.Background()
	router, registry := settingsRouter(t, nil)
	put := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := authedPut(t, router, "/api/host-settings", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s: expected 200, got %d body=%s", body, rec.Code, rec.Body.String())
		}
		return rec
	}

	if _, err := registry.Save(ctx, host.HostInput{
		ID: "r1", Name: "Remote 1", Address: "192.0.2.1", Username: "u", CredentialType: "password",
		Credentials: host.Credentials{Password: "p"},
	}, 0); err != nil {
		t.Fatal(err)
	}

	// Setting only the proxy leaves the default host unchanged.
	put(`{"revision":1,"proxyAddress":"127.0.0.1:7890"}`)
	if got := currentSettings(t, registry); got.DefaultHostID == nil ||
		*got.DefaultHostID != host.LocalID || got.ProxyAddress != "127.0.0.1:7890" {
		t.Fatalf("proxy-only update changed the default host: %+v", got)
	}

	// Setting only the default host leaves the proxy unchanged.
	put(`{"revision":2,"defaultHostId":"r1"}`)
	if got := currentSettings(t, registry); got.DefaultHostID == nil ||
		*got.DefaultHostID != "r1" || got.ProxyAddress != "127.0.0.1:7890" {
		t.Fatalf("default-host-only update changed the proxy: %+v", got)
	}

	// A body with neither field changes nothing but still advances the revision.
	put(`{"revision":3}`)
	if got := currentSettings(t, registry); got.DefaultHostID == nil ||
		*got.DefaultHostID != "r1" || got.ProxyAddress != "127.0.0.1:7890" || got.Revision != 4 {
		t.Fatalf("empty update changed the settings: %+v", got)
	}

	// An explicit null or empty string clears the proxy; an empty id clears the default host.
	put(`{"revision":4,"proxyAddress":null}`)
	if got := currentSettings(t, registry); got.ProxyAddress != "" || got.DefaultHostID == nil {
		t.Fatalf("null did not clear only the proxy: %+v", got)
	}
	put(`{"revision":5,"proxyAddress":"127.0.0.1:7891","defaultHostId":"r1"}`)
	put(`{"revision":6,"proxyAddress":""}`)
	if got := currentSettings(t, registry); got.ProxyAddress != "" || got.DefaultHostID == nil {
		t.Fatalf("empty string did not clear only the proxy: %+v", got)
	}
	put(`{"revision":7,"defaultHostId":null}`)
	if got := currentSettings(t, registry); got.DefaultHostID != nil {
		t.Fatalf("null did not clear the default host: %+v", got)
	}

	// A stale revision is refused and keeps the current settings.
	rec := authedPut(t, router, "/api/host-settings", `{"revision":3,"proxyAddress":"127.0.0.1:9"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a stale revision, got %d", rec.Code)
	}
	if got := currentSettings(t, registry); got.ProxyAddress != "" || got.Revision != 8 {
		t.Fatalf("stale update changed the settings: %+v", got)
	}
}

func TestHostSettingsUnauthenticatedCallsAreRefused(t *testing.T) {
	router, registry := settingsRouter(t, nil)
	for _, token := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodPut, "/api/host-settings",
			strings.NewReader(`{"revision":1,"proxyAddress":"127.0.0.1:7890"}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: expected 401, got %d", token, w.Code)
		}
	}
	if got := currentSettings(t, registry); got.ProxyAddress != "" || got.Revision != 1 {
		t.Fatalf("an unauthenticated call changed the settings: %+v", got)
	}
}

func TestProxyFailuresAreReportedWithTheProxyAddress(t *testing.T) {
	const proxyAddress = "127.0.0.1:7890"
	// The cause carries detail that must stay out of the response.
	cause := errors.New("socks5 handshake with user:secret@10.0.0.9 failed")

	for _, code := range []host.ErrorCode{
		host.ErrProxyUnreachable,
		host.ErrProxyAuthRequired,
		host.ErrProxyRefused,
		host.ErrProxyTargetUnreachable,
	} {
		t.Run(string(code), func(t *testing.T) {
			key := generateTestPubKey(t)
			dialer := &spyProbeDialer{key: key}
			router, registry := settingsRouter(t, func(cfg *RouterConfig) {
				cfg.ProbeDialer = dialer.Dial
			})
			ctx := context.Background()
			created, err := registry.Save(ctx, host.HostInput{
				ID: "r1", Name: "Remote 1", Address: "192.0.2.10", Username: "u", CredentialType: "password",
				Credentials: host.Credentials{Password: "p"},
			}, 0)
			if err != nil {
				t.Fatal(err)
			}

			dialer.err = &host.ProxyError{Code: code, Proxy: proxyAddress, Err: cause}
			want := string(code) + ": proxy " + proxyAddress

			// Probe of an unsaved host and of a registered host.
			for _, target := range []string{"/api/hosts/probe", "/api/hosts/" + string(created.ID) + "/probe"} {
				body := `{"address":"192.0.2.10","port":22}`
				if strings.HasSuffix(target, "/probe") && target != "/api/hosts/probe" {
					body = ""
				}
				rec := authedRequest(t, router, http.MethodPost, target, body)
				if rec.Code != http.StatusServiceUnavailable {
					t.Fatalf("POST %s: expected 503, got %d body=%s", target, rec.Code, rec.Body.String())
				}
				assertProxyDiagnostic(t, rec, want)
			}

			// The same mapping applies wherever a host error is written.
			hostErr := &host.Error{Code: code, Cause: &host.ProxyError{Code: code, Proxy: proxyAddress, Err: cause}}
			for _, mapErr := range []func(http.ResponseWriter, error){mapHostError, mapSessionError} {
				w := httptest.NewRecorder()
				mapErr(w, hostErr)
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("mapped status %d, want 503", w.Code)
				}
				assertProxyDiagnostic(t, w, want)
			}

			// Other probe failures are unchanged.
			dialer.err = errors.New("probe dial: connection refused")
			rec := authedRequest(t, router, http.MethodPost, "/api/hosts/probe", `{"address":"192.0.2.10","port":22}`)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "probe_failed") {
				t.Fatalf("non-proxy probe failure: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func assertProxyDiagnostic(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var failure struct {
		Error string `json:"error"`
	}
	decodeBody(t, rec, &failure)
	if failure.Error != want {
		t.Fatalf("diagnostic %q, want %q", failure.Error, want)
	}
	for _, secret := range []string{"secret", "10.0.0.9", "handshake"} {
		if strings.Contains(failure.Error, secret) {
			t.Fatalf("diagnostic leaks internal detail %q: %s", secret, failure.Error)
		}
	}
}
