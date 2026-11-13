package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop/spike"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

func TestDesktopDownloadRetainsRealSourceAuthorization(t *testing.T) {
	root := t.TempDir()
	const contents = "\x00\xff中文\n"
	valid := filepath.Join(root, "valid")
	large := filepath.Join(root, "large")
	outside := filepath.Join(t.TempDir(), "outside")
	for name, data := range map[string]string{valid: contents, large: "01234567890123456789", outside: "private"} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.DefaultConfig()
	cfg.Hosts.StateDir = t.TempDir()
	cfg.Files.Enabled, cfg.Files.Roots, cfg.Files.MaxFileSize = true, []string{root}, 16
	c, err := Start(context.Background(), &cfg, config.Provenance{}, web.FS(), app.Options{
		Backend: spike.Backend{}, ProcessFact: spike.Processes{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	for _, tc := range []struct {
		name, path               string
		revision                 uint64
		failDestination, success bool
	}{
		{name: "valid", path: valid, revision: 1, success: true},
		{name: "outside root", path: outside, revision: 1},
		{name: "obsolete revision", path: valid, revision: 2},
		{name: "oversized", path: large, revision: 1},
		{name: "destination failure", path: valid, revision: 1, failDestination: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "existing")
			if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			choose := func(context.Context, string) (Destination, error) {
				selected := destination
				if tc.failDestination {
					selected = filepath.Join(destination, "not-a-directory")
				}
				return Destination{Path: selected, OverwriteConfirmed: true}, nil
			}
			_, err := c.Bridge.saveDownload(context.Background(), DownloadRequest{
				HostID: "local", Path: tc.path, ConnectionRevision: tc.revision,
			}, cfg.Files.MaxFileSize, choose)
			if (err == nil) != tc.success {
				t.Fatalf("download success=%v, error=%v", tc.success, err)
			}
			data, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			expected := "keep"
			if tc.success {
				expected = contents
			}
			if string(data) != expected {
				t.Fatal("source bytes or preserved destination changed")
			}
		})
	}
}
