package http

import "github.com/AK12-Official/visual-tmux-client/hub/internal/config"

// PublicClientConfig defines the unauthenticated configuration envelope returned to browsers.
type PublicClientConfig struct {
	Version int       `json:"version"`
	Web     PublicWeb `json:"web"`
}

// PublicWeb defines web UI parameters with durations formatted as integer milliseconds.
type PublicWeb struct {
	SessionPollInterval int64                 `json:"session_poll_interval"`
	ActivityDecay       int64                 `json:"activity_decay"`
	ActivityThrottle    int64                 `json:"activity_throttle"`
	ResizeDebounce      int64                 `json:"resize_debounce"`
	Reconnect           PublicReconnectConfig `json:"reconnect"`
	Terminal            PublicTerminalConfig  `json:"terminal"`
	Notifications       PublicNotifications   `json:"notifications"`
}

// PublicReconnectConfig holds reconnection delay settings in milliseconds.
type PublicReconnectConfig struct {
	InitialDelay int64 `json:"initial_delay"`
	MaxDelay     int64 `json:"max_delay"`
}

// PublicTerminalConfig holds terminal display and font settings.
type PublicTerminalConfig struct {
	Scrollback  int `json:"scrollback"`
	FontSize    int `json:"font_size"`
	MinFontSize int `json:"min_font_size"`
	MaxFontSize int `json:"max_font_size"`
}

// PublicNotifications holds notification stack limit and lifetimes in milliseconds.
type PublicNotifications struct {
	MaxToasts       int   `json:"max_toasts"`
	ErrorLifetime   int64 `json:"error_lifetime"`
	WarningLifetime int64 `json:"warning_lifetime"`
	InfoLifetime    int64 `json:"info_lifetime"`
}

// NewPublicClientConfig maps an internal WebConfig into the public browser projection.
func NewPublicClientConfig(web config.WebConfig) PublicClientConfig {
	return PublicClientConfig{
		Version: 1,
		Web: PublicWeb{
			SessionPollInterval: web.SessionPollInterval.Duration().Milliseconds(),
			ActivityDecay:       web.ActivityDecay.Duration().Milliseconds(),
			ActivityThrottle:    web.ActivityThrottle.Duration().Milliseconds(),
			ResizeDebounce:      web.ResizeDebounce.Duration().Milliseconds(),
			Reconnect: PublicReconnectConfig{
				InitialDelay: web.Reconnect.InitialDelay.Duration().Milliseconds(),
				MaxDelay:     web.Reconnect.MaxDelay.Duration().Milliseconds(),
			},
			Terminal: PublicTerminalConfig{
				Scrollback:  web.Terminal.Scrollback,
				FontSize:    web.Terminal.FontSize,
				MinFontSize: web.Terminal.MinFontSize,
				MaxFontSize: web.Terminal.MaxFontSize,
			},
			Notifications: PublicNotifications{
				MaxToasts:       web.Notifications.MaxToasts,
				ErrorLifetime:   web.Notifications.ErrorLifetime.Duration().Milliseconds(),
				WarningLifetime: web.Notifications.WarningLifetime.Duration().Milliseconds(),
				InfoLifetime:    web.Notifications.InfoLifetime.Duration().Milliseconds(),
			},
		},
	}
}

type apiError struct {
	Error string `json:"error"`
}

type createSessionRequest struct {
	Name string `json:"name"`
}

type renameSessionRequest struct {
	Name string `json:"name"`
}

type wsTicketRequest struct {
	HostID  string `json:"hostId"`
	Session string `json:"session"`
}
