package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// version is replaced at build time for release binaries.
var version = "dev"

// config holds runtime configuration resolved from flags and environment.
type config struct {
	addr   string // listen address (host:port)
	origin string // allowed WebSocket origin; empty means "use the bind address"
	token  string // shared bearer token
}

// usage documents the full configuration surface so `--help` covers the
// environment variables alongside the flags.
func usage(fs *flag.FlagSet) func() {
	return func() {
		out := fs.Output()
		fmt.Fprintf(out, "visual-tmux-client %s\n\n", version)
		fmt.Fprintf(out, "Usage:\n  visual-tmux-client [flags]\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(out, "\nEnvironment:\n")
		fmt.Fprintf(out, "  VISUAL_TMUX_CLIENT_TOKEN      shared bearer token; generated and printed if unset\n")
		fmt.Fprintf(out, "  VISUAL_TMUX_CLIENT_ORIGIN     allowed WebSocket origin; defaults to the bind address\n")
		fmt.Fprintf(out, "  VISUAL_TMUX_CLIENT_TMUX_PATH  path to the tmux binary; defaults to $PATH lookup\n")
	}
}

// parseConfig parses flags and environment into a config.
func parseConfig(args []string) (*config, error) {
	fs := flag.NewFlagSet("visual-tmux-client", flag.ContinueOnError)
	fs.Usage = usage(fs)
	showVersion := fs.Bool("version", false, "print version and exit")
	addr := fs.String("addr", "127.0.0.1:7690", "listen address (host:port)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	if err := validateAddr(*addr); err != nil {
		return nil, err
	}
	cfg := &config{
		addr:   *addr,
		origin: os.Getenv("VISUAL_TMUX_CLIENT_ORIGIN"),
		token:  os.Getenv("VISUAL_TMUX_CLIENT_TOKEN"),
	}
	if cfg.origin == "" {
		cfg.origin = "http://" + cfg.addr
	}
	return cfg, nil
}

// validateAddr rejects malformed listen addresses so `--addr` fails fast at
// startup rather than later at bind time.
func validateAddr(addr string) error {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --addr %q: %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid --addr %q: bad port", addr)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "visual-tmux-client: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		return err
	}
	token, err := resolveToken()
	if err != nil {
		return err
	}
	cfg.token = token

	srv := newServer(cfg, token)

	sweepCtx, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go srv.tickets.runSweeper(sweepCtx, 10*time.Second)

	httpServer := &http.Server{
		Addr:    cfg.addr,
		Handler: srv.handler(),
	}

	sigCtx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-sigCtx.Done():
		// Graceful shutdown: close active attachments (killing their pty
		// processes) and stop accepting connections, bounded by a 10 s
		// watchdog. If anything fails to close in time, force-close.
		srv.shutdownAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
		}
		return nil
	}
}
