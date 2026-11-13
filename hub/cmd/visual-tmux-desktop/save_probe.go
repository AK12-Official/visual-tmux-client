//go:build desktop && (linux || darwin)

package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const saveProbeBytes = "\x00\xffdesktop\r\n中文\x00end"

func prepareSaveFixture(cfg *config.Config, directory string) error {
	root := filepath.Join(directory, "source")
	if err := os.Mkdir(root, probeDirectoryMode); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "fixture.bin"), []byte(saveProbeBytes), probeFileMode); err != nil {
		return err
	}
	cfg.Files.Enabled = true
	cfg.Files.Roots = []string{root}
	return nil
}

func (s *desktopWindow) probeDestination() string {
	return filepath.Join(s.temporaryDir, "selected.bin")
}

func (s *desktopWindow) attachSaveProbe() {
	s.controller.Bridge.Register("probe-save", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Approved bool `json:"approved"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid save probe", http.StatusBadRequest)
			return
		}
		ready := false
		fmt.Fprintln(os.Stderr, "spike: arming chooser; approve:", body.Approved)
		application.InvokeSync(func() {
			ready = scheduleSaveProbe(s.window.Load().NativeWindow(), body.Approved, s.probeDestination())
		})
		if !ready {
			http.Error(w, "save probe unavailable", http.StatusServiceUnavailable)
			return
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
	s.controller.Bridge.Register("probe-save-result", http.HandlerFunc(s.probeSaveResult))
}

func (s *desktopWindow) probeSaveResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approved bool `json:"approved"`
	}
	if desktop.DecodeJSON(r, &body) != nil {
		http.Error(w, "invalid save result", http.StatusBadRequest)
		return
	}
	if !body.Approved {
		if _, err := os.Stat(s.probeDestination()); !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "cancel created a destination", http.StatusConflict)
			return
		}
		fmt.Fprintln(os.Stderr, "spike: native save chooser cancellation created no file")
	} else {
		data, err := os.ReadFile(s.probeDestination())
		if err != nil || !bytes.Equal(data, []byte(saveProbeBytes)) {
			http.Error(w, "native download bytes differ", http.StatusConflict)
			return
		}
		s.downloadPassed.Store(true)
		fmt.Fprintln(os.Stderr, "spike: native save chooser and authenticated byte-preserving download passed")
	}
	desktop.WriteJSON(w, map[string]bool{"ok": true})
}

const probeDirectoryMode = 0700
const probeFileMode = 0600
