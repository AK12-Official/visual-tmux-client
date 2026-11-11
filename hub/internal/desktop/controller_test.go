package desktop

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

type failingBackend struct {
	address   string
	failStart bool
	stops     atomic.Int32
}

func (b *failingBackend) Start(context.Context) (chan error, error) {
	if b.failStart {
		return nil, errors.New("start failure")
	}
	return make(chan error, 1), nil
}
func (b *failingBackend) Addr() string { return b.address }
func (b *failingBackend) Shutdown(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b.stops.Add(1)
	return nil
}

func TestControllerFailureCleanup(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Hosts.StateDir = t.TempDir()
	for _, tc := range []struct {
		name       string
		startFail  bool
		status     int
		badAddress bool
	}{
		{"start", true, 200, false}, {"readiness", false, 503, false}, {"wrong-bind", false, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			address := strings.TrimPrefix(server.URL, "http://")
			if tc.badAddress {
				address = "0.0.0.0:12345"
			}
			fake := &failingBackend{address: address, failStart: tc.startFail}
			factory := func(*config.Config, config.Provenance, fs.FS, app.Options) (backend, error) { return fake, nil }
			_, err := start(context.Background(), &cfg, config.Provenance{}, nil, app.Options{}, factory)
			if err == nil || fake.stops.Load() != 1 {
				t.Fatalf("failure cleanup: err=%v stops=%d", err, fake.stops.Load())
			}
		})
	}
}

func TestControllerOwnsInjectedBackendAndWindowFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = t.TempDir()
	opts := app.Options{Backend: spike.Backend{}, ProcessFact: spike.Processes{}}
	controller, err := Start(context.Background(), &cfg, config.Provenance{}, web.FS(), opts)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := controller.Bridge.LaunchURL()
	if err != nil || strings.Contains(launch, "Bearer") {
		t.Fatal("cannot launch")
	}
	windowErr := errors.New("window construction failed")
	if err := controller.RunWindow(func(*Controller) error { return windowErr }); !errors.Is(err, windowErr) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			if err := controller.Shutdown(); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	// A replacement controller acquiring the same state proves lock release.
	next, err := Start(context.Background(), &cfg, config.Provenance{}, web.FS(), opts)
	if err != nil {
		t.Fatalf("state lock leaked: %v", err)
	}
	if err := next.Shutdown(); err != nil {
		t.Fatal(err)
	}
}
