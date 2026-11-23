package desktop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
)

type backend interface {
	Start(context.Context) (chan error, error)
	Addr() string
	Shutdown(context.Context) error
}

type backendFactory func(*config.Config, config.Provenance, fs.FS, app.Options) (backend, error)

// Controller owns the backend lifetime independently of the native Run loop.
// No method starts a window or invokes tmux: production adapters are provided
// by app.New and native wiring; tests can inject both sides.
type Controller struct {
	Bridge      *Bridge
	Preferences *Preferences
	Errors      <-chan error
	backend     backend
	cancel      context.CancelFunc
	budget      time.Duration
	stop        sync.Once
	stopErr     error
	stopping    chan struct{}
}

func Start(
	ctx context.Context, cfg *config.Config, prov config.Provenance, assets fs.FS, options app.Options,
) (*Controller, error) {
	return start(ctx, cfg, prov, assets, options, func(
		c *config.Config, p config.Provenance, f fs.FS, o app.Options,
	) (backend, error) {
		return app.New(c, p, f, o)
	})
}

func start(
	ctx context.Context, cfg *config.Config, prov config.Provenance, assets fs.FS,
	options app.Options, factory backendFactory,
) (*Controller, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	owned := *cfg
	owned.Server.Addr, owned.WebSocket.Origin = "127.0.0.1:0", ""
	token, err := auth.GenerateBearerToken()
	if err != nil {
		return nil, err
	}
	owned.Auth.Token = token
	owned.Tmux.Path = ResolveTmux(owned.Tmux.Path)
	if owned.Hosts.StateDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		owned.Hosts.StateDir = filepath.Join(base, "visual-tmux-client")
	}
	bridge := NewBridge(token)
	wrap := options.WrapHTTP
	options.WrapHTTP = func(next http.Handler) http.Handler {
		if wrap != nil {
			next = wrap(next)
		}
		return bridge.Wrap(next)
	}
	instance, err := factory(&owned, prov, assets, options)
	if err != nil {
		return nil, fmt.Errorf("desktop backend initialization: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	controller := &Controller{Bridge: bridge, backend: instance, cancel: cancel, stopping: make(chan struct{}),
		budget: owned.Shutdown.AttachmentTimeout.Duration() + owned.Shutdown.HTTPTimeout.Duration()}
	channel, err := instance.Start(workerCtx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("desktop listener: %w", err), controller.Shutdown())
	}
	controller.Errors = channel
	origin := app.BrowserURL(instance.Addr())
	if err := bridge.SetOrigin(origin); err != nil {
		return nil, errors.Join(err, controller.Shutdown())
	}
	if err := checkReady(ctx, origin); err != nil {
		return nil, errors.Join(err, controller.Shutdown())
	}
	// Open preferences only after the registry has acquired the state lock.
	controller.Preferences = OpenPreferences(owned.Hosts.StateDir)
	controller.Preferences.Attach(bridge)
	return controller, nil
}

const readinessTimeout = 3 * time.Second

func checkReady(ctx context.Context, origin string) error {
	readyCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, origin+"/api/client-config", nil)
	if err != nil {
		return err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("desktop readiness: %w", err)
	}
	defer response.Body.Close() //nolint:errcheck // read-only readiness response; no pending writes
	if response.StatusCode != http.StatusOK {
		return errors.New("desktop backend is not ready")
	}
	return nil
}

// Shutdown is safe for overlapping quit, native failure and signal paths. Use a
// fresh timeout context because native/application worker contexts may be done.
func (c *Controller) Shutdown() error {
	c.stop.Do(func() {
		close(c.stopping)
		c.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), c.budget)
		defer cancel()
		c.stopErr = c.backend.Shutdown(ctx)
	})
	return c.stopErr
}

func (c *Controller) Stopping() <-chan struct{} { return c.stopping }

func (c *Controller) Addr() string { return c.backend.Addr() }

// WatchBackend releases listener, file/SSH services and registry ownership
// before reporting a runtime failure. Expected shutdown never reports failure.
func (c *Controller) WatchBackend(ctx context.Context, report func(error)) {
	select {
	case err := <-c.Errors:
		select {
		case <-c.stopping:
			return
		default:
		}
		if err == nil {
			err = errors.New("desktop backend stopped unexpectedly; restart the application")
		}
		report(errors.Join(err, c.Shutdown()))
	case <-c.stopping:
	case <-ctx.Done():
	}
}

// RunWindow releases acquired state/listener resources even if window creation
// fails before a native shutdown callback has been installed.
func (c *Controller) RunWindow(run func(*Controller) error) (err error) {
	defer func() { err = errors.Join(err, c.Shutdown()) }()
	return run(c)
}
