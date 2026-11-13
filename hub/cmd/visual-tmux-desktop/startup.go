//go:build desktop && (linux || darwin)

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func newDesktop(configPath string) (*desktopWindow, error) {
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	userConfig, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	var args []string
	if configPath != "" {
		args = []string{"--config", configPath}
	}
	cfg, prov, err := config.LoadDesktop(args, workDir, userConfig, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	if cfg.Hosts.StateDir == "" {
		cfg.Hosts.StateDir = filepath.Join(userConfig, "visual-tmux-client")
	}
	fmt.Fprintln(os.Stderr, "desktop manages server.addr, auth.token and websocket.origin")
	profile := cfg.Hosts.StateDir
	if resolved, err := filepath.EvalSymlinks(profile); err == nil {
		profile = resolved
	}
	identity := sha256.Sum256(fmt.Appendf(nil, "%d:%s", os.Getuid(), profile))
	s := &desktopWindow{cfg: *cfg}
	// Native single-instance ownership precedes backend registry acquisition.
	// A second desktop launch only focuses; headless ownership remains an error.
	s.createNative(fmt.Sprintf("vtc-%x", identity))
	s.controller, err = desktop.Start(context.Background(), cfg, *prov, web.FS(), app.Options{})
	return s, err
}

func (s *desktopWindow) createNative(profile string) {
	logger := desktop.NativeLogger(os.Stderr)
	options := application.Options{
		Name:   "Visual Tmux Client",
		Linux:  application.LinuxOptions{ApplicationID: "io.github.ak12official.visualtmuxclient"},
		Logger: logger,
		// The owned close hook explicitly quits after confirmation and cleanup.
		// Keep Cocoa's automatic policy out of the lifecycle state machine,
		// including when the existing window is temporarily hidden.
		Mac:                         application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		DisableDefaultSignalHandler: true,
		ShouldQuit: func() bool {
			if focusFixture() {
				fmt.Fprintln(os.Stderr, "focus fixture: native quit requested; lifecycle absent:", s.lifecycle == nil)
			}
			if s.lifecycle == nil || s.lifecycle.State() == desktop.Stopped {
				return true
			}
			s.lifecycle.RequestQuit()
			return false
		},
		OnShutdown: func() {
			if focusFixture() {
				fmt.Fprintln(os.Stderr, "focus fixture: native shutdown")
			}
			if err := s.close(); err != nil {
				fmt.Fprintln(os.Stderr, "desktop shutdown:", err)
			}
		},
		PostShutdown: func() {
			// Darwin can terminate without Run returning. A forced probe timeout
			// must still fail CI instead of producing a successful process exit.
			if s.probe && (!s.passed.Load() || !s.lifecycle.Confirmed()) {
				os.Exit(1)
			}
		},
	}
	if profile != "" {
		options.SingleInstance = &application.SingleInstanceOptions{
			UniqueID: profile,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if focusFixture() {
					fmt.Fprintln(os.Stderr, "focus fixture: second launch received")
				}
				if window := s.window.Load(); window != nil {
					application.InvokeAsync(func() {
						window.Show()
						window.Focus()
					})
				}
			},
		}
	}
	s.native = application.New(options)
}

func showStartupError(s *desktopWindow, failure error) {
	if s == nil {
		s = &desktopWindow{}
		s.createNative("")
	}
	// No launch URL or bearer is included in startup diagnostics.
	message := "Unable to start / 无法启动\n\n" + failure.Error() +
		"\n\nCheck configuration and state ownership. Close the other application using this state, then retry."
	content := "<!doctype html><meta charset=utf-8><title>Startup error</title><pre>" +
		html.EscapeString(message) + "</pre>"
	s.native.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "startup-error", Title: "Visual Tmux Client - startup error", Width: initialWidth, Height: initialHeight,
		HTML: content,
	})
	if err := s.native.Run(); err != nil && !errors.Is(err, failure) {
		fmt.Fprintln(os.Stderr, "desktop error window unavailable:", err)
	}
}

func focusFixture() bool { return os.Getenv("VTC_NATIVE_FIXTURE_MODE") == "focus" }
