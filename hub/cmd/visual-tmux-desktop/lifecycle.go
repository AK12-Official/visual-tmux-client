//go:build desktop && (linux || darwin)

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func (s *desktopWindow) attachLifecycle(window *application.WebviewWindow) {
	requests := make(chan struct{}, 1)
	s.lifecycle = desktop.NewLifecycle(s.close, func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "desktop shutdown:", err)
		}
		s.native.Quit()
	}, func(id uint64) {
		if !s.frontendReady.Load() {
			s.confirmUnavailableWorkspace(window, id)
			return
		}
		select {
		case requests <- struct{}{}:
		default:
		}
	})
	s.attachFullscreen(window, requests)
	s.attachQuitPoll(requests, window)
	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if s.lifecycle.State() == desktop.Stopped {
			return
		}
		event.Cancel()
		s.lifecycle.RequestQuit()
	})
	s.controller.Bridge.Register("workspace-ready", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid ready request", http.StatusBadRequest)
			return
		}
		s.frontendReady.Store(true)
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
	s.controller.Bridge.Register("quit-response", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID       uint64 `json:"id"`
			Approved bool   `json:"approved"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid quit response", http.StatusBadRequest)
			return
		}
		if !s.lifecycle.Respond(body.ID, body.Approved) {
			http.Error(w, "quit request expired; close the window to retry", http.StatusConflict)
			return
		}
		if s.probe && body.Approved {
			fmt.Fprintln(os.Stderr, "spike: frontend approved quit after preference flush")
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
	s.attachMenu(window)
	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		defer signal.Stop(shutdownSignals)
		select {
		case <-shutdownSignals:
			s.lifecycle.Stop()
		case <-s.lifecycle.Done():
		case <-s.native.Context().Done():
		}
	}()
}

func (s *desktopWindow) attachMenu(window *application.WebviewWindow) {
	menu := application.NewMenu()
	appMenu := menu.AddSubmenu("Application / 应用")
	appMenu.Add("Reload workspace / 重新加载").OnClick(func(*application.Context) {
		if s.lifecycle.State() != desktop.Running {
			return
		}
		dialog := s.native.Dialog.Question().AttachToWindow(window).
			SetTitle("Reload workspace? / 重新加载工作区？").
			SetMessage("Unsaved edits will be lost. / 未保存的编辑将丢失。")
		dialog.AddButton("Cancel / 取消").SetAsCancel().SetAsDefault()
		dialog.AddButton("Reload / 重新加载").OnClick(func() {
			launchURL, err := s.freshLaunchURL()
			if err != nil {
				fmt.Fprintln(os.Stderr, "desktop reload unavailable")
				return
			}
			window.SetURL(launchURL)
		})
		dialog.Show()
	})
	appMenu.Add("Quit / 退出").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) {
		s.lifecycle.RequestQuit()
	})
	// Cocoa's Edit roles supply Cmd+C/Cmd+V to the WebView. Linux gets no
	// Ctrl+C menu accelerator, so terminal interrupts keep reaching xterm.
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.EditMenu)
	}
	s.native.Menu.Set(menu)
}

func (s *desktopWindow) attachQuitPoll(requests <-chan struct{}, window *application.WebviewWindow) {
	s.controller.Bridge.Register("quit-poll", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid poll request", http.StatusBadRequest)
			return
		}
		timer := time.NewTimer(probeTimeout)
		defer timer.Stop()
		var id uint64
		select {
		case <-requests:
			id = s.lifecycle.PendingRequest()
		case <-timer.C:
		case <-r.Context().Done():
			return
		case <-s.lifecycle.Stopping():
		case <-s.controller.Stopping():
		}
		desktop.WriteJSON(w, struct {
			ID         uint64 `json:"id"`
			Fullscreen bool   `json:"fullscreen"`
		}{id, window.IsFullscreen()})
	}))
}

func (s *desktopWindow) confirmUnavailableWorkspace(window *application.WebviewWindow, id uint64) {
	dialog := s.native.Dialog.Question().AttachToWindow(window).
		SetTitle("Close application? / 关闭应用？").
		SetMessage("Workspace confirmation is unavailable. Unsaved data may be lost. / 工作区暂不可用，未保存的数据可能丢失。")
	dialog.AddButton("Cancel / 取消").SetAsCancel().SetAsDefault().OnClick(func() {
		s.lifecycle.Respond(id, false)
	})
	dialog.AddButton("Close / 关闭").OnClick(func() { s.lifecycle.Respond(id, true) })
	dialog.Show()
}

func (s *desktopWindow) freshLaunchURL() (string, error) {
	address, err := s.controller.Bridge.LaunchURL()
	if err == nil {
		s.frontendReady.Store(false)
	}
	return address, err
}

func (s *desktopWindow) attachFullscreen(window *application.WebviewWindow, requests chan<- struct{}) {
	notifyWindow := func(*application.WindowEvent) {
		select {
		case requests <- struct{}{}:
		default:
		}
	}
	window.OnWindowEvent(events.Common.WindowFullscreen, notifyWindow)
	window.OnWindowEvent(events.Common.WindowUnFullscreen, notifyWindow)
	s.controller.Bridge.Register("fullscreen", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if desktop.DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid fullscreen request", http.StatusBadRequest)
			return
		}
		if body.Enabled {
			window.Fullscreen()
		} else {
			window.UnFullscreen()
		}
		desktop.WriteJSON(w, map[string]bool{"ok": true})
	}))
}
