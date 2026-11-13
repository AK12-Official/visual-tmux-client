package desktop

import (
	"net/http"
	"strings"
	"testing"
)

func TestExternalLinkAllowlistAndAuthentication(t *testing.T) {
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	var opened []string
	b.AttachExternalLinks(func(address string) error { opened = append(opened, address); return nil })
	for _, raw := range []string{
		"file:///etc/passwd", "javascript:alert(1)", "mailto:a@b", "https://user:secret@example.test",
		"//example.test", "https://", strings.Repeat("x", maxExternalURLBytes+1),
	} {
		if _, err := ExternalURL(raw); err == nil {
			t.Fatalf("accepted unsafe scheme/URL %q", raw)
		}
	}
	for _, origin := range []string{"", "https://foreign.test"} {
		response := bridgeRequest(b, "open-external", `{"url":"https://example.test"}`, origin, "test-bearer")
		if response.Code != http.StatusForbidden {
			t.Fatal("foreign/absent origin opened browser")
		}
	}
	if len(opened) != 0 {
		t.Fatal("browser opened before authorized action")
	}
	for _, raw := range []string{"http://example.test/a", "https://example.test/path?q=1#anchor"} {
		if _, err := ExternalURL(raw); err != nil {
			t.Fatal(err)
		}
	}
	response := bridgeRequest(b, "open-external", `{"url":"https://example.test"}`, testOrigin, "test-bearer")
	if response.Code != http.StatusOK || len(opened) != 1 {
		t.Fatal("authorized browser action failed")
	}
}
