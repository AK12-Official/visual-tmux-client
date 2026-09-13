package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultConfigRoundtrip(t *testing.T) {
	def := DefaultConfig()

	// Marshal to YAML
	data, err := yaml.Marshal(&def)
	if err != nil {
		t.Fatalf("failed to marshal default config: %v", err)
	}

	// Validate AST
	if _, err := ValidateYAMLDocument(data); err != nil {
		t.Fatalf("ValidateYAMLDocument failed on serialized default config: %v", err)
	}

	// Load via temporary file
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	loaded, prov, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("Load failed on default config roundtrip: %v", err)
	}

	if loaded.Version != 1 {
		t.Errorf("expected version 1, got %d", loaded.Version)
	}
	if loaded.Server.Addr != def.Server.Addr {
		t.Errorf("expected addr %s, got %s", def.Server.Addr, loaded.Server.Addr)
	}
	if loaded.Server.ReadHeaderTimeout != def.Server.ReadHeaderTimeout {
		t.Errorf("expected read header timeout %v, got %v",
			def.Server.ReadHeaderTimeout, loaded.Server.ReadHeaderTimeout)
	}
	if loaded.Terminal.StagingBufferBytes != def.Terminal.StagingBufferBytes {
		t.Errorf("expected staging buffer bytes %d, got %d",
			def.Terminal.StagingBufferBytes, loaded.Terminal.StagingBufferBytes)
	}
	if loaded.Terminal.OutputLowWaterBytes != def.Terminal.OutputLowWaterBytes {
		t.Errorf("expected low water %d, got %d",
			def.Terminal.OutputLowWaterBytes, loaded.Terminal.OutputLowWaterBytes)
	}
	if loaded.Terminal.OutputHighWaterBytes != def.Terminal.OutputHighWaterBytes {
		t.Errorf("expected high water %d, got %d",
			def.Terminal.OutputHighWaterBytes, loaded.Terminal.OutputHighWaterBytes)
	}
	if loaded.Web.SessionPollInterval != def.Web.SessionPollInterval {
		t.Errorf("expected session poll interval %v, got %v",
			def.Web.SessionPollInterval, loaded.Web.SessionPollInterval)
	}
	if loaded.Web.Terminal.FontSize != def.Web.Terminal.FontSize {
		t.Errorf("expected font size %d, got %d", def.Web.Terminal.FontSize, loaded.Web.Terminal.FontSize)
	}
	if prov.AddrSource != SourceYAML {
		t.Errorf("expected AddrSource YAML, got %s", prov.AddrSource)
	}
}

func TestLoadPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	yamlContent := `
server:
  addr: "127.0.0.1:8080"
auth:
  token: "yaml-token"
websocket:
  origin: "http://yaml-origin:8080"
tmux:
  path: "bin/tmux"
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	dummyLookup := func(k string) (string, bool) { return "", false }

	// 1. YAML values applied when no env or CLI flags provided
	cfg, prov, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:8080" || prov.AddrSource != SourceYAML {
		t.Errorf("addr: got %s (%s)", cfg.Server.Addr, prov.AddrSource)
	}
	if cfg.Auth.Token != "yaml-token" || prov.TokenSource != SourceYAML {
		t.Errorf("token: got %s (%s)", cfg.Auth.Token, prov.TokenSource)
	}
	if cfg.WebSocket.Origin != "http://yaml-origin:8080" || prov.OriginSource != SourceYAML {
		t.Errorf("origin: got %s (%s)", cfg.WebSocket.Origin, prov.OriginSource)
	}
	expectedTmux := filepath.Join(tmpDir, "bin/tmux")
	if cfg.Tmux.Path != expectedTmux || prov.TmuxPathSource != SourceYAML {
		t.Errorf("tmux path: got %s (%s), expected %s", cfg.Tmux.Path, prov.TmuxPathSource, expectedTmux)
	}

	// 2. Env overrides YAML
	envMap := map[string]string{
		"VISUAL_TMUX_CLIENT_ADDR":      "127.0.0.1:9090",
		"VISUAL_TMUX_CLIENT_TOKEN":     "env-token",
		"VISUAL_TMUX_CLIENT_ORIGIN":    "http://env-origin:9000",
		"VISUAL_TMUX_CLIENT_TMUX_PATH": "env-tmux",
	}
	lookup := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}
	cfg, prov, err = Load([]string{"--config", cfgPath}, tmpDir, lookup, "v1.0")
	if err != nil {
		t.Fatalf("Load with env failed: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9090" || prov.AddrSource != SourceEnvironment {
		t.Errorf("env addr: got %s (%s), expected 127.0.0.1:9090 and SourceEnvironment",
			cfg.Server.Addr, prov.AddrSource)
	}
	if cfg.Auth.Token != "env-token" || prov.TokenSource != SourceEnvironment {
		t.Errorf("env token: got %s (%s)", cfg.Auth.Token, prov.TokenSource)
	}
	if cfg.WebSocket.Origin != "http://env-origin:9000" || prov.OriginSource != SourceEnvironment {
		t.Errorf("env origin: got %s (%s)", cfg.WebSocket.Origin, prov.OriginSource)
	}
	if cfg.Tmux.Path != filepath.Join(tmpDir, "env-tmux") || prov.TmuxPathSource != SourceEnvironment {
		t.Errorf("env tmux path: got %s (%s)", cfg.Tmux.Path, prov.TmuxPathSource)
	}

	// 3. Explicit empty string in Env overrides YAML (generates token, default origin, PATH tmux)
	emptyEnvMap := map[string]string{
		"VISUAL_TMUX_CLIENT_TOKEN":     "",
		"VISUAL_TMUX_CLIENT_ORIGIN":    "",
		"VISUAL_TMUX_CLIENT_TMUX_PATH": "",
	}
	emptyLookup := func(k string) (string, bool) {
		v, ok := emptyEnvMap[k]
		return v, ok
	}
	cfg, prov, err = Load([]string{"--config", cfgPath}, tmpDir, emptyLookup, "v1.0")
	if err != nil {
		t.Fatalf("Load with empty env failed: %v", err)
	}
	if cfg.Auth.Token != "" || prov.TokenSource != SourceGenerated {
		t.Errorf("empty env token: got %s (%s), expected empty and SourceGenerated",
			cfg.Auth.Token, prov.TokenSource)
	}
	if cfg.WebSocket.Origin != "" || prov.OriginSource != SourceEnvironment {
		t.Errorf("empty env origin: got %s (%s), expected empty and SourceEnvironment",
			cfg.WebSocket.Origin, prov.OriginSource)
	}
	if cfg.Tmux.Path != "" || prov.TmuxPathSource != SourceEnvironment {
		t.Errorf("empty env tmux path: got %s (%s), expected empty and SourceEnvironment",
			cfg.Tmux.Path, prov.TmuxPathSource)
	}

	// 4. CLI flag overrides Env, YAML and default, even if explicit default
	args := []string{"--config", cfgPath, "--addr", "127.0.0.1:7690"}
	cfg, prov, err = Load(args, tmpDir, lookup, "v1.0")
	if err != nil {
		t.Fatalf("Load with cli addr failed: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:7690" || prov.AddrSource != SourceCLI {
		t.Errorf("cli addr: got %s (%s), expected 127.0.0.1:7690 and SourceCLI",
			cfg.Server.Addr, prov.AddrSource)
	}
}

func TestLoadEnvConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	envYamlContent := `
server:
  addr: "127.0.0.1:8888"
`
	envCfgPath := filepath.Join(tmpDir, "env-config.yaml")
	if err := os.WriteFile(envCfgPath, []byte(envYamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cliYamlContent := `
server:
  addr: "127.0.0.1:7777"
