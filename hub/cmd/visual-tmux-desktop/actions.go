//go:build desktop && (linux || darwin)

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func attachDownload(
	controller *desktop.Controller, native *application.App, window *application.WebviewWindow,
	enabled bool, limit int64,
) {
	controller.Bridge.AttachDownload(enabled, limit, func(ctx context.Context, name string) (string, error) {
		selected, err := native.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
			Title: "Save file / 保存文件",
		}).
			AttachToWindow(window).SetFilename(name).PromptForSingleSelection()
		if err != nil || selected == "" {
			return selected, err
		}
		if _, err := os.Lstat(selected); errors.Is(err, os.ErrNotExist) {
			return selected, nil
		} else if err != nil {
			return "", err
		}
		// Do not depend on the platform chooser's overwrite prompt semantics.
		approved := make(chan bool, 1)
		dialog := native.Dialog.Question().AttachToWindow(window).SetTitle("Replace file? / 替换文件？").
			SetMessage("Replace / 替换: " + filepath.Base(selected))
		dialog.AddButton("Cancel / 取消").SetAsCancel().SetAsDefault().OnClick(func() { approved <- false })
		dialog.AddButton("Replace / 替换").OnClick(func() { approved <- true })
		dialog.Show()
		select {
		case yes := <-approved:
			if yes {
				return selected, nil
			}
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	controller.Bridge.Register("clipboard", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid clipboard request", http.StatusBadRequest)
			return
		}
		if !native.Clipboard.SetText(body.Text) {
			http.Error(w, "clipboard unavailable", http.StatusServiceUnavailable)
			return
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
}
