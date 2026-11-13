//go:build desktop && linux

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	if os.Getenv("VTC_NATIVE_FIXTURE_MODE") == "real-local" {
		nativeFixtureRunner = runRealLocalFixture
		nativeFixtureGuard = true
	}
}

func TestNativeRealLocalTerminal(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("native display required")
	}
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux required")
	}
	sockets := testutil.SocketDir(t)
	t.Setenv("TMUX_TMPDIR", sockets)
	client := tmux.NewClient(path, "g")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	_, stderr, code, err := client.Exec(ctx, "-f", "/dev/null", "new-session", "-d", "-s", "real-gui",
		"while :; do printf '\\033[32mREAL_GUI_中文\\033[0m\\n'; sleep 0.2; done")
	if err != nil || code != 0 {
		t.Fatalf("private server: %v %d %s", err, code, stderr)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if _, _, _, err := client.Exec(cleanup, "kill-server"); err != nil {
			t.Log(err)
		}
	})
	remote := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User: "gui-test", Password: "fixture-only", TmuxSocketName: "r",
	})
	dropServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/drop" {
			http.NotFound(w, r)
			return
		}
		remote.DropConnections()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer dropServer.Close()
	for name, contents := range map[string]string{
		"draft.txt": "remote-draft\n", "README.md": "# Remote-中文\n", "fixture.bin": "\x00\xffremote-中文\n",
	} {
		if err := os.WriteFile(filepath.Join(remote.RootDir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", remote.SocketDir)
	remoteClient := tmux.NewClient(path, remote.TmuxSocket)
	_, stderr, code, err = remoteClient.Exec(ctx, "-f", "/dev/null", "new-session", "-d",
		"-s", "remote-gui", "-c", remote.RootDir,
		"i=0; while :; do i=$((i+1)); printf '\\033[34mREAL_REMOTE_中文_%s\\033[0m\\n' \"$i\"; sleep 0.2; done")
	t.Setenv("TMUX_TMPDIR", sockets)
	if err != nil || code != 0 {
		t.Fatalf("private remote server: %v %d %s", err, code, stderr)
	}
	directory := t.TempDir()
	enrollment, err := json.Marshal(map[string]any{
		"address": remote.Addr, "port": remote.Port, "username": remote.User,
		"password": remote.Password, "fingerprint": remote.Fingerprint,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "enrollment.json"), enrollment, 0600); err != nil {
		t.Fatal(err)
	}

	for round := range 2 {
		if round == 1 {
			fingerprint := remote.RotateHostKey()
			remote.DropConnections()
			var details map[string]any
			if err := json.Unmarshal(enrollment, &details); err != nil {
				t.Fatal(err)
			}
			details["fingerprint"] = fingerprint
			rotated, err := json.Marshal(details)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "enrollment.json"), rotated, 0600); err != nil {
				t.Fatal(err)
			}
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeRealLocalTerminal$")
		command.Env = append(tmux.ScrubbedEnv(), "VTC_NATIVE_SIGNAL_FIXTURE=1", "VTC_NATIVE_FIXTURE_MODE=real-local",
			"VTC_NATIVE_SIGNAL_STATE="+directory, "VTC_REAL_SOCKET_DIR="+sockets, "VTC_REAL_TMUX="+path,
			"VTC_REAL_DROP="+dropServer.URL+"/drop", fmt.Sprintf("VTC_REAL_ROUND=%d", round))
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "REAL_LOCAL_PASS") {
			t.Fatalf("real native round %d: %v\n%s", round, err, desktop.RedactDiagnostic(string(output)))
		}
		contents, readErr := os.ReadFile(filepath.Join(remote.RootDir, "draft.txt"))
		if readErr != nil || string(contents) != fmt.Sprintf("remote-saved-中文-%d\n", round) {
			t.Fatal("remote editor save did not reach SFTP source")
		}
		stdout, _, code, err := client.Exec(ctx, "list-sessions", "-F", "#{session_name}")
		if err != nil || code != 0 || !strings.Contains(stdout, "real-gui") {
			t.Fatal("GUI exit lost tmux session")
		}
		t.Log(desktop.RedactDiagnostic(string(output)))
	}
}

