package desktop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/coder/websocket"
)

func desktopFixture(t *testing.T) *Controller {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = t.TempDir()
	c, err := Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func desktopHTTP(t *testing.T, c *Controller, method, path, body, token string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, method, c.Bridge.origin+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", c.Bridge.origin)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close() //nolint:errcheck // read-only test response
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}

func TestDesktopComposedAuthorizationAndTerminalBoundary(t *testing.T) {
	c := desktopFixture(t)
	code := launchBody(t, c.Bridge)
	for _, path := range []string{"/?desktop=1", "/api/client-config"} {
		status, body := desktopHTTP(t, c, http.MethodGet, path, "", "")
		if status != http.StatusOK || strings.Contains(body, c.Bridge.token) || strings.Contains(body, code) {
			t.Fatalf("public response leaked credentials: status=%d", status)
		}
	}
	for _, path := range []string{"/api/hosts/local/sessions", "/api/hosts", "/api/host-settings"} {
		status, _ := desktopHTTP(t, c, http.MethodGet, path, "", "")
		if status != http.StatusUnauthorized {
			t.Fatalf("unprotected %s: %d", path, status)
		}
	}
	status, body := desktopHTTP(t, c, http.MethodPost, desktopPrefix+"bootstrap", code, "")
	var credential struct {
		Token string `json:"token"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &credential) != nil ||
		credential.Token != c.Bridge.token {
		t.Fatal("desktop exchange failed")
	}
	status, body = desktopHTTP(t, c, http.MethodGet, "/api/hosts/local/sessions", "", credential.Token)
	if status != http.StatusOK || !strings.Contains(body, "desktop-spike") {
		t.Fatal("owned workspace unavailable")
	}
	ticketBody := `{"hostId":"local","session":"desktop-spike"}`
	status, body = desktopHTTP(t, c, http.MethodPost, "/api/ws-ticket", ticketBody, credential.Token)
	var issued struct {
		Ticket string `json:"ticket"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &issued) != nil || issued.Ticket == "" {
		t.Fatal("terminal ticket unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	address := "ws" + strings.TrimPrefix(c.Bridge.origin, "http") + "/ws/local/other?ticket=" + issued.Ticket
	foreign := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://foreign.test"}}}
	_, response, err := websocket.Dial(ctx, address, foreign)
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatal("foreign WebSocket origin accepted")
	}
	owned := &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {c.Bridge.origin}}}
	conn, _, err := websocket.Dial(ctx, address, owned)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow() //nolint:errcheck // test socket cleanup
	_, frame, err := conn.Read(ctx)
	if err != nil || !strings.Contains(string(frame), "ticket does not match session") {
		t.Fatal("desktop composition lost terminal ticket binding")
	}
	status, _ = desktopHTTP(t, c, http.MethodPost, desktopPrefix+"bootstrap", code, "")
	if status != http.StatusUnauthorized {
		t.Fatal("launch code replayed")
	}
}
