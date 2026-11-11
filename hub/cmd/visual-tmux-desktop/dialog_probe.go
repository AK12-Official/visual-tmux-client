//go:build desktop && (linux || darwin)

package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func (s *desktopWindow) attachDialogProbe() {
	s.controller.Bridge.Register("probe-error", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Message string `json:"message"`
		}
		if desktop.DecodeJSON(r, &body) != nil || len(body.Message) > maxProbeErrorBytes {
			http.Error(w, "invalid probe error", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(os.Stderr, "spike: frontend probe failed:", desktop.RedactDiagnostic(body.Message))
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
	s.controller.Bridge.Register("probe-dialog", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Approved bool   `json:"approved"`
			Text     string `json:"text"`
		}
		if desktop.DecodeJSON(r, &body) != nil || len(body.Text) > maxProbeTextBytes {
			http.Error(w, "invalid dialog probe", http.StatusBadRequest)
			return
		}
		ready := false
		application.InvokeSync(func() {
			ready = scheduleDialogProbe(s.window.Load().NativeWindow(), body.Approved, body.Text)
		})
		if !ready {
			http.Error(w, "dialog probe unavailable", http.StatusServiceUnavailable)
			return
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
	s.controller.Bridge.Register("probe-dialog-result", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OK bool `json:"ok"`
		}
		if desktop.DecodeJSON(r, &body) != nil || !body.OK {
			http.Error(w, "dialog probe failed", http.StatusBadRequest)
			return
		}
		s.dialogsPassed.Store(true)
		fmt.Fprintln(os.Stderr, "spike: native confirmation cancel/accept and Unicode input prompt passed")
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
}

const maxProbeTextBytes = 128
const maxProbeErrorBytes = 1024
