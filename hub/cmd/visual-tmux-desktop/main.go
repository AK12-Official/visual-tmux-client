//go:build desktop && (linux || darwin)

// This entry is a compatibility spike until the native integration gate passes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

const (
	probeTimeout    = 20 * time.Second
	probeCloseDelay = 200 * time.Millisecond
	initialWidth    = 1200
	initialHeight   = 800
)

// Only unsigned compatibility bundles opt in at build time. The ordinary
// development binary still requires an explicit --spike switch.
var defaultSpike = "false"

type spikeWindow struct {
	controller *desktop.Controller
	native     *application.App
	cfg        config.Config
	stateDir   string
	probe      bool
	passed     atomic.Bool
	stop       sync.Once
	stopErr    error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "visual-tmux-desktop:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	spikeMode := flag.Bool("spike", defaultSpike == "true", "run disposable spike; no real tmux or saved hosts")
	probe := flag.Bool("probe", false, "verify authenticated HTTP action, Vue mount and reload, then quit")
	flag.Parse()
	if !*spikeMode || flag.NArg() != 0 {
		return errors.New("desktop entry is a prototype; use --spike (optionally --probe)")
	}
	instance, err := newSpike(*probe)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, instance.close()) }()
	return instance.run()
}

func newSpike(probe bool) (*spikeWindow, error) {
	directory, err := os.MkdirTemp("", "vtc-desktop-spike-")
	if err != nil {
		return nil, err
	}
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = directory
	controller, err := desktop.Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		return nil, errors.Join(err, os.RemoveAll(directory))
	}
	return &spikeWindow{controller: controller, cfg: cfg, stateDir: directory, probe: probe}, nil
}

func (s *spikeWindow) close() error {
	s.stop.Do(func() { s.stopErr = errors.Join(s.controller.Shutdown(), os.RemoveAll(s.stateDir)) })
	return s.stopErr
}

func (s *spikeWindow) run() error {
	launchURL, err := s.controller.Bridge.LaunchURL()
	if err != nil {
		return err
	}
	s.native = application.New(application.Options{
		Name:   "Visual Tmux Client compatibility spike",
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		OnShutdown: func() {
			if err := s.close(); err != nil {
				fmt.Fprintln(os.Stderr, "desktop shutdown:", err)
			}
		},
	})
	js := ""
	if s.probe {
		s.controller.Bridge.Register("probe", http.HandlerFunc(s.probeMounted))
		js = probeJS
	}
	window := s.native.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "desktop-spike", Title: "Visual Tmux Client - compatibility spike",
		Width: initialWidth, Height: initialHeight, URL: launchURL, JS: js,
	})
	attachDownload(s.controller, s.native, window, s.cfg.Files.Enabled, s.cfg.Files.MaxFileSize)
	go s.watch()
	if err := s.native.Run(); err != nil {
		return err
	}
	if s.probe && !s.passed.Load() {
		return errors.New("native load/reload probe did not pass")
	}
	return nil
}

func (s *spikeWindow) probeMounted(w http.ResponseWriter, r *http.Request) {
	var result struct {
		Reloaded bool `json:"reloaded"`
	}
	if desktop.DecodeJSON(r, &result) != nil {
		http.Error(w, "invalid probe", http.StatusBadRequest)
		return
	}
	if result.Reloaded {
		s.passed.Store(true)
		fmt.Fprintln(os.Stderr, "spike: PASS authenticated HTTP action; Vue workspace mounted after reload")
		time.AfterFunc(probeCloseDelay, s.native.Quit)
	} else {
		fmt.Fprintln(os.Stderr, "spike: authenticated HTTP action and Vue mount verified; reloading")
	}
	desktop.WriteJSON(w, map[string]bool{"ok": true})
}

func (s *spikeWindow) watch() {
	var timeout <-chan time.Time
	if s.probe {
		timer := time.NewTimer(probeTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case err := <-s.controller.Errors:
		if err != nil {
			fmt.Fprintln(os.Stderr, "spike backend:", err)
		}
		s.native.Quit()
	case <-timeout:
		fmt.Fprintln(os.Stderr, "spike: FAIL native load/reload probe timed out")
		s.native.Quit()
	case <-s.native.Context().Done():
	}
}

// This probe runs after actual frontend authentication and workspace rendering.
// No token is put in JavaScript source, logs or raw native messages.
const probeJS = `(() => {
 const timer = setInterval(async () => {
  const root = document.getElementById('app');
  if (!root?.querySelector('.app__bar') || !root.textContent.includes('desktop-spike')) return;
  clearInterval(timer);
  const reloaded = sessionStorage.getItem('vtc-spike-reloaded') === '1';
  const response = await fetch('/api/desktop/probe', {
   method: 'POST', headers: {
    'Content-Type':'application/json', Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')
   },
   body: JSON.stringify({reloaded})
  });
  if (!response.ok) return;
  if (!reloaded) { sessionStorage.setItem('vtc-spike-reloaded','1'); location.reload(); }
  else sessionStorage.removeItem('vtc-spike-reloaded');
 },100);
})()`
