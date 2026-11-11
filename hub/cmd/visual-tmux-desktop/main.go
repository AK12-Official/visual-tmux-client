//go:build desktop && (linux || darwin)

// Native desktop composition; compatibility probes use isolated fake services.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	probeFinalStage = 2
)

// Only unsigned compatibility bundles opt in at build time. The ordinary
// development binary starts the configured application by default.
var defaultSpike = "false"

// Populated by the same release linker convention as the headless command.
var version = "dev"

type desktopWindow struct {
	controller      *desktop.Controller
	lifecycle       *desktop.Lifecycle
	native          *application.App
	window          atomic.Pointer[application.WebviewWindow]
	cfg             config.Config
	temporaryDir    string
	probe           bool
	passed          atomic.Bool
	quitApproved    atomic.Bool
	frontendReady   atomic.Bool
	navigationReady atomic.Bool
	stop            sync.Once
	stopErr         error
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "visual-tmux-desktop:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	flags := flag.NewFlagSet("visual-tmux-desktop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	spikeMode := flags.Bool("spike", defaultSpike == "true", "run disposable spike; no real tmux or saved hosts")
	probe := flags.Bool("probe", false, "verify authenticated HTTP action, Vue mount and reload, then quit")
	configPath := flags.String("config", "", "path to YAML configuration file")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if flags.NArg() != 0 || (*probe && !*spikeMode) || (*spikeMode && *configPath != "") {
		return errors.New("unexpected arguments; --probe requires --spike, which cannot use --config")
	}
	var instance *desktopWindow
	if *spikeMode {
		instance, err = newSpike(*probe)
	} else {
		instance, err = newDesktop(*configPath)
	}
	if err != nil {
		showStartupError(instance, err)
		return err
	}
	defer func() { err = errors.Join(err, instance.close()) }()
	return instance.run()
}

func newSpike(probe bool) (*desktopWindow, error) {
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
	return &desktopWindow{controller: controller, cfg: cfg, temporaryDir: directory, probe: probe}, nil
}

func (s *desktopWindow) close() error {
	s.stop.Do(func() {
		if s.controller != nil {
			s.stopErr = s.controller.Shutdown()
		}
		if s.temporaryDir != "" {
			s.stopErr = errors.Join(s.stopErr, os.RemoveAll(s.temporaryDir))
		}
	})
	return s.stopErr
}

func (s *desktopWindow) run() error {
	launchURL, err := s.controller.Bridge.LaunchURL()
	if err != nil {
		return err
	}
	if s.native == nil {
		s.createNative("")
	}
	js := ""
	if s.probe {
		s.controller.Bridge.Register("probe", http.HandlerFunc(s.probeMounted))
		js = probeJS
	}
	window := s.native.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Visual Tmux Client",
		Width: initialWidth, Height: initialHeight, URL: launchURL, JS: js,
	})
	s.window.Store(window)
	attachDownload(s.controller, s.native, window, s.cfg.Files.Enabled, s.cfg.Files.MaxFileSize)
	s.attachLifecycle(window)
	if err := s.attachNavigation(window, launchURL); err != nil {
		return err
	}
	go s.watch()
	if err := s.native.Run(); err != nil {
		return err
	}
	if s.probe && (!s.passed.Load() || !s.quitApproved.Load()) {
		return errors.New("native authentication/reload/flushed-quit probe did not pass")
	}
	return nil
}

func (s *desktopWindow) probeMounted(w http.ResponseWriter, r *http.Request) {
	var result struct {
		Stage int `json:"stage"`
	}
	if desktop.DecodeJSON(r, &result) != nil {
		http.Error(w, "invalid probe", http.StatusBadRequest)
		return
	}
	switch result.Stage {
	case probeFinalStage:
		s.passed.Store(true)
		fmt.Fprintln(os.Stderr, "spike: PASS authenticated HTTP action and both reload modes; closing window")
		time.AfterFunc(probeCloseDelay, func() { s.window.Load().Close() })
	case 1:
		address, err := s.freshLaunchURL()
		if err != nil {
			http.Error(w, "reload unavailable", http.StatusServiceUnavailable)
			return
		}
		desktop.WriteJSON(w, map[string]string{"launchURL": address})
		return
	default:
		if !s.probeNavigation(r.Context()) {
			http.Error(w, "native navigation guard probe failed", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintln(os.Stderr, "spike: authenticated HTTP action and Vue mount verified; reloading")
	}
	desktop.WriteJSON(w, map[string]bool{"ok": true})
}

func (s *desktopWindow) watch() {
	var timeout <-chan time.Time
	if s.probe {
		timer := time.NewTimer(probeTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case err := <-s.controller.Errors:
		if state := s.lifecycle.State(); state == desktop.Stopping || state == desktop.Stopped {
			return
		}
		s.frontendReady.Store(false)
		if cleanupErr := s.close(); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "desktop backend cleanup:", cleanupErr)
		}
		fmt.Fprintln(os.Stderr, "desktop backend stopped:", err)
		dialog := s.native.Dialog.Error().SetTitle("Backend stopped / 后端已停止").
			SetMessage("Restart the application. / 请重启应用。 Configuration and state ownership may need attention.")
		dialog.AddButton("Quit / 退出").SetAsDefault().OnClick(s.lifecycle.Stop)
		dialog.Show()
	case <-timeout:
		fmt.Fprintln(os.Stderr, "spike: FAIL native load/reload probe timed out")
		s.lifecycle.Stop()
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
  const stage = Number(sessionStorage.getItem('vtc-spike-stage') || '0');
  const response = await fetch('/api/desktop/probe', {
   method: 'POST', headers: {
    'Content-Type':'application/json', Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')
   },
   body: JSON.stringify({stage})
  });
  if (!response.ok) return;
  if (stage === 0) { sessionStorage.setItem('vtc-spike-stage','1'); location.reload(); }
  else if (stage === 1) {
   const result = await response.json();
   sessionStorage.setItem('vtc-spike-stage','2'); location.replace(result.launchURL);
  } else sessionStorage.removeItem('vtc-spike-stage');
 },100);
})()`
