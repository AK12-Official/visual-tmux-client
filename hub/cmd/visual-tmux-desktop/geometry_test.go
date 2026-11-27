//go:build desktop && (linux || darwin)

package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	if os.Getenv("VTC_NATIVE_FIXTURE_MODE") == "geometry" {
		nativeFixtureRunner = runGeometryFixture
	}
}

// The DOM tests run in jsdom, which performs no layout. This fixture renders
// the real workspace in the platform WebView, switches language through the
// real language selector and compares the size of every visible control.
func TestNativeControlGeometry(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("native display required")
	}
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Private state and synthetic sessions only: the child never reads the
	// implicit configuration, the saved hosts or a real tmux server.
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeControlGeometry$")
	command.Dir = directory
	command.Env = append(tmux.FilterEnv(tmux.ScrubbedEnv(),
		"VISUAL_TMUX_CLIENT_CONFIG", "VISUAL_TMUX_CLIENT_TMUX_PATH", "VISUAL_TMUX_CLIENT_HOSTS_STATE_DIR",
		"VISUAL_TMUX_CLIENT_HOSTS_MASTER_KEY_FILE"),
		"VTC_NATIVE_SIGNAL_FIXTURE=1", "VTC_NATIVE_FIXTURE_MODE=geometry", "VTC_NATIVE_SIGNAL_STATE="+directory)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "GEOMETRY_PASS") {
		t.Fatalf("native control geometry: %v\n%s", err, desktop.RedactDiagnostic(string(output)))
	}
	t.Log(desktop.RedactDiagnostic(string(output)))
}

const geometryTimeout = 45 * time.Second

func runGeometryFixture() error {
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = os.Getenv("VTC_NATIVE_SIGNAL_STATE")
	if err := os.MkdirAll(cfg.Hosts.StateDir, 0700); err != nil {
		return err
	}
	controller, err := desktop.Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		return err
	}
	s := &desktopWindow{cfg: cfg, controller: controller, fixtureJS: geometryJS}
	s.createNative("")
	var passed atomic.Bool
	finished := make(chan string, 1)
	// VTC_GEOMETRY_HOLD_MS keeps each scene on screen before the next one, for
	// looking at the layout by hand. It changes nothing about the comparison.
	hold, _ := time.ParseDuration(os.Getenv("VTC_GEOMETRY_HOLD_MS") + "ms")
	controller.Bridge.Register("fixture-geometry", geometryHandler(&passed, finished, hold))
	go func() {
		var reason string
		select {
		case reason = <-finished:
		case <-time.After(geometryTimeout + hold*time.Duration(len(geometryScenes)*2)):
			reason = "the page did not report every scene"
		case <-s.native.Context().Done():
			return
		}
		if reason != "" {
			fmt.Fprintln(os.Stderr, "GEOMETRY_FAIL:", reason)
		}
		// Closing the window takes the application's own confirmed-quit path.
		// Cocoa may end the process inside it without returning to Run, so
		// the result is printed here rather than after Run.
		application.InvokeAsync(func() {
			if s.window.Load() != nil {
				s.window.Load().Close()
			} else {
				s.native.Quit()
			}
		})
	}()
	if err := s.run(); err != nil {
		return err
	}
	if !passed.Load() {
		return errors.New("control geometry differs between languages")
	}
	return nil
}

type geometryControl struct {
	Key      string  `json:"key"`
	Class    string  `json:"class"`
	Text     string  `json:"text"`
	Width    float64 `json:"w"`
	Height   float64 `json:"h"`
	Label    float64 `json:"tw"`
	Overflow bool    `json:"overflow"`
}

type geometrySnapshot struct {
	Scene    string            `json:"scene"`
	Locale   string            `json:"locale"`
	Controls []geometryControl `json:"controls"`
}

var geometryScenes = []string{"workspace", "hosts", "add-host"}

