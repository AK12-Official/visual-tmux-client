//go:build desktop && linux

package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	if os.Getenv("VTC_NATIVE_FIXTURE_MODE") == "workspace" {
		nativeFixtureRunner = runWorkspaceFixture
	}
}

func TestNativeLinuxWorkspace(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("native display required")
	}
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeLinuxWorkspace$")
	command.Dir = directory
	command.Env = append(tmux.ScrubbedEnv(), "VTC_NATIVE_SIGNAL_FIXTURE=1", "VTC_NATIVE_FIXTURE_MODE=workspace",
		"VTC_NATIVE_SIGNAL_STATE="+directory, "GDK_DEBUG=no-portals", "GSK_RENDERER=cairo",
		"GSETTINGS_BACKEND=memory", "GIO_USE_VFS=local")
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		private := filepath.Join(directory, key)
		if err := os.Mkdir(private, 0700); err != nil {
			t.Fatal(err)
		}
		command.Env = append(tmux.FilterEnv(command.Env, key), key+"="+private)
	}
	if os.Getenv("VTC_PRIVATE_DISPLAY") == "1" {
		command.Env = append(command.Env, prepareWorkspaceBrowser(t, directory)...)
		query := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/http")
		query.Env = command.Env
		result, queryErr := query.CombinedOutput()
		if queryErr != nil || strings.TrimSpace(string(result)) != "vtc-browser.desktop" {
			t.Fatalf("private browser registration: %v %s", queryErr, result)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		for _, name := range []string{"browser.log"} {
			if log, readErr := os.ReadFile(filepath.Join(directory, name)); readErr == nil {
				t.Log(string(log))
			}
		}
		t.Fatalf("native workspace: %v\n%s", err, desktop.RedactDiagnostic(string(output)))
	}
	if !strings.Contains(string(output), "WORKSPACE_PASS") {
		t.Fatalf("missing workspace result: %s", output)
	}
	t.Log(desktop.RedactDiagnostic(string(output)))
}

func runWorkspaceFixture() error {
	directory := os.Getenv("VTC_NATIVE_SIGNAL_STATE")
	root := filepath.Join(directory, "source")
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "draft.txt"), []byte("original\n"), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Preview-中文\n"), 0600); err != nil {
		return err
	}
	cfg := config.DefaultConfig()
	cfg.Files.Enabled, cfg.Files.Roots, cfg.Hosts.StateDir = true, []string{root}, filepath.Join(directory, "state")
	processes := &workspaceProcesses{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" && r.Header.Get("Authorization") == "" {
			processes.externalHits.Add(1)
		}
		if _, err := io.WriteString(w, "<!doctype html><title>Private external browser fixture</title>"); err != nil {
			fmt.Fprintln(os.Stderr, "browser fixture response interrupted")
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	processes.httpURL, processes.httpsURL = server.URL+"/target", secure.URL+"/target"
	processes.browserLog = os.Getenv("VTC_BROWSER_LOG")
	controller, err := desktop.Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: workspaceBackend{root: root}, ProcessFact: processes,
	})
	if err != nil {
		return err
	}
	s := &desktopWindow{cfg: cfg, controller: controller, fixtureJS: workspaceJS}
	s.createNative("")
	var passed atomic.Bool
	controller.Bridge.Register("fixture-workspace", workspaceHandler(s, processes, &passed))
	go func() {
		select {
		case <-time.After(30 * time.Second):
			fmt.Fprintln(os.Stderr, "WORKSPACE_TIMEOUT")
			application.InvokeAsync(func() {
				if s.lifecycle != nil {
					s.lifecycle.Stop()
				}
			})
		case <-s.native.Context().Done():
		}
	}()
	if err := s.run(); err != nil {
		return err
	}
	if !passed.Load() || !s.lifecycle.Confirmed() || processes.closed.Load() != 1 {
		return errors.New("workspace confirmation/attachment cleanup did not pass")
	}
	contents, err := os.ReadFile(filepath.Join(root, "draft.txt"))
	if err != nil || string(contents) != "saved-中文\n" {
		return errors.New("discard changed the previously saved file")
	}
	_, err = fmt.Fprintln(os.Stdout,
		"WORKSPACE_PASS: dirty cancel preserves editor and streaming attachment; discard exits")
	return err
}

