package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/coder/websocket"
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
	if orig, set := os.LookupEnv("VISUAL_TMUX_CLIENT_ORIGIN"); set {
		os.Unsetenv("VISUAL_TMUX_CLIENT_ORIGIN")
		t.Cleanup(func() { os.Setenv("VISUAL_TMUX_CLIENT_ORIGIN", orig) })
	}
	cfg, err := parseConfig([]string{"--addr", "0.0.0.0:7690"})
	if err != nil {
		t.Fatalf("parseConfig unset origin: %v", err)
	}
	if cfg.origin != "" {
		t.Fatalf("unset origin should be empty, got %q", cfg.origin)
	}

	t.Setenv("VISUAL_TMUX_CLIENT_ORIGIN", "")
	cfg, err = parseConfig([]string{"--addr", "0.0.0.0:7690"})
	if err != nil {
		t.Fatalf("parseConfig empty origin: %v", err)
	}
	if cfg.origin != "" {
		t.Fatalf("empty origin should be empty, got %q", cfg.origin)
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

func TestDefaultOriginRealRouteSameHost(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: ""}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", http.Header{
		"Origin": []string{ts.URL},
	})
	if err != nil {
		t.Fatalf("real-route same-host handshake failed: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected status 101, got %d", resp.StatusCode)
	}

	mt, data := readFrame(t, conn)
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %d", mt)
	}
	if frameType(t, data) != "error" {
		t.Fatalf("expected error frame, got %s", frameType(t, data))
	}
}
