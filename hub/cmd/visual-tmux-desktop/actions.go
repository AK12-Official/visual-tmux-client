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
	enabled bool, limit int64, probeDestination string,
) {
	controller.Bridge.AttachExternalLinks(native.Browser.OpenURL)
	chooseDestination := func(ctx context.Context, name string) (desktop.Destination, error) {
		selected, err := native.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
			Title: "Save file / 保存文件", ButtonText: "Save / 保存",
		}).
			AttachToWindow(window).SetFilename(name).PromptForSingleSelection()
		if probeDestination != "" {
			if selected != "" && selected != probeDestination {
				return desktop.Destination{}, errors.New("probe chooser selected a destination outside its fixture")
			}
		}
		if err != nil || selected == "" {
			return desktop.Destination{Path: selected}, err
		}
		if _, err := os.Lstat(selected); errors.Is(err, os.ErrNotExist) {
			return desktop.Destination{Path: selected}, nil
		} else if err != nil {
			return desktop.Destination{}, err
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
				return desktop.Destination{Path: selected, OverwriteConfirmed: true}, nil
			}
			return desktop.Destination{}, nil
		case <-ctx.Done():
			return desktop.Destination{}, ctx.Err()
		}
	}
	controller.Bridge.AttachDownload(enabled, limit, chooseDestination)
	controller.Bridge.Register("clipboard", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid clipboard request", http.StatusBadRequest)
			return
		}
		copied := false
		application.InvokeSync(func() { copied = native.Clipboard.SetText(body.Text) })
		if !copied {
			http.Error(w, "clipboard unavailable", http.StatusServiceUnavailable)
			return
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
}
