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
	"path/filepath"
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
	startupProbe    bool
	fixtureJS       string // populated only by native test fixtures
	passed          atomic.Bool
	frontendReady   atomic.Bool
	navigationReady atomic.Bool
	dialogsPassed   atomic.Bool
	downloadPassed  atomic.Bool
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
	startupProbe := flags.Bool("probe-startup", false, "verify startup/auth/reload/quit only; excludes save/input")
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
	if flags.NArg() != 0 || ((*probe || *startupProbe) && !*spikeMode) ||
		(*probe && *startupProbe) || (*spikeMode && *configPath != "") {
		return errors.New("unexpected arguments; select one probe with --spike, which cannot use --config")
	}
	var instance *desktopWindow
	if *spikeMode {
		instance, err = newSpike(*probe || *startupProbe)
	} else {
		instance, err = newDesktop(*configPath)
	}
	if err != nil {
		showStartupError(instance, err)
		return err
	}
	defer func() { err = errors.Join(err, instance.close()) }()
	instance.startupProbe = *startupProbe
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
	if probe {
		if err := prepareSaveFixture(&cfg, directory); err != nil {
			return nil, errors.Join(err, os.RemoveAll(directory))
		}
	}
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
	js := s.fixtureJS
	if s.probe {
		s.controller.Bridge.Register("probe", http.HandlerFunc(s.probeMounted))
		s.attachDialogProbe()
		s.attachSaveProbe()
		js = probeJS
		if s.startupProbe {
			fmt.Fprintln(os.Stderr, "spike: startup-only scope; save and input acceptance excluded")
			minimalPath := "/usr/bin:/bin:/usr/sbin:/sbin"
			fmt.Fprintln(os.Stderr, "spike: minimal package PATH:", os.Getenv("PATH") == minimalPath)
			js = "window.__vtcProbeStartupOnly = true;\n" + js
		}
	}
	window := s.native.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main", Title: "Visual Tmux Client",
		Width: initialWidth, Height: initialHeight, URL: launchURL, JS: js,
	})
	s.window.Store(window)
	probeDestination := ""
	if s.probe {
		probeDestination = s.probeDestination()
	}
	attachDownload(s.controller, s.native, window, s.cfg.Files.Enabled, s.cfg.Files.MaxFileSize, probeDestination)
	s.attachLifecycle(window)
	if err := s.attachNavigation(window, launchURL); err != nil {
		return err
	}
	go s.watch()
	if err := s.native.Run(); err != nil {
		return err
	}
	if s.probe && (!s.passed.Load() || !s.lifecycle.Confirmed()) {
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
		if !s.startupProbe && (!s.dialogsPassed.Load() || !s.downloadPassed.Load()) {
			http.Error(w, "native dialogs did not pass", http.StatusServiceUnavailable)
			return
		}
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
	desktop.WriteJSON(w, map[string]any{
		"ok": true, "sourcePath": filepath.Join(s.temporaryDir, "source", "fixture.bin"),
	})
}

func (s *desktopWindow) watch() {
	go s.controller.WatchBackend(s.native.Context(), s.backendFailed)
	var timeout <-chan time.Time
	if s.probe {
		timer := time.NewTimer(probeTimeout)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-timeout:
		fmt.Fprintln(os.Stderr, "spike: FAIL native load/reload probe timed out")
		s.lifecycle.Stop()
	case <-s.native.Context().Done():
	}
}

// This probe runs after actual frontend authentication and workspace rendering.
// No token is put in JavaScript source, logs or raw native messages.
const probeJS = `(() => {
 if (window.__vtcProbeStarted) return;
 window.__vtcProbeStarted = true;
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
  if (stage === 0) {
   const initial = await response.json();
   const action = async (name, body) => {
    const response = await fetch('/api/desktop/'+name, {
     method:'POST', headers:{'Content-Type':'application/json',
      Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')}, body:JSON.stringify(body)
    });
    if (!response.ok) throw new Error('Native integration probe failed: '+await response.text());
    return response.json();
   };
   if (!window.__vtcProbeStartupOnly) {
   for (const approved of [false,true]) {
    await action('probe-dialog',{approved,text:''});
    if (window.confirm('Native confirmation probe / 原生确认测试') !== approved) throw new Error('Confirmation mismatch');
   }
   const text = '中文-输入-✓';
   await action('probe-dialog',{approved:true,text});
   if (window.prompt('Native input probe / 原生输入测试','original') !== text) throw new Error('Prompt mismatch');
   await action('probe-dialog-result',{ok:true});
   for (const approved of [false,true]) {
    await action('probe-save',{approved});
    const result = await action('download',{hostId:'local',connectionRevision:1,path:initial.sourcePath});
    if (result.cancelled === approved) throw new Error('Save cancellation mismatch');
    await action('probe-save-result',{approved});
   }
   }
   sessionStorage.setItem('vtc-spike-stage','1'); location.reload();
  }
  else if (stage === 1) {
   const result = await response.json();
   sessionStorage.setItem('vtc-spike-stage','2'); location.replace(result.launchURL);
  } else sessionStorage.removeItem('vtc-spike-stage');
 },100);
 window.addEventListener('unhandledrejection', event => {
  fetch('/api/desktop/probe-error', {
   method:'POST', headers:{'Content-Type':'application/json',
    Authorization:'Bearer '+sessionStorage.getItem('vtc:desktop-token')},
   body:JSON.stringify({message:String(event.reason?.message || event.reason).slice(0,1024)})
  });
 });
})()`

func (s *desktopWindow) backendFailed(err error) {
	s.frontendReady.Store(false)
	if cleanupErr := s.close(); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "desktop backend cleanup:", cleanupErr)
	}
	fmt.Fprintln(os.Stderr, "desktop backend stopped:", err)
	dialog := s.native.Dialog.Error().SetTitle("Backend stopped / 后端已停止").
		SetMessage("Restart the application. / 请重启应用。 Configuration and state ownership may need attention.")
	dialog.AddButton("Quit / 退出").SetAsDefault().OnClick(s.lifecycle.Stop)
	dialog.Show()
}
