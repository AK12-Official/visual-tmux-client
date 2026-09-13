package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainVersionAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Version flag
	err := run([]string{"--version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error on --version, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "dev") {
		t.Errorf("expected version output 'dev', got: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	// Help flag
	err = run([]string{"--help"}, &stdout, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected usage in stderr, got: %q", stderr.String())
	}
}

func TestMainInvalidConfigFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--config", "nonexistent-config-file.yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on nonexistent config file")
	}
}

func TestMainInvalidAddrFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--addr", "invalid-no-port"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid --addr")
	}
	if !strings.Contains(err.Error(), "invalid address") {
		t.Errorf("expected 'invalid address' error, got: %v", err)
	}
}

func TestMainUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--bogus-flag"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on unknown flag")
	}
}

func TestMainInvalidYAMLConfig(t *testing.T) {
	tmpDir := t.TempDir()
	badYAML := filepath.Join(tmpDir, "bad.yaml")
	if err := os.WriteFile(badYAML, []byte("unknown_key: 123\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := run([]string{"--config", badYAML}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on invalid YAML config")
	}
	if !strings.Contains(err.Error(), "unknown configuration key") {
		t.Errorf("expected unknown configuration key error, got: %v", err)
	}
}