func runRealLocalFixture() error {
	if err := os.Setenv("TMUX_TMPDIR", os.Getenv("VTC_REAL_SOCKET_DIR")); err != nil {
		return err
	}
	cfg := config.DefaultConfig()
	cfg.Tmux.Path = os.Getenv("VTC_REAL_TMUX")
	cfg.Files.Enabled = true
	cfg.Files.Roots = []string{os.Getenv("VTC_NATIVE_SIGNAL_STATE")}
	cfg.Hosts.StateDir = filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "state")
	controller, err := desktop.Start(context.Background(), &cfg, config.Provenance{},
		web.FS(), app.Options{Socket: "g"})
	if err != nil {
		return err
	}
	enrollment, err := os.ReadFile(filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "enrollment.json"))
	if err != nil {
		return err
	}
	fixtureJS := "window.__vtcEnrollment=" + string(enrollment) + ";\n" +
		"window.__vtcRealRound=" + os.Getenv("VTC_REAL_ROUND") + ";\n" + realLocalJS
	s := &desktopWindow{cfg: cfg, controller: controller, fixtureJS: fixtureJS}
	processes := &workspaceProcesses{}
	destination := filepath.Join(os.Getenv("VTC_NATIVE_SIGNAL_STATE"), "download-"+os.Getenv("VTC_REAL_ROUND")+".bin")
	var passed atomic.Bool
	controller.Bridge.Register("fixture-real-local", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message string `json:"message"`
			Failed  bool   `json:"failed"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "bad fixture request", 400)
			return
		}
		if body.Message != "" {
			if body.Message == "remote download requested" {
				if os.Getenv("VTC_PRIVATE_PORTAL") == "1" {
					processes.startPortal(destination, "save")
				} else {
					application.InvokeSync(func() {
						scheduleSaveProbe(s.window.Load().NativeWindow(), true, destination)
					})
				}
			}
			if body.Message == "remote download result" {
				contents, err := os.ReadFile(destination)
				if err != nil || string(contents) != "\x00\xffremote-中文\n" {
					http.Error(w, "remote download incomplete", http.StatusConflict)
					return
				}
				if processes.portal != nil {
					if err := <-processes.portal; err != nil {
						http.Error(w, "portal failed", 500)
						return
					}
					processes.portal = nil
				}
			}
			if body.Message == "remote save requested" {
				application.InvokeSync(func() { scheduleDialogProbe(s.window.Load().NativeWindow(), true, "") })
			}
			if body.Message == "drop remote" {
				request, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
					os.Getenv("VTC_REAL_DROP"), nil)
				if err != nil {
					http.Error(w, "drop request invalid", 500)
					return
				}
				client := &http.Client{Timeout: 3 * time.Second}
				response, err := client.Do(request)
				if err != nil {
					http.Error(w, "drop request failed", 500)
					return
				}
				if err := response.Body.Close(); err != nil {
					http.Error(w, "drop response failed", 500)
					return
				}
			}
			fmt.Fprintln(os.Stderr, "REAL_HOST_CHECK:", body.Message)
			desktop.WriteJSON(w, map[string]bool{"ok": true})
			if body.Failed {
				go s.lifecycle.Stop()
			}
			return
		}
		passed.Store(true)
		desktop.WriteJSON(w, map[string]bool{"ok": true})
		s.lifecycle.RequestQuit()
	}))
	s.createNative("")
	if err := s.run(); err != nil {
		return err
	}
	if !passed.Load() {
		return errors.New("real local GUI did not finish")
	}
	_, err = fmt.Fprintln(os.Stdout, "REAL_LOCAL_PASS: real tmux ANSI/Chinese output rendered; native quit completed")
	return err
}

//go:embed testdata/real-local.js
var realLocalJS string
