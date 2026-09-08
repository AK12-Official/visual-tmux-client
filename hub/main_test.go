package main

import (
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
		"localhost:80":  "http://localhost:80",
		// Not a host:port; degrade to prefixing rather than erroring.
		"weird": "http://weird",
	}
	for addr, want := range cases {
		if got := browserURL(addr); got != want {
			t.Errorf("browserURL(%q) = %q, want %q", addr, got, want)
		}
	}
}
