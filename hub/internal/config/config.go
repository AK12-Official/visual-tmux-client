package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration for strict unit-bearing YAML strings.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// String implements fmt.Stringer for duration formatting.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// MarshalYAML serializes Duration as a unit-bearing duration string.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

const yamlTagString = "!!str"

// UnmarshalYAML strictly unmarshals a unit-bearing string into Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode || value.Tag != yamlTagString {
		return fmt.Errorf("duration must be a unit-bearing string, got tag %s", value.Tag)
	}
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	*d = Duration(dur)
	return nil
}

// Config defines the complete strongly typed runtime configuration.
type Config struct {
	Version   int             `yaml:"version"`
	Server    ServerConfig    `yaml:"server"`
	Auth      AuthConfig      `yaml:"auth"`
	Tmux      TmuxConfig      `yaml:"tmux"`
	WebSocket WebSocketConfig `yaml:"websocket"`
	Terminal  TerminalConfig  `yaml:"terminal"`
	Shutdown  ShutdownConfig  `yaml:"shutdown"`
	Web       WebConfig       `yaml:"web"`
}

// ServerConfig holds HTTP server parameters.
type ServerConfig struct {
	Addr                string   `yaml:"addr"`
	ReadHeaderTimeout   Duration `yaml:"read_header_timeout"`
	IdleTimeout         Duration `yaml:"idle_timeout"`
	MaxRequestBodyBytes int64    `yaml:"max_request_body_bytes"`
}

// AuthConfig holds credentials and ticket lifetime parameters.
type AuthConfig struct {
	Token               string   `yaml:"token"`
	TicketTTL           Duration `yaml:"ticket_ttl"`
	TicketSweepInterval Duration `yaml:"ticket_sweep_interval"`
}

// TmuxConfig holds tmux executable settings.
type TmuxConfig struct {
	Path string `yaml:"path"`
}

// WebSocketConfig holds WebSocket limits and write timeouts.
type WebSocketConfig struct {
	Origin               string   `yaml:"origin"`
	MaxInputMessageBytes int64    `yaml:"max_input_message_bytes"`
	ControlWriteTimeout  Duration `yaml:"control_write_timeout"`
	OutputWriteTimeout   Duration `yaml:"output_write_timeout"`
	ExitWriteTimeout     Duration `yaml:"exit_write_timeout"`
}

// TerminalConfig holds dimension limits, buffer sizes, and watermarks.
type TerminalConfig struct {
	MaxDimension             int      `yaml:"max_dimension"`
	StagingBufferBytes       int      `yaml:"staging_buffer_bytes"`
	OutputHighWaterBytes     int      `yaml:"output_high_water_bytes"`
	OutputLowWaterBytes      int      `yaml:"output_low_water_bytes"`
	BackpressurePollInterval Duration `yaml:"backpressure_poll_interval"`
}

// ShutdownConfig holds phase timeouts for graceful shutdown.
type ShutdownConfig struct {
	AttachmentTimeout Duration `yaml:"attachment_timeout"`
	HTTPTimeout       Duration `yaml:"http_timeout"`
}

// WebConfig holds browser runtime parameters projected to the client.
type WebConfig struct {
	SessionPollInterval Duration            `yaml:"session_poll_interval"`
	ActivityDecay       Duration            `yaml:"activity_decay"`
	ActivityThrottle    Duration            `yaml:"activity_throttle"`
	ResizeDebounce      Duration            `yaml:"resize_debounce"`
	Reconnect           ReconnectConfig     `yaml:"reconnect"`
	Terminal            WebTerminalConfig   `yaml:"terminal"`
	Notifications       NotificationsConfig `yaml:"notifications"`
}

// ReconnectConfig holds browser reconnection delay settings.
type ReconnectConfig struct {
	InitialDelay Duration `yaml:"initial_delay"`
	MaxDelay     Duration `yaml:"max_delay"`
}

// WebTerminalConfig holds browser terminal display and font settings.
type WebTerminalConfig struct {
	Scrollback  int `yaml:"scrollback"`
	FontSize    int `yaml:"font_size"`
	MinFontSize int `yaml:"min_font_size"`
	MaxFontSize int `yaml:"max_font_size"`
}

// NotificationsConfig holds toast notification limits and lifetimes.
type NotificationsConfig struct {
	MaxToasts       int      `yaml:"max_toasts"`
	ErrorLifetime   Duration `yaml:"error_lifetime"`
	WarningLifetime Duration `yaml:"warning_lifetime"`
	InfoLifetime    Duration `yaml:"info_lifetime"`
}
