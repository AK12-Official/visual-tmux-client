package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	thttp "github.com/AK12-Official/visual-tmux-client/hub/internal/transport/http"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/transport/ws"
)

const (
	defaultInputChunkSize = 32 * 1024
	wildcardIPv4          = "0.0.0.0"
	wildcardIPv6          = "::"
	loopbackIPv4          = "127.0.0.1"
)

// App is the composition root orchestrating the lifecycle of all hub components.
type App struct {
	cfg        *config.Config
	prov       config.Provenance
	manager    *terminal.Manager
	server     *http.Server
	stopSweep  context.CancelFunc
	tickets    *auth.TicketStore
	tmuxClient terminal.ProcessFactory
	sessions   *session.Service
}

// BrowserURL turns a listen address into a clickable browser URL.
func BrowserURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == wildcardIPv4 || host == wildcardIPv6 {
		host = loopbackIPv4
	}
	return "http://" + net.JoinHostPort(host, port)
}

// StartupBanner formats the startup authentication token, origin, and browser URL.
func StartupBanner(token string, prov config.Provenance, addr string) string {
	source := string(prov.TokenSource)
	if source == "" {
		source = "generated"
	}
	return fmt.Sprintf(
		"visual-tmux-client: token: %s (source: %s)\nvisual-tmux-client: open %s\n",
		token, source, BrowserURL(addr),
	)
}

// Options allows overriding dependencies (useful for tests).
type Options struct {
	Socket      string
	ProcessFact terminal.ProcessFactory
	Backend     session.Backend
}

// New constructs the application composition root.
func New(cfg *config.Config, prov config.Provenance, staticFS fs.FS, opt ...Options) (*App, error) {
	var opts Options
	if len(opt) > 0 {
		opts = opt[0]
	}

	terminalManager := terminal.NewManager(cfg.Shutdown.AttachmentTimeout.Duration())
	ticketStore := auth.NewTicketStore(cfg.Auth.TicketTTL.Duration(), nil)

	var procFact terminal.ProcessFactory
	var sessBackend session.Backend

	if opts.ProcessFact != nil && opts.Backend != nil {
		procFact = opts.ProcessFact
		sessBackend = opts.Backend
	} else {
		// Normal production tmux client
		client := sessionBackendFactory(cfg.Tmux.Path, opts.Socket)
		procFact = client
		sessBackend = client
	}

	sessionService := session.NewService(sessBackend)

	termOpts := terminal.Options{
		MaxDimension:             cfg.Terminal.MaxDimension,
		StagingBufferBytes:       cfg.Terminal.StagingBufferBytes,
		OutputHighWaterBytes:     cfg.Terminal.OutputHighWaterBytes,
		OutputLowWaterBytes:      cfg.Terminal.OutputLowWaterBytes,
		BackpressurePollInterval: cfg.Terminal.BackpressurePollInterval.Duration(),
		ControlWriteTimeout:      cfg.WebSocket.ControlWriteTimeout.Duration(),
		OutputWriteTimeout:       cfg.WebSocket.OutputWriteTimeout.Duration(),
		ExitWriteTimeout:         cfg.WebSocket.ExitWriteTimeout.Duration(),
		InputChunkSize:           defaultInputChunkSize,
	}

	wsOpts := ws.Options{
		Origin:          cfg.WebSocket.Origin,
		MaxInputMessage: cfg.WebSocket.MaxInputMessageBytes,
		MaxDimension:    cfg.Terminal.MaxDimension,
		TerminalOptions: termOpts,
	}

	wsHandler := ws.NewHandler(
		ticketStore,
		procFact,
		terminalManager,
		wsOpts,
		nil,
		nil,
	)

	routerCfg := thttp.RouterConfig{
		Token:               cfg.Auth.Token,
		MaxRequestBodyBytes: cfg.Server.MaxRequestBodyBytes,
		WebConfig:           cfg.Web,
		StaticFS:            staticFS,
	}

	router := thttp.NewRouter(routerCfg, sessionService, ticketStore, wsHandler)

	httpServer := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           router,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Duration(),
		IdleTimeout:       cfg.Server.IdleTimeout.Duration(),
	}

	return &App{
		cfg:        cfg,
		prov:       prov,
		manager:    terminalManager,
		server:     httpServer,
		tickets:    ticketStore,
		tmuxClient: procFact,
		sessions:   sessionService,
	}, nil
}

// Start begins background workers and starts listening for HTTP/WS requests.
func (a *App) Start(ctx context.Context) (chan error, error) {
	sweepCtx, stopSweep := context.WithCancel(ctx)
	a.stopSweep = stopSweep
	go a.tickets.RunSweeper(sweepCtx, a.cfg.Auth.TicketSweepInterval.Duration())

	ln, err := net.Listen("tcp", a.server.Addr)
	if err != nil {
		stopSweep()
		return nil, fmt.Errorf("listen on %s: %w", a.server.Addr, err)
	}
	// Update Addr with actual bound address (important for port 0)
	a.server.Addr = ln.Addr().String()

	errCh := make(chan error, 1)
	go func() {
		err := a.server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	return errCh, nil
}

// Addr returns the bound server address.
func (a *App) Addr() string {
	return a.server.Addr
}

// Shutdown executes two-phase graceful shutdown: first active terminal attachments,
// then the HTTP server, each bounded by its configured timeout.
func (a *App) Shutdown(ctx context.Context) error {
	if a.stopSweep != nil {
		a.stopSweep()
	}

	var errs []error

	// Phase 1: Terminal attachment shutdown
	attCtx, attCancel := context.WithTimeout(ctx, a.cfg.Shutdown.AttachmentTimeout.Duration())
	if err := a.manager.Shutdown(attCtx); err != nil {
		errs = append(errs, fmt.Errorf("attachment shutdown: %w", err))
	}
	attCancel()

	// Phase 2: HTTP server shutdown
	httpCtx, httpCancel := context.WithTimeout(ctx, a.cfg.Shutdown.HTTPTimeout.Duration())
	if err := a.server.Shutdown(httpCtx); err != nil {
		_ = a.server.Close() //nolint:errcheck // force close on failure
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	httpCancel()

	return errors.Join(errs...)
}
