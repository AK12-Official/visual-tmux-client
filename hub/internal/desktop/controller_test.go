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
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

type failingBackend struct {
	address   string
	failStart bool
	stops     atomic.Int32
}

func TestHeadlessOwnerConflictDoesNotModifyOrStopOwner(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Addr = "127.0.0.1:0"
	cfg.Auth.Token = "headless-fixture-only"
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = t.TempDir()
	opts := app.Options{Backend: spike.Backend{}, ProcessFact: spike.Processes{}}
	owner, err := app.New(&cfg, config.Provenance{}, web.FS(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := owner.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	_, err = Start(context.Background(), &cfg, config.Provenance{}, web.FS(), opts)
	if !errors.Is(err, host.ErrStoreOwned) {
		t.Fatalf("headless ownership was not retained: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	address := app.BrowserURL(owner.Addr()) + "/api/hosts/local/sessions"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+cfg.Auth.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("desktop conflict stopped owner: %v", err)
	}
	defer response.Body.Close() //nolint:errcheck // read-only test response
	if response.StatusCode != http.StatusOK {
		t.Fatalf("owner changed after conflict: %d", response.StatusCode)
	}
	if cfg.Auth.Token != "headless-fixture-only" || cfg.Server.Addr != "127.0.0.1:0" {
		t.Fatal("desktop start modified headless transport configuration")
	}
}

type injectedFailureBackend struct {
	backend
	fail chan error
}

func (b injectedFailureBackend) Start(ctx context.Context) (chan error, error) {
	_, err := b.backend.Start(ctx)
	return b.fail, err
}

func TestBackendFailureReleasesListenerAndStateBeforeReporting(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = t.TempDir()
	opts := app.Options{Backend: spike.Backend{}, ProcessFact: spike.Processes{}}
	fail := make(chan error, 1)
	factory := func(c *config.Config, p config.Provenance, f fs.FS, o app.Options) (backend, error) {
		instance, err := app.New(c, p, f, o)
		if err != nil {
			return nil, err
		}
		return injectedFailureBackend{backend: instance, fail: fail}, nil
	}
	controller, err := start(context.Background(), &cfg, config.Provenance{}, web.FS(), opts, factory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controller.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	reported := make(chan error, 1)
	go controller.WatchBackend(context.Background(), func(err error) { reported <- err })
	bad := errors.New("injected backend failure")
	fail <- bad
	select {
	case err := <-reported:
		if !errors.Is(err, bad) || strings.Contains(err.Error(), controller.Bridge.token) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("failure did not finish bounded cleanup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, controller.Bridge.origin+"/api/client-config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := http.DefaultClient.Do(request); err == nil {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("failed backend still serves its listener")
	}
	replacement, err := Start(context.Background(), &cfg, config.Provenance{}, web.FS(), opts)
	if err != nil {
		t.Fatalf("failed backend retained state ownership: %v", err)
	}
	if err := replacement.Shutdown(); err != nil {
		t.Fatal(err)
	}
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
