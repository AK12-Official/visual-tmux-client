//go:build desktop && linux

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// GTK disposes the chooser with its owned parent when the application quits.
func cancelOwnedDialogs(_ *application.WebviewWindow) {}
