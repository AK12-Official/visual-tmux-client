package config

import "time"

// Default configuration constants as specified in design.md.
const (
	DefaultVersion = 1

	DefaultServerAddr                = "127.0.0.1:7690"
	DefaultServerReadHeaderTimeout   = 10 * time.Second
	DefaultServerIdleTimeout         = 120 * time.Second
	DefaultServerMaxRequestBodyBytes = 65536 // 64 KiB

	DefaultAuthToken               = ""
	DefaultAuthTicketTTL           = 30 * time.Second
	DefaultAuthTicketSweepInterval = 10 * time.Second

	DefaultTmuxPath = ""

	DefaultWebSocketOrigin               = ""
	DefaultWebSocketMaxInputMessageBytes = 8388608 // 8 MiB
	DefaultWebSocketControlWriteTimeout  = 5 * time.Second
	DefaultWebSocketOutputWriteTimeout   = 10 * time.Second
	DefaultWebSocketExitWriteTimeout     = 3 * time.Second

	DefaultTerminalMaxDimension             = 1000
	DefaultTerminalStagingBufferBytes       = 2097152 // 2 MiB
	DefaultTerminalOutputHighWaterBytes     = 1048576 // 1 MiB
	DefaultTerminalOutputLowWaterBytes      = 131072  // 128 KiB
	DefaultTerminalBackpressurePollInterval = 100 * time.Millisecond

	DefaultShutdownAttachmentTimeout = 3 * time.Second
	DefaultShutdownHTTPTimeout       = 10 * time.Second

	DefaultWebSessionPollInterval          = 5 * time.Second
	DefaultWebActivityDecay                = 2 * time.Second
	DefaultWebActivityThrottle             = 500 * time.Millisecond
	DefaultWebResizeDebounce               = 100 * time.Millisecond
	DefaultWebReconnectInitialDelay        = 500 * time.Millisecond
	DefaultWebReconnectMaxDelay            = 3 * time.Second
	DefaultWebTerminalScrollback           = 5000
	DefaultWebTerminalFontSize             = 13
	DefaultWebTerminalMinFontSize          = 8
	DefaultWebTerminalMaxFontSize          = 24
	DefaultWebNotificationsMaxToasts       = 5
	DefaultWebNotificationsErrorLifetime   = 8 * time.Second
	DefaultWebNotificationsWarningLifetime = 5 * time.Second
	DefaultWebNotificationsInfoLifetime    = 3 * time.Second
)

// DefaultConfig returns a complete Config populated with all design defaults.
func DefaultConfig() Config {
	return Config{
		Version: DefaultVersion,
		Server: ServerConfig{
			Addr:                DefaultServerAddr,
			ReadHeaderTimeout:   Duration(DefaultServerReadHeaderTimeout),
			IdleTimeout:         Duration(DefaultServerIdleTimeout),
			MaxRequestBodyBytes: DefaultServerMaxRequestBodyBytes,
		},
		Auth: AuthConfig{
			Token:               DefaultAuthToken,
			TicketTTL:           Duration(DefaultAuthTicketTTL),
			TicketSweepInterval: Duration(DefaultAuthTicketSweepInterval),
		},
		Tmux: TmuxConfig{
			Path: DefaultTmuxPath,
		},
		WebSocket: WebSocketConfig{
			Origin:               DefaultWebSocketOrigin,
			MaxInputMessageBytes: DefaultWebSocketMaxInputMessageBytes,
			ControlWriteTimeout:  Duration(DefaultWebSocketControlWriteTimeout),
			OutputWriteTimeout:   Duration(DefaultWebSocketOutputWriteTimeout),
			ExitWriteTimeout:     Duration(DefaultWebSocketExitWriteTimeout),
		},
		Terminal: TerminalConfig{
			MaxDimension:             DefaultTerminalMaxDimension,
			StagingBufferBytes:       DefaultTerminalStagingBufferBytes,
			OutputHighWaterBytes:     DefaultTerminalOutputHighWaterBytes,
			OutputLowWaterBytes:      DefaultTerminalOutputLowWaterBytes,
			BackpressurePollInterval: Duration(DefaultTerminalBackpressurePollInterval),
		},
		Shutdown: ShutdownConfig{
			AttachmentTimeout: Duration(DefaultShutdownAttachmentTimeout),
			HTTPTimeout:       Duration(DefaultShutdownHTTPTimeout),
		},
		Web: WebConfig{
			SessionPollInterval: Duration(DefaultWebSessionPollInterval),
			ActivityDecay:       Duration(DefaultWebActivityDecay),
			ActivityThrottle:    Duration(DefaultWebActivityThrottle),
			ResizeDebounce:      Duration(DefaultWebResizeDebounce),
			Reconnect: ReconnectConfig{
				InitialDelay: Duration(DefaultWebReconnectInitialDelay),
				MaxDelay:     Duration(DefaultWebReconnectMaxDelay),
			},
			Terminal: WebTerminalConfig{
				Scrollback:  DefaultWebTerminalScrollback,
				FontSize:    DefaultWebTerminalFontSize,
				MinFontSize: DefaultWebTerminalMinFontSize,
				MaxFontSize: DefaultWebTerminalMaxFontSize,
			},
			Notifications: NotificationsConfig{
				MaxToasts:       DefaultWebNotificationsMaxToasts,
				ErrorLifetime:   Duration(DefaultWebNotificationsErrorLifetime),
				WarningLifetime: Duration(DefaultWebNotificationsWarningLifetime),
				InfoLifetime:    Duration(DefaultWebNotificationsInfoLifetime),
			},
		},
	}
}
