package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesktopConfiguration(t *testing.T) {
	base := t.TempDir()
	appDir := filepath.Join(base, "visual-tmux-client")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(appDir, DefaultImplicitConfigFile)
	yaml := `server:
  addr: public-invalid
auth:
  token: YAML-secret
websocket:
  origin: invalid-origin
web:
  terminal:
    font_size: 15
hosts:
  state_dir: state
tmux:
  path: bin/tmux
`
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	env := func(key string) (string, bool) {
		switch key {
		case "VISUAL_TMUX_CLIENT_ADDR", "VISUAL_TMUX_CLIENT_TOKEN", "VISUAL_TMUX_CLIENT_ORIGIN":
			return "ENV-secret-invalid", true
		}
		return "", false
	}
	for _, cwd := range []string{t.TempDir(), t.TempDir()} {
		cfg, prov, err := LoadDesktop(nil, cwd, base, env)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Server.Addr != "127.0.0.1:0" || cfg.Auth.Token != "" || cfg.WebSocket.Origin != "" {
			t.Fatal("desktop transport not managed")
		}
		if prov.ConfigFile != path || prov.TokenSource != SourceDesktop || cfg.Web.Terminal.FontSize != 15 {
			t.Fatalf("configuration source lost: %+v", prov)
		}
		if cfg.Hosts.StateDir != filepath.Join(appDir, "state") || cfg.Tmux.Path != filepath.Join(appDir, "bin/tmux") {
			t.Fatal("YAML paths not relative to configuration")
		}
	}
	if _, _, err := LoadDesktop([]string{"--addr", "127.0.0.1:42"}, base, base, env); err == nil {
		t.Fatal("accepted desktop --addr")
	}
	// The headless loader continues validating its original transport settings.
	if _, _, err := Load([]string{"--config", path}, base, env, "test"); err == nil {
		t.Fatal("headless transport validation changed")
	}
}

func TestDesktopExplicitAndInvalidConfiguration(t *testing.T) {
	cwd, userDir := t.TempDir(), t.TempDir()
	const file = "explicit.yaml"
	if err := os.WriteFile(filepath.Join(cwd, file), []byte("files:\n  enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) (string, bool) { return file, key == "VISUAL_TMUX_CLIENT_CONFIG" }
	for _, args := range [][]string{nil, {"--config", file}} {
		cfg, prov, err := LoadDesktop(args, cwd, userDir, lookup)
		if err != nil || cfg.Files.Enabled || prov.ConfigFile != filepath.Join(cwd, file) {
			t.Fatalf("explicit selection failed: cfg=%v prov=%v err=%v", cfg, prov, err)
		}
	}
	inputs := []string{
		"unknown: true",
		"auth:\n  token: private-secret\nfiles:\n  max_file_size: 0",
		"files:\n  enabled: false\n  enabled: true",
	}
	for _, input := range inputs {
		if err := os.WriteFile(filepath.Join(cwd, file), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadDesktop(nil, cwd, userDir, lookup)
		if err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("unsafe validation: %v", err)
		}
	}
	for arg, expected := range map[string]error{"--help": flag.ErrHelp, "--version": ErrVersion} {
		_, _, err := LoadDesktop([]string{arg}, cwd, userDir, lookup)
		if !errors.Is(err, expected) {
			t.Fatalf("informational flag %s: %v", arg, err)
		}
	}
	if _, _, err := LoadDesktop([]string{"--config", "missing.yaml"}, cwd, userDir, lookup); err == nil {
		t.Fatal("missing explicit file accepted")
	}
}
