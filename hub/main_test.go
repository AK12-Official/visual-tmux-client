package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStartupBanner(t *testing.T) {
	// The env case is the one that motivated the banner: a token supplied via
	// `VISUAL_TMUX_CLIENT_TOKEN=x ./visual-tmux-client` exists only inside the
	// child process, so the banner must print it for the operator to use.
	got := startupBanner("tok-from-env", false, "127.0.0.1:7690")
	for _, want := range []string{
		"token: tok-from-env",
		"source: environment",
		"open http://127.0.0.1:7690",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("banner should contain %q, got:\n%s", want, got)
		}
	}

	got = startupBanner("tok-gen", true, "127.0.0.1:9000")
	for _, want := range []string{"source: generated", "open http://127.0.0.1:9000"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner should contain %q, got:\n%s", want, got)
		}
	}
}

func TestBrowserURL(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:7690": "http://127.0.0.1:7690",
		// Wildcard binds are not openable in a browser; print loopback.
		"0.0.0.0:7690": "http://127.0.0.1:7690",
		":7690":        "http://127.0.0.1:7690",
		"[::]:7690":    "http://127.0.0.1:7690",
		"localhost:80": "http://localhost:80",
		// Not a host:port; degrade to prefixing rather than erroring.
		"weird": "http://weird",
	}
	for addr, want := range cases {
		if got := browserURL(addr); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestParseConfigOriginPolicy(t *testing.T) {
	t.Setenv("VISUAL_TMUX_CLIENT_ORIGIN", "")
	cfg, err := parseConfig([]string{"--addr", "0.0.0.0:7690"})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.origin != "" {
		t.Fatalf("default origin should be dynamic same-origin, got %q", cfg.origin)
	}

	t.Setenv("VISUAL_TMUX_CLIENT_ORIGIN", "https://tmux.example.com")
	cfg, err = parseConfig([]string{"--addr", "0.0.0.0:7690"})
	if err != nil {
		t.Fatalf("parseConfig explicit origin: %v", err)
	}
	if cfg.origin != "https://tmux.example.com" {
		t.Fatalf("explicit origin = %q, want exact configured value", cfg.origin)
	}
}

func TestRequestOriginAllowed(t *testing.T) {
	const host = "192.0.2.10:7690"
	for _, origin := range []string{"", "http://" + host, "https://" + host} {
		if !requestOriginAllowed(origin, host) {
			t.Errorf("expected origin %q to be allowed for host %q", origin, host)
		}
	}
	for _, origin := range []string{
		"http://evil.example",
		"http://evil.example:7690",
		"http://192.0.2.11:7690",
	} {
		if requestOriginAllowed(origin, host) {
			t.Errorf("expected foreign origin %q to be rejected for host %q", origin, host)
		}
	}
}

func TestAttachRouteRejectsForeignOriginBeforeUpgrade(t *testing.T) {
	s := &server{origin: ""}
	req := httptest.NewRequest(http.MethodGet, "http://192.0.2.10:7690/ws/local/demo", nil)
	req.Host = "192.0.2.10:7690"
	req.Header.Set("Origin", "http://evil.example:7690")
	rec := httptest.NewRecorder()

	s.attachRoute(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin: expected 403, got %d", rec.Code)
	}
}
