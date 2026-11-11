//go:build desktop && (linux || darwin)

package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

// Native Run must execute on the original main thread, including in fixtures.
func TestMain(m *testing.M) {
	if os.Getenv("VTC_NATIVE_SIGNAL_FIXTURE") == "1" {
		run := runSignalFixture
		if os.Getenv("VTC_NATIVE_FIXTURE_MODE") == "focus" {
			run = runFocusFixture
		}
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeSecondLaunchFocusesBeforeOpeningRegistry(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("native display required")
	}
	directory := t.TempDir()
	configBody := fmt.Sprintf("version: 1\ntmux:\n  path: %q\nhosts:\n  state_dir: %q\nfiles:\n  enabled: false\n",
		filepath.Join(directory, "not-installed-tmux"), directory)
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(configBody), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := func() *exec.Cmd {
		fixtureTest := "-test.run=^TestNativeSecondLaunchFocusesBeforeOpeningRegistry$"
		command := exec.CommandContext(ctx, os.Args[0], fixtureTest)
		command.Env = append(tmux.FilterEnv(tmux.ScrubbedEnv(),
			"VISUAL_TMUX_CLIENT_CONFIG", "VISUAL_TMUX_CLIENT_TMUX_PATH", "VISUAL_TMUX_CLIENT_HOSTS_STATE_DIR",
			"VISUAL_TMUX_CLIENT_HOSTS_MASTER_KEY_FILE"),
			"VTC_NATIVE_SIGNAL_FIXTURE=1", "VTC_NATIVE_FIXTURE_MODE=focus", "VTC_NATIVE_SIGNAL_STATE="+directory)
		command.Stderr = os.Stderr
		return command
	}
	first := start()
	stdout, err := first.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if first.ProcessState == nil {
			_ = first.Process.Kill() //nolint:errcheck // only the owned test child on failure
			_ = first.Wait()         //nolint:errcheck // reap the owned child
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "FOCUS_FIXTURE_HIDDEN" {
		t.Fatal("first window was not ready/hidden")
	}
	second := start()
	second.Stderr = nil
	if output, err := second.CombinedOutput(); err != nil {
		t.Fatalf("second launch opened/conflicted with registry: %v %s", err, output)
	}
	if !scanner.Scan() || scanner.Text() != "FOCUS_FIXTURE_VISIBLE" {
		t.Fatal("second launch did not focus first window")
	}
	if err := first.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
}

func runFocusFixture() error {
	directory := os.Getenv("VTC_NATIVE_SIGNAL_STATE")
	window, err := newDesktop(filepath.Join(directory, "config.yaml"))
	if err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if window.frontendReady.Load() {
				window.window.Load().Hide()
				if _, err := fmt.Fprintln(os.Stdout, "FOCUS_FIXTURE_HIDDEN"); err != nil {
					return
				}
				break
			}
		}
		for range ticker.C {
			if native := window.window.Load(); native.IsVisible() && native.IsFocused() {
				if _, err := fmt.Fprintln(os.Stdout, "FOCUS_FIXTURE_VISIBLE"); err != nil {
					return
				}
				return
			}
		}
	}()
	return window.run()
}

func TestNativeSignalReleasesListenerAndState(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("native display required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeSignalReleasesListenerAndState$")
	command.Env = append(os.Environ(), "VTC_NATIVE_SIGNAL_FIXTURE=1", "VTC_NATIVE_SIGNAL_STATE="+directory)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill() //nolint:errcheck // only this owned child on test failure
		}
		if command.ProcessState == nil {
			_ = command.Wait() //nolint:errcheck // reap this fixture after a failed assertion
		}
	})
	scanner := bufio.NewScanner(stdout)
	var address string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "FIXTURE_READY ") {
			address = strings.TrimPrefix(scanner.Text(), "FIXTURE_READY ")
			break
		}
	}
	if address == "" {
		t.Fatalf("native fixture did not become ready: %v", scanner.Err())
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("signal cleanup failed: %v", err)
	}
	if connection, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		_ = connection.Close() //nolint:errcheck // test-only failed assertion cleanup
		t.Fatal("listener survived signal shutdown")
	}
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = directory
	replacement, err := desktop.Start(ctx, &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		t.Fatalf("native signal retained state lock: %v", err)
	}
	if err := replacement.Shutdown(); err != nil {
		t.Fatal(err)
	}
}

func runSignalFixture() error {
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Hosts.StateDir = os.Getenv("VTC_NATIVE_SIGNAL_STATE")
	controller, err := desktop.Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		return err
	}
	window := &desktopWindow{cfg: cfg, controller: controller}
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if window.frontendReady.Load() {
				if _, err := fmt.Fprintln(os.Stdout, "FIXTURE_READY", controller.Addr()); err != nil {
					fmt.Fprintln(os.Stderr, "fixture output unavailable")
				}
				return
			}
		}
	}()
	return window.run()
}