// geometryHandler collects one snapshot per scene and language and, once all
// of them arrived, fails on any control whose width or height differs between
// the two languages and on any label that does not fit its control.
func geometryHandler(passed *atomic.Bool, finished chan<- string, hold time.Duration) http.Handler {
	started := time.Now()
	var mu sync.Mutex
	snapshots := map[string]map[string]geometrySnapshot{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Phase   string           `json:"phase"`
			Message string           `json:"message"`
			Snap    geometrySnapshot `json:"snapshot"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid fixture", http.StatusBadRequest)
			return
		}
		switch body.Phase {
		case "snapshot":
			mu.Lock()
			if snapshots[body.Snap.Scene] == nil {
				snapshots[body.Snap.Scene] = map[string]geometrySnapshot{}
			}
			snapshots[body.Snap.Scene][body.Snap.Locale] = body.Snap
			complete := true
			for _, scene := range geometryScenes {
				if len(snapshots[scene]) != 2 {
					complete = false
				}
			}
			differences := []string{}
			if complete {
				differences = compareGeometry(snapshots)
			}
			mu.Unlock()
			if hold > 0 {
				time.Sleep(hold)
			}
			if !complete {
				desktop.WriteJSON(w, map[string]bool{"ok": true})
				return
			}
			if len(differences) != 0 {
				select {
				case finished <- strings.Join(differences, "\n"):
				default:
				}
				http.Error(w, "control geometry differs between languages", http.StatusConflict)
				return
			}
			// Cocoa may terminate inside the confirmed quit without returning
			// to Run, so the result is reported before the window closes.
			passed.Store(true)
			fmt.Fprintln(os.Stdout, "GEOMETRY_PASS")
			select {
			case finished <- "":
			default:
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "checkpoint":
			if len(body.Message) <= 128 {
				fmt.Fprintf(os.Stderr, "GEOMETRY_STEP %6.1fs: %s\n", time.Since(started).Seconds(), body.Message)
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "error":
			message := body.Message
			if len(message) > 1024 {
				message = message[:1024]
			}
			select {
			case finished <- "the page reported: " + message:
			default:
			}
			desktop.WriteJSON(w, map[string]bool{"ok": false})
		default:
			http.Error(w, "unknown fixture phase", http.StatusBadRequest)
		}
	})
}

func compareGeometry(snapshots map[string]map[string]geometrySnapshot) []string {
	var differences, report []string
	for _, scene := range geometryScenes {
		english, chinese := snapshots[scene]["en"], snapshots[scene]["zh-CN"]
		if len(english.Controls) != len(chinese.Controls) {
			differences = append(differences, fmt.Sprintf("scene %s: %d visible controls in English and %d in Simplified Chinese",
				scene, len(english.Controls), len(chinese.Controls)))
		}
		for i := 0; i < len(english.Controls) && i < len(chinese.Controls); i++ {
			a, b := english.Controls[i], chinese.Controls[i]
			if a.Key != b.Key {
				differences = append(differences, fmt.Sprintf("scene %s: the controls are in a different order (%s, %s)", scene, a.Key, b.Key))
				break
			}
			report = append(report, fmt.Sprintf("GEOMETRY %-8s %-11s en=%5.1fx%-4.1f zh=%5.1fx%-4.1f label=%5.1f/%-5.1f %.28s %q / %q",
				scene, a.Key, a.Width, a.Height, b.Width, b.Height, a.Label, b.Label, a.Class, a.Text, b.Text))
			if math.Abs(a.Width-b.Width) > 0.5 || math.Abs(a.Height-b.Height) > 0.5 {
				differences = append(differences, fmt.Sprintf("scene %s %s (%s): %.1fx%.1f in English and %.1fx%.1f in Simplified Chinese",
					scene, a.Key, a.Class, a.Width, a.Height, b.Width, b.Height))
			}
			for _, control := range []geometryControl{a, b} {
				if control.Overflow {
					differences = append(differences, fmt.Sprintf("scene %s %s (%s): the label %q does not fit %.1fx%.1f",
						scene, control.Key, control.Class, control.Text, control.Width, control.Height))
				}
			}
		}
	}
	for _, line := range report {
		fmt.Fprintln(os.Stderr, line)
	}
	return differences
}

//go:embed testdata/geometry.js
var geometryJS string