`
	cliCfgPath := filepath.Join(tmpDir, "cli-config.yaml")
	if err := os.WriteFile(cliCfgPath, []byte(cliYamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	lookup := func(k string) (string, bool) {
		if k == "VISUAL_TMUX_CLIENT_CONFIG" {
			return envCfgPath, true
		}
		return "", false
	}

	// 1. VISUAL_TMUX_CLIENT_CONFIG is used when --config is omitted
	cfg, prov, err := Load([]string{}, tmpDir, lookup, "v1.0")
	if err != nil {
		t.Fatalf("Load with env config failed: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:8888" {
		t.Errorf("expected addr from env config 127.0.0.1:8888, got %s", cfg.Server.Addr)
	}
	if !prov.ExplicitConfig || prov.ConfigFile != envCfgPath {
		t.Errorf("expected explicit config pointing to %s, got %+v", envCfgPath, prov)
	}

	// 2. CLI --config overrides VISUAL_TMUX_CLIENT_CONFIG
	cfg, prov, err = Load([]string{"--config", cliCfgPath}, tmpDir, lookup, "v1.0")
	if err != nil {
		t.Fatalf("Load with cli config overriding env failed: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:7777" {
		t.Errorf("expected addr from cli config 127.0.0.1:7777, got %s", cfg.Server.Addr)
	}
	if prov.ConfigFile != cliCfgPath {
		t.Errorf("expected config file %s, got %s", cliCfgPath, prov.ConfigFile)
	}
}

func TestLoadImplicitConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	yamlContent := `
server:
  addr: "127.0.0.1:9999"
`
	implicitFile := filepath.Join(tmpDir, DefaultImplicitConfigFile)
	if err := os.WriteFile(implicitFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	cfg, prov, err := Load([]string{}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("failed to load implicit config: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9999" || prov.AddrSource != SourceYAML {
		t.Errorf("expected 127.0.0.1:9999 from implicit YAML, got %s (%s)", cfg.Server.Addr, prov.AddrSource)
	}
	if prov.ConfigFile != implicitFile {
		t.Errorf("expected prov.ConfigFile %s, got %s", implicitFile, prov.ConfigFile)
	}
}

func TestLoadMissingImplicitConfigFileUsesDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	dummyLookup := func(k string) (string, bool) { return "", false }
	cfg, prov, err := Load([]string{}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("failed to load without config file: %v", err)
	}
	if cfg.Server.Addr != DefaultServerAddr || prov.AddrSource != SourceDefault {
		t.Errorf("expected default addr, got %s (%s)", cfg.Server.Addr, prov.AddrSource)
	}
	if prov.ConfigFile != "" {
		t.Errorf("expected empty prov.ConfigFile, got %s", prov.ConfigFile)
	}
}

func TestLoadMissingExplicitConfigFileFails(t *testing.T) {
	tmpDir := t.TempDir()
	nonExistent := filepath.Join(tmpDir, "missing.yaml")
	dummyLookup := func(k string) (string, bool) { return "", false }
	_, _, err := Load([]string{"--config", nonExistent}, tmpDir, dummyLookup, "v1.0")
	if err == nil {
		t.Fatalf("expected error loading missing explicit config file, got nil")
	}
	if !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("expected error to identify missing file path, got: %v", err)
	}
}

func TestPermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}
	tmpDir := t.TempDir()
	unreadableFile := filepath.Join(tmpDir, "unreadable.yaml")
	if err := os.WriteFile(unreadableFile, []byte("server:\n  addr: 127.0.0.1:8080\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(unreadableFile, 0o600); err != nil {
			t.Logf("chmod cleanup: %v", err)
		}
	}()

	dummyLookup := func(k string) (string, bool) { return "", false }
	_, _, err := Load([]string{"--config", unreadableFile}, tmpDir, dummyLookup, "v1.0")
	if err == nil {
		t.Fatalf("expected permission error, got nil")
	}

	implicitUnreadable := filepath.Join(tmpDir, DefaultImplicitConfigFile)
	if err := os.WriteFile(implicitUnreadable, []byte("server:\n  addr: 127.0.0.1:8080\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(implicitUnreadable, 0o600); err != nil {
			t.Logf("chmod cleanup: %v", err)
		}
	}()

	_, _, err = Load([]string{}, tmpDir, dummyLookup, "v1.0")
	if err == nil {
		t.Fatalf("expected error for unreadable implicit file, got nil")
	}
}

func TestHelpAndVersionBypass(t *testing.T) {
	tmpDir := t.TempDir()
	// Place invalid YAML in implicit file
	implicitFile := filepath.Join(tmpDir, DefaultImplicitConfigFile)
	if err := os.WriteFile(implicitFile, []byte("invalid: yaml: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Load([]string{"--help"}, tmpDir, nil, "v1.0")
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("expected ErrHelp, got: %v", err)
	}

	_, _, err = Load([]string{"--version"}, tmpDir, nil, "v1.0")
	if !errors.Is(err, ErrVersion) {
		t.Errorf("expected ErrVersion, got: %v", err)
	}
}

func TestStrictValidationRejections(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		errSubstr string
	}{
		{
			name:      "unknown key",
			yaml:      "unknown_key: true",
			errSubstr: "unknown configuration key",
		},
		{
			name: "duplicate key",
			yaml: `
