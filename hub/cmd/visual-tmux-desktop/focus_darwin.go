//go:build desktop && darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func applyActivationToken(_ *application.WebviewWindow, _ string) {}
