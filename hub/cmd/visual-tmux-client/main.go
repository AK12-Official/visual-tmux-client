package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

// version is replaced at build time via -X main.version for release binaries.
var version = "dev"

func printLine(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...) //nolint:errcheck // CLI output writing
}

func usage(fs *flag.FlagSet, out io.Writer) func() {
	return func() {
		printLine(out, "visual-tmux-client %s\n\n", version)
		printLine(out, "Usage:\n  visual-tmux-client [flags]\n\nFlags:\n")
		fs.SetOutput(out)
		fs.PrintDefaults()
		printLine(out, "\nEnvironment:\n")
		printLine(out, "  VISUAL_TMUX_CLIENT_CONFIG     path to the YAML configuration file\n")
		printLine(out, "  VISUAL_TMUX_CLIENT_ADDR       listen address (host:port)\n")
		printLine(out, "  VISUAL_TMUX_CLIENT_TOKEN      shared bearer token; printed at startup, generated if unset\n")
		printLine(out, "  VISUAL_TMUX_CLIENT_ORIGIN     allowed WebSocket origin; defaults to matching request host\n")
		printLine(out, "  VISUAL_TMUX_CLIENT_TMUX_PATH  path to the tmux binary; defaults to $PATH lookup\n")
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("visual-tmux-client", flag.ContinueOnError)
	fs.Usage = usage(fs, stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	_ = fs.String("config", "", "path to YAML configuration file")
	_ = fs.String("addr", "", "listen address (host:port)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		printLine(stdout, "%s\n", version)
		return nil
	}

	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	cfg, prov, err := config.Load(args, workDir, os.LookupEnv, version)
	if err != nil {
		return err
	}

	if cfg.Auth.Token == "" {
		token, err := auth.GenerateBearerToken()
		if err != nil {
			return fmt.Errorf("generate bearer token: %w", err)
		}
		cfg.Auth.Token = token
		prov.TokenSource = config.SourceGenerated
	}

	banner := app.StartupBanner(cfg.Auth.Token, *prov, cfg.Server.Addr)
	printLine(stderr, "%s", banner)

	appInstance, err := app.New(cfg, *prov, web.FS())
	if err != nil {
		return fmt.Errorf("create app: %w", err)
	}

	sigCtx, stopSig := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSig()

	errCh, err := appInstance.Start(sigCtx)
	if err != nil {
		return fmt.Errorf("start app: %w", err)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-sigCtx.Done():
		shutdownBudget := cfg.Shutdown.AttachmentTimeout.Duration() +
			cfg.Shutdown.HTTPTimeout.Duration() + time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownBudget)
		defer cancel()
		return appInstance.Shutdown(shutdownCtx)
	}
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "visual-tmux-client: %v\n", err)
		os.Exit(1)
	}
}