server:
  addr: "127.0.0.1:7690"
  addr: "127.0.0.1:7691"
`,
			errSubstr: "duplicate key",
		},
		{
			name: "multiple documents",
			yaml: `
version: 1
---
version: 1
`,
			errSubstr: "multiple YAML documents are not supported",
		},
		{
			name: "null value",
			yaml: `
server:
  addr: null
`,
			errSubstr: "null values are not allowed",
		},
		{
			name: "duration as integer",
			yaml: `
server:
  read_header_timeout: 10
`,
			errSubstr: "must be a unit-bearing duration string",
		},
		{
			name: "byte limit as string",
			yaml: `
server:
  max_request_body_bytes: "65536"
`,
			errSubstr: "must be an integer",
		},
		{
			name: "unsupported version",
			yaml: `
version: 2
`,
			errSubstr: "unsupported version 2",
		},
		{
			name: "invalid address",
			yaml: `
server:
  addr: "invalid-host-port"
`,
			errSubstr: "invalid address",
		},
		{
			name: "invalid port 0",
			yaml: `
server:
  addr: "127.0.0.1:0"
`,
			errSubstr: "invalid port",
		},
		{
			name: "invalid port 70000",
			yaml: `
server:
  addr: "127.0.0.1:70000"
`,
			errSubstr: "invalid port",
		},
		{
			name: "invalid origin scheme",
			yaml: `
websocket:
  origin: "ftp://example.com"
`,
			errSubstr: "scheme must be http or https",
		},
		{
			name: "origin with path",
			yaml: `
websocket:
  origin: "http://example.com/ws"
`,
			errSubstr: "path, query, and fragment are not allowed",
		},
		{
			name: "token with newline does not disclose token",
			yaml: `
auth:
  token: "secret\ntoken"
`,
			errSubstr: "token contains invalid header character",
		},
		{
			name: "low water equal to high water",
			yaml: `
terminal:
  output_high_water_bytes: 1000
  output_low_water_bytes: 1000
`,
			errSubstr: "0 < low < high",
		},
		{
			name: "low water greater than high water",
			yaml: `
terminal:
  output_high_water_bytes: 1000
  output_low_water_bytes: 2000
`,
			errSubstr: "0 < low < high",
		},
		{
			name: "staging buffer below 32KiB",
			yaml: `
terminal:
  staging_buffer_bytes: 1024
`,
			errSubstr: "out of range [32768, 67108864]",
		},
		{
			name: "reconnect initial delay greater than max delay",
			yaml: `
web:
  reconnect:
    initial_delay: "5s"
    max_delay: "2s"
`,
			errSubstr: "initial_delay <= max_delay",
		},
		{
			name: "web duration not integer milliseconds",
			yaml: `
web:
  resize_debounce: "1500us"
`,
			errSubstr: "must be integer milliseconds",
		},
		{
			name: "font default outside min max",
			yaml: `
web:
  terminal:
    min_font_size: 10
    max_font_size: 20
    font_size: 25
`,
			errSubstr: "must be within [10, 20]",
		},
		{
			name: "font min greater than max",
			yaml: `
web:
  terminal:
    min_font_size: 20
    max_font_size: 10
`,
			errSubstr: "min_font_size (20) <= max",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			dummyLookup := func(k string) (string, bool) { return "", false }
			_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
			if err == nil {
				t.Fatalf("expected validation error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
			if strings.Contains(tc.name, "token") {
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("secret token was disclosed in error message: %v", err)
				}
			}
		})
	}
}

func TestExplicitNumericZeroPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	yamlContent := `
web:
  terminal:
    scrollback: 0
