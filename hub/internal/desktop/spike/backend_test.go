package spike_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

// The spike must never fall through to the real tmux backend. Exercise the
// actual app wiring, protected route and refusal paths, not a mock HTTP router.
func TestDisposableBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.Auth.Token = "spike-test-token"
	cfg.Hosts.StateDir = t.TempDir()
	cfg.Files.Enabled = false
	instance, err := app.New(&cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := instance.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if _, err := instance.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	url := app.BrowserURL(instance.Addr()) + "/api/hosts/local/sessions"
	// Desktop launch exchange is absent unless the desktop explicitly wraps the
	// router. Merely serving the shared frontend cannot disclose a credential.
	bootstrapURL := app.BrowserURL(instance.Addr()) + "/api/desktop/bootstrap"
	bootstrapResponse, err := client.Post(bootstrapURL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapResponse.StatusCode == http.StatusOK {
		t.Error("ordinary server exposed desktop bootstrap")
	}
	if err := bootstrapResponse.Body.Close(); err != nil {
		t.Error(err)
	}
	for _, authorized := range []bool{false, true} {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		if authorized {
			req.Header.Set("Authorization", "Bearer "+cfg.Auth.Token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			if !authorized {
				if resp.StatusCode != http.StatusUnauthorized {
					t.Errorf("unauthorized status: %d", resp.StatusCode)
				}
				return
			}
			var result struct {
				Sessions []session.Session `json:"sessions"`
			}
			if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&result) != nil {
				t.Fatalf("cannot read synthetic sessions: status %d", resp.StatusCode)
			}
			if len(result.Sessions) != 1 || result.Sessions[0].Name != "desktop-spike" {
				t.Errorf("unexpected synthetic sessions: %+v", result.Sessions)
			}
		}()
	}
	ctx := context.Background()
	backend := spike.Backend{}
	if _, err := backend.Create(ctx, "test"); err == nil {
		t.Error("spike allowed session creation")
	}
	if backend.Rename(ctx, "desktop-spike", "renamed") == nil || backend.Kill(ctx, "desktop-spike") == nil {
		t.Error("spike allowed session mutation")
	}
	if _, err := (spike.Processes{}).NewProcess(ctx, "desktop-spike", 80, 24); err == nil {
		t.Error("spike allowed process creation")
	}
}
