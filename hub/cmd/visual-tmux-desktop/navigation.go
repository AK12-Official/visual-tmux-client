//go:build desktop && (linux || darwin)

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func (s *desktopWindow) attachNavigation(window *application.WebviewWindow, launchURL string) error {
	address, err := url.Parse(launchURL)
	if err != nil {
		return err
	}
	origin := address.Scheme + "://" + address.Host
	var once sync.Once
	window.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
		once.Do(func() {
			application.InvokeSync(func() {
				s.navigationReady.Store(installNavigationGuard(window.NativeWindow(), origin))
			})
			if !s.navigationReady.Load() {
				fmt.Fprintln(os.Stderr, "desktop navigation guard unavailable; closing window")
				s.lifecycle.Stop()
			}
		})
	})
	return nil
}

func (s *desktopWindow) probeNavigation(ctx context.Context) bool {
	deadline, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
	defer cancel()
	ticker := time.NewTicker(probeCloseDelay)
	defer ticker.Stop()
	for !s.navigationReady.Load() {
		select {
		case <-deadline.Done():
			return false
		case <-ticker.C:
		}
	}
	window := s.window.Load()
	before := 0
	application.InvokeSync(func() { before = blockedNavigationCount(window.NativeWindow()) })
	window.SetURL("https://vtc-blocked.invalid/")
	for {
		count := 0
		application.InvokeSync(func() { count = blockedNavigationCount(window.NativeWindow()) })
		if count > before {
			fmt.Fprintln(os.Stderr, "spike: native off-origin navigation blocked")
			return true
		}
		select {
		case <-deadline.Done():
			return false
		case <-ticker.C:
		}
	}
}

const readinessProbeTimeout = 3 * time.Second