func workspaceHandler(s *desktopWindow, processes *workspaceProcesses, passed *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Phase    string  `json:"phase"`
			Message  string  `json:"message"`
			Approved bool    `json:"approved"`
			X        float64 `json:"x"`
			Y        float64 `json:"y"`
			EndX     float64 `json:"endX"`
			EndY     float64 `json:"endY"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid fixture", 400)
			return
		}
		switch body.Phase {
		case "status":
			processes.mu.Lock()
			input := string(processes.input)
			processes.mu.Unlock()
			browser, err := os.ReadFile(processes.browserLog)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				http.Error(w, "browser fixture log unreadable", 500)
				return
			}
			desktop.WriteJSON(w, map[string]any{"created": processes.created.Load(), "closed": processes.closed.Load(),
				"frames": processes.frames.Load(), "resizes": processes.resizes.Load(), "state": s.lifecycle.State(),
				"cols": processes.cols.Load(), "rows": processes.rows.Load(), "input": input,
				"fullscreen": s.window.Load().IsFullscreen(), "privateInput": os.Getenv("VTC_PRIVATE_DISPLAY") == "1",
				"sourcePath": filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "source", "draft.txt"),
				"httpURL":    processes.httpURL, "httpsURL": processes.httpsURL,
				"externalHits": processes.externalHits.Load(),
				"browserHTTP":  strings.Contains(string(browser), processes.httpURL),
				"browserHTTPS": strings.Contains(string(browser), processes.httpsURL),
				"browserDone":  strings.Count(string(browser), `"done": true`)})
		case "saved":
			source := filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "source", "draft.txt")
			contents, err := os.ReadFile(source)
			if err != nil || string(contents) != "saved-中文\n" {
				http.Error(w, "editor save did not reach disk", http.StatusConflict)
				return
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "overwrite":
			destination := filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "download.txt")
			if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
				http.Error(w, "cannot prepare destination", http.StatusInternalServerError)
				return
			}
			ready := false
			application.InvokeSync(func() {
				pointer := s.window.Load().NativeWindow()
				ready = scheduleSaveProbe(pointer, true, destination) && scheduleOverwrite(pointer, body.Approved)
			})
			if !ready {
				http.Error(w, "chooser unavailable", http.StatusServiceUnavailable)
				return
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "overwrite-result":
			destination := filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "download.txt")
			contents, err := os.ReadFile(destination)
			expected := "keep"
			if body.Approved {
				expected = "original\n"
			}
			if err != nil || string(contents) != expected {
				http.Error(w, "overwrite destination differs", http.StatusConflict)
				return
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "clipboard-status":
			var text string
			application.InvokeSync(func() { text, _ = s.native.Clipboard.Text() })
			desktop.WriteJSON(w, map[string]string{"text": text})
		case "resize":
			go func() {
				for _, size := range [][2]int{{1100, 700}, {1000, 650}, {1180, 760}, {1050, 720}, {1160, 780}} {
					s.window.Load().SetSize(size[0], size[1])
					time.Sleep(70 * time.Millisecond)
				}
			}()
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "pause", "resume":
			processes.paused.Store(body.Phase == "pause")
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "pointer", "drag", "key-copy", "key-paste", "commit", "wheel-up", "wheel-down":
			ok := false
			application.InvokeSync(func() {
				switch body.Phase {
				case "pointer", "drag":
					ok = privatePointer(s.window.Load().NativeWindow(), body.X, body.Y,
						body.EndX, body.EndY, body.Phase == "drag")
				case "key-copy", "key-paste":
					ok = privateKey(s.window.Load().NativeWindow(), body.Phase == "key-paste")
				case "wheel-up", "wheel-down":
					ok = privateWheel(s.window.Load().NativeWindow(), body.Phase == "wheel-down")
				case "commit":
					ok = inputCommit(s.window.Load().NativeWindow(), "输入-中文")
				}
			})
			if !ok {
				http.Error(w, "native input unavailable", http.StatusServiceUnavailable)
				return
			}
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "cancel", "discard":
			if processes.created.Load() != 1 || processes.closed.Load() != 0 {
				http.Error(w, "attachment changed", http.StatusConflict)
				return
			}
			approved := body.Phase == "discard"
			application.InvokeSync(func() { scheduleDialogProbe(s.window.Load().NativeWindow(), approved, "") })
			if approved {
				passed.Store(true)
			}
			s.window.Load().Close()
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "checkpoint":
			fmt.Fprintln(os.Stderr, "WORKSPACE_CHECK:", desktop.RedactDiagnostic(body.Message))
			desktop.WriteJSON(w, map[string]bool{"ok": true})
		case "error":
			if len(body.Message) > 256 {
				body.Message = "oversized fixture error"
			}
			fmt.Fprintln(os.Stderr, "WORKSPACE_JS_ERROR:", desktop.RedactDiagnostic(body.Message))
			desktop.WriteJSON(w, map[string]bool{"ok": false})
			go s.lifecycle.Stop()
		default:
			http.Error(w, "unknown fixture phase", 400)
		}
	})
}

type workspaceProcesses struct {
	externalHits                                 atomic.Int32
	httpURL, httpsURL, browserLog                string
	created, closed, frames, resizes, cols, rows atomic.Int32
	paused                                       atomic.Bool
	mu                                           sync.Mutex
	input                                        []byte
}

type workspaceBackend struct {
	spike.Backend
	root string
}

func (b workspaceBackend) PaneWorkingDirectory(context.Context, string) (string, error) {
	return b.root, nil
}

func (f *workspaceProcesses) NewProcess(_ context.Context, _ string, cols, rows int) (terminal.Process, error) {
	r, w := io.Pipe()
	p := &workspaceProcess{reader: r, writer: w, done: make(chan struct{}), factory: f}
	f.created.Add(1)
	f.cols.Store(int32(cols))
	f.rows.Store(int32(rows))
	go func() {
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if f.paused.Load() {
					continue
				}
				if _, err := fmt.Fprintf(w, "\x1b[32mstream-中文-%d\x1b[0m\r\n", f.frames.Load()); err != nil {
					return
				}
				f.frames.Add(1)
			case <-p.done:
				return
			}
		}
	}()
	return p, nil
}

type workspaceProcess struct {
	reader   *io.PipeReader
	writer   *io.PipeWriter
	done     chan struct{}
	once     sync.Once
	factory  *workspaceProcesses
	closeErr error
}

func (p *workspaceProcess) Read(b []byte) (int, error) { return p.reader.Read(b) }
func (p *workspaceProcess) Write(b []byte) (int, error) {
	p.factory.mu.Lock()
	defer p.factory.mu.Unlock()
	p.factory.input = append(p.factory.input, b...)
	return len(b), nil
}
func (p *workspaceProcess) Resize(cols, rows int) error {
	p.factory.resizes.Add(1)
	p.factory.cols.Store(int32(cols))
	p.factory.rows.Store(int32(rows))
	return nil
}
func (p *workspaceProcess) Close() error {
	p.once.Do(func() {
		close(p.done)
		p.factory.closed.Add(1)
		p.closeErr = errors.Join(p.reader.Close(), p.writer.Close())
	})
	return p.closeErr
}
func (p *workspaceProcess) Detach() error       { return p.Close() }
func (p *workspaceProcess) Wait() (*int, error) { <-p.done; code := 0; return &code, nil }

//go:embed testdata/workspace.js
var workspaceJS string

//go:embed testdata/browser.py
var workspaceBrowser string

func prepareWorkspaceBrowser(t *testing.T, directory string) []string {
	t.Helper()
	browser, err := exec.LookPath("google-chrome")
	if err != nil {
		t.Fatal("private browser verification requires google-chrome")
	}
	launcher := filepath.Join(directory, "browser.py")
	if err := os.WriteFile(launcher, []byte(workspaceBrowser), 0700); err != nil {
		t.Fatal(err)
	}
	applications := filepath.Join(directory, "XDG_DATA_HOME", "applications")
	if err := os.MkdirAll(applications, 0700); err != nil {
		t.Fatal(err)
	}
	entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Private browser fixture\n"+
		"Exec=/usr/bin/python3 %s %%u\n"+
		"MimeType=x-scheme-handler/http;x-scheme-handler/https;\nNoDisplay=true\n", launcher)
	if err := os.WriteFile(filepath.Join(applications, "vtc-browser.desktop"), []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	mime := "[Default Applications]\nx-scheme-handler/http=vtc-browser.desktop\n" +
		"x-scheme-handler/https=vtc-browser.desktop\n"
	mimePath := filepath.Join(directory, "XDG_CONFIG_HOME", "mimeapps.list")
	if err := os.WriteFile(mimePath, []byte(mime), 0600); err != nil {
		t.Fatal(err)
	}
	profiles := filepath.Join(directory, "browsers")
	if err := os.Mkdir(profiles, 0700); err != nil {
		t.Fatal(err)
	}
	return []string{"BROWSER=" + launcher, "VTC_BROWSER_ROOT=" + profiles, "VTC_BROWSER_BINARY=" + browser,
		"VTC_BROWSER_LOG=" + filepath.Join(directory, "browser.log"), "XDG_CURRENT_DESKTOP=X-Generic"}
}
