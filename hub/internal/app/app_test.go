package app

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
)

type dummyBackend struct{}

func (d *dummyBackend) List(ctx context.Context) ([]session.Session, error) {
	return []session.Session{}, nil
}

func (d *dummyBackend) Create(ctx context.Context, name string) (*session.Session, error) {
	return &session.Session{Name: name}, nil
}

func (d *dummyBackend) Rename(ctx context.Context, oldName, newName string) error {
	return nil
}

func (d *dummyBackend) Kill(ctx context.Context, name string) error {
	return nil
}

type dummyFactory struct{}

func (d *dummyFactory) NewProcess(ctx context.Context, sess string, cols, rows int) (terminal.Process, error) {
	return nil, nil
}

func TestStartupBannerAndBrowserURL(t *testing.T) {
	banner := StartupBanner("secret-123", config.Provenance{TokenSource: config.SourceGenerated}, "0.0.0.0:7690")
	expectedURL := "http://127.0.0.1:7690"
	if BrowserURL("0.0.0.0:7690") != expectedURL {
		t.Errorf("expected %s, got %s", expectedURL, BrowserURL("0.0.0.0:7690"))
	}
	if BrowserURL("192.168.1.10:8080") != "http://192.168.1.10:8080" {
		t.Errorf("unexpected URL for non-wildcard: %s", BrowserURL("192.168.1.10:8080"))
	}
	if len(banner) == 0 {
		t.Fatal("empty banner")
	}
}

func TestAppLifecycleAndShutdown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.Shutdown.AttachmentTimeout = config.Duration(100 * time.Millisecond)
	cfg.Shutdown.HTTPTimeout = config.Duration(100 * time.Millisecond)

	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")},
	}

	opts := Options{
		Backend:     &dummyBackend{},
		ProcessFact: &dummyFactory{},
	}

	app, err := New(&cfg, config.Provenance{}, staticFS, opts)
	if err != nil {
		t.Fatalf("New app failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh, err := app.Start(ctx)
	if err != nil {
		t.Fatalf("app Start failed: %v", err)
	}

	// Verify HTTP server responds to unauthenticated /api/client-config
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + app.Addr() + "/api/client-config")
	if err != nil {
		t.Fatalf("GET /api/client-config failed: %v", err)
	}
	_ = resp.Body.Close() //nolint:errcheck // test cleanup
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// Wait for server error channel
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected server exit error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after shutdown")
	}
}

func TestBrowserURL_EdgeCases(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"0.0.0.0:7690", "http://127.0.0.1:7690"},
		{"[::]:7690", "http://127.0.0.1:7690"},
		{":7690", "http://127.0.0.1:7690"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"192.168.1.50:9000", "http://192.168.1.50:9000"},
		{"invalid-format", "http://invalid-format"},
	}

	for _, tt := range tests {
		got := BrowserURL(tt.addr)
		if got != tt.want {
			t.Errorf("BrowserURL(%q) = %q; want %q", tt.addr, got, tt.want)
		}
	}
}

func TestStartupBanner_Sources(t *testing.T) {
	sources := []config.Source{
		config.SourceGenerated,
		config.SourceYAML,
		config.SourceEnvironment,
		config.SourceCLI,
		config.SourceDefault,
		"",
	}

	for _, s := range sources {
		banner := StartupBanner("tok-123", config.Provenance{TokenSource: s}, "127.0.0.1:7690")
		if !strings.Contains(banner, "tok-123") {
			t.Errorf("banner missing token for source %q", s)
		}
		if !strings.Contains(banner, "http://127.0.0.1:7690") {
			t.Errorf("banner missing URL for source %q", s)
		}
	}
}

func TestAppStart_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }() //nolint:errcheck // cleanup

	cfg := config.DefaultConfig()
	cfg.Server.Addr = ln.Addr().String()

	app, err := New(&cfg, config.Provenance{}, nil, Options{
		Backend:     &dummyBackend{},
		ProcessFact: &dummyFactory{},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err = app.Start(ctx)
	if err == nil {
		t.Fatal("expected Start to fail when port already in use")
	}
}
