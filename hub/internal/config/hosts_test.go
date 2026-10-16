package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHostPathsResolveAgainstConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	dir := t.TempDir()
	err := applyYAML(&cfg, &Provenance{}, []byte("hosts:\n  state_dir: state\n  master_key_file: keys/master\n"), dir)
	if err != nil {
		t.Fatal(err)
	}
	expectedState := filepath.Join(dir, "state")
	expectedKey := filepath.Join(dir, "keys/master")
	if cfg.Hosts.StateDir != expectedState || cfg.Hosts.MasterKeyFile != expectedKey {
		t.Fatalf("host paths: %+v", cfg.Hosts)
	}
}

func TestRemoteBoundsRejectExplicitZero(t *testing.T) {
	fields := []string{
		"dial_timeout", "handshake_timeout", "operation_timeout",
		"keepalive_interval", "max_backoff", "max_connections",
	}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			cfg := DefaultConfig()
			value := "0s"
			if field == "max_connections" {
				value = "0"
			}
			err := applyYAML(&cfg, &Provenance{}, []byte("hosts:\n  "+field+": "+value+"\n"), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateConfig(&cfg); err == nil || !strings.Contains(err.Error(), "hosts."+field) {
				t.Fatalf("field-specific validation: %v", err)
			}
		})
	}
}