`
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	cfg, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Web.Terminal.Scrollback != 0 {
		t.Errorf("expected explicit 0 scrollback, got %d", cfg.Web.Terminal.Scrollback)
	}
}

func TestExampleYAMLMatchesDefaults(t *testing.T) {
	examplePath := filepath.Join("..", "..", "..", "configs", "visual-tmux-client.example.yaml")
	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("failed to read example yaml at %s: %v", examplePath, err)
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "example.yaml")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
	if err != nil {
		t.Fatalf("example YAML failed to load: %v", err)
	}

	expected := DefaultConfig()
	if !reflect.DeepEqual(*cfg, expected) {
		t.Errorf("example YAML differs from DefaultConfig():\ngot:  %+v\nwant: %+v", *cfg, expected)
	}
}

func TestConfig_UnsetFieldsAndEmptyYAML(t *testing.T) {
	dummyLookup := func(k string) (string, bool) { return "", false }

	t.Run("empty_document", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "empty.yaml")
		if err := os.WriteFile(cfgPath, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
		if err == nil || !strings.Contains(err.Error(), "empty YAML document") {
			t.Errorf("expected 'empty YAML document' error, got: %v", err)
		}
	})

	t.Run("empty_mapping", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "mapping.yaml")
		if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
		if err != nil {
			t.Fatalf("expected empty mapping to load successfully, got: %v", err)
		}
		def := DefaultConfig()
		if !reflect.DeepEqual(*cfg, def) {
			t.Errorf("expected empty mapping to populate all defaults, diff detected")
		}
	})

	t.Run("partial_server_only", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "partial.yaml")
		content := "server:\n  addr: \"127.0.0.1:9090\"\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, prov, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
		if err != nil {
			t.Fatalf("Load partial failed: %v", err)
		}
		if cfg.Server.Addr != "127.0.0.1:9090" || prov.AddrSource != SourceYAML {
			t.Errorf("expected 127.0.0.1:9090 from YAML, got %s", cfg.Server.Addr)
		}
		if cfg.Server.ReadHeaderTimeout != Duration(DefaultServerReadHeaderTimeout) {
			t.Errorf("expected default read_header_timeout, got %v", cfg.Server.ReadHeaderTimeout)
		}
	})
}

func TestConfig_NegativeNumbersAndBounds(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		errSubstr string
	}{
		{
			name:      "negative max_request_body_bytes",
			yaml:      "server:\n  max_request_body_bytes: -1\n",
			errSubstr: "out of range",
		},
		{
			name:      "zero max_request_body_bytes",
			yaml:      "server:\n  max_request_body_bytes: 0\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative websocket max_input_message_bytes",
			yaml:      "websocket:\n  max_input_message_bytes: -1\n",
			errSubstr: "out of range",
		},
		{
			name:      "zero websocket max_input_message_bytes",
			yaml:      "websocket:\n  max_input_message_bytes: 0\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative terminal max_dimension",
			yaml:      "terminal:\n  max_dimension: -10\n",
			errSubstr: "out of range",
		},
		{
			name:      "below min terminal max_dimension",
			yaml:      "terminal:\n  max_dimension: 79\n",
			errSubstr: "out of range [80, 65535]",
		},
		{
			name:      "negative staging_buffer_bytes",
			yaml:      "terminal:\n  staging_buffer_bytes: -1\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative output_high_water_bytes",
			yaml:      "terminal:\n  output_high_water_bytes: -100\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative output_low_water_bytes",
			yaml:      "terminal:\n  output_high_water_bytes: 1000\n  output_low_water_bytes: -10\n",
			errSubstr: "0 < low < high",
		},
		{
			name:      "zero output_low_water_bytes",
			yaml:      "terminal:\n  output_high_water_bytes: 1000\n  output_low_water_bytes: 0\n",
			errSubstr: "0 < low < high",
		},
		{
			name:      "negative scrollback",
			yaml:      "web:\n  terminal:\n    scrollback: -1\n",
			errSubstr: "out of range [0, 100000]",
		},
		{
			name:      "negative font_size",
			yaml:      "web:\n  terminal:\n    font_size: -1\n",
			errSubstr: "must be within",
		},
		{
			name:      "negative min_font_size",
			yaml:      "web:\n  terminal:\n    min_font_size: -5\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative max_toasts",
			yaml:      "web:\n  notifications:\n    max_toasts: -1\n",
			errSubstr: "out of range [2, 100]",
		},
		{
			name:      "below min max_toasts",
			yaml:      "web:\n  notifications:\n    max_toasts: 1\n",
			errSubstr: "out of range [2, 100]",
		},
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}
}

func TestConfig_ExtremeDurationsAndUnits(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		errSubstr string
	}{
		{
			name:      "zero duration below min",
			yaml:      "server:\n  read_header_timeout: \"0s\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "sub-millisecond duration below min",
			yaml:      "server:\n  read_header_timeout: \"500us\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "duration above 24h max",
			yaml:      "server:\n  read_header_timeout: \"25h\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "negative duration",
			yaml:      "server:\n  idle_timeout: \"-5s\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "ticket_ttl above 5m max",
			yaml:      "auth:\n  ticket_ttl: \"6m\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "ticket_ttl zero",
			yaml:      "auth:\n  ticket_ttl: \"0s\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "activity_decay above 5s max",
			yaml:      "web:\n  activity_decay: \"6s\"\n",
			errSubstr: "out of range",
		},
		{
			name:      "activity_throttle exceeds decay",
			yaml:      "web:\n  activity_decay: \"2s\"\n  activity_throttle: \"3s\"\n",
			errSubstr: "throttle <= activity_decay",
		},
		{
			name:      "invalid duration unit",
			yaml:      "server:\n  read_header_timeout: \"100xyz\"\n",
			errSubstr: "invalid duration",
		},
		{
			name:      "fractional millisecond browser timer",
			yaml:      "web:\n  session_poll_interval: \"1500us\"\n",
			errSubstr: "must be integer milliseconds",
		},
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}
}

func TestConfig_OriginAndTokenEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		errSubstr string
	}{
		{
			name:      "origin with userinfo",
			yaml:      "websocket:\n  origin: \"http://user:pass@example.com\"\n",
			errSubstr: "userinfo is not allowed",
		},
		{
			name:      "origin with query",
			yaml:      "websocket:\n  origin: \"http://example.com?query=1\"\n",
			errSubstr: "path, query, and fragment are not allowed",
		},
		{
			name:      "origin with fragment",
			yaml:      "websocket:\n  origin: \"http://example.com#hash\"\n",
			errSubstr: "path, query, and fragment are not allowed",
		},
		{
			name:      "origin missing host",
			yaml:      "websocket:\n  origin: \"http:///\"\n",
			errSubstr: "missing host",
		},
		{
			name:      "token with leading space",
			yaml:      "auth:\n  token: \" my-token\"\n",
			errSubstr: "leading or trailing whitespace",
		},
		{
			name:      "token with trailing space",
			yaml:      "auth:\n  token: \"my-token \"\n",
			errSubstr: "leading or trailing whitespace",
		},
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}
}

func TestConfig_InvalidYAMLSyntaxAndTypes(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		errSubstr string
	}{
		{
			name:      "unclosed bracket syntax error",
			yaml:      "server:\n  addr: [unclosed\n",
			errSubstr: "malformed YAML",
		},
		{
			name:      "mapping where scalar expected",
			yaml:      "server:\n  addr:\n    host: 127.0.0.1\n",
			errSubstr: "must be a scalar value",
		},
		{
			name:      "scalar where mapping expected",
			yaml:      "server: \"127.0.0.1:7690\"\n",
			errSubstr: "must be a mapping",
		},
		{
			name:      "null value with tilde",
			yaml:      "server:\n  addr: ~\n",
			errSubstr: "null values are not allowed",
		},
	}

	dummyLookup := func(k string) (string, bool) { return "", false }
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(cfgPath, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Load([]string{"--config", cfgPath}, tmpDir, dummyLookup, "v1.0")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Errorf("expected error containing %q, got: %v", tc.errSubstr, err)
			}
		})
	}
}
