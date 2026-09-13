package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	minGeneralDuration = 1 * time.Millisecond
	maxGeneralDuration = 24 * time.Hour
	maxTicketTTL       = 5 * time.Minute
	maxActivityDecay   = 5 * time.Second
	maxBrowserDuration = 2147483647 * time.Millisecond // 2^31 - 1 ms

	minPort = 1
	maxPort = 65535

	minMaxRequestBodyBytes = 1
	maxMaxRequestBodyBytes = 8388608 // 8 MiB

	minMaxInputMessageBytes = 1
	maxMaxInputMessageBytes = 67108864 // 64 MiB

	minTerminalDimension = 80
	maxTerminalDimension = 65535

	minStagingBufferBytes = 32768 // 32 KiB, one read chunk
	maxStagingBufferBytes = 67108864

	minOutputHighWaterBytes = 1
	maxOutputHighWaterBytes = 67108864

	minScrollback = 0
	maxScrollback = 100000

	minFontSizeBound = 1
	maxFontSizeBound = 256

	minToasts = 2
	maxToasts = 100

	tagYAMLString = "!!str"
	tagYAMLInt    = "!!int"

	keyTerminalSection = "terminal"
)

var allowedRootKeys = map[string]bool{
	"version":          true,
	"server":           true,
	"auth":             true,
	"tmux":             true,
	"websocket":        true,
	keyTerminalSection: true,
	"shutdown":         true,
	"web":              true,
}

var allowedServerKeys = map[string]bool{
	"addr":                   true,
	"read_header_timeout":    true,
	"idle_timeout":           true,
	"max_request_body_bytes": true,
}

var allowedAuthKeys = map[string]bool{
	"token":                 true,
	"ticket_ttl":            true,
	"ticket_sweep_interval": true,
}

var allowedTmuxKeys = map[string]bool{
	"path": true,
}

var allowedWebSocketKeys = map[string]bool{
	"origin":                  true,
	"max_input_message_bytes": true,
	"control_write_timeout":   true,
	"output_write_timeout":    true,
	"exit_write_timeout":      true,
}

var allowedTerminalKeys = map[string]bool{
	"max_dimension":              true,
	"staging_buffer_bytes":       true,
	"output_high_water_bytes":    true,
	"output_low_water_bytes":     true,
	"backpressure_poll_interval": true,
}

var allowedShutdownKeys = map[string]bool{
	"attachment_timeout": true,
	"http_timeout":       true,
}

var allowedWebKeys = map[string]bool{
	"session_poll_interval": true,
	"activity_decay":        true,
	"activity_throttle":     true,
	"resize_debounce":       true,
	"reconnect":             true,
	keyTerminalSection:      true,
	"notifications":         true,
}

var allowedReconnectKeys = map[string]bool{
	"initial_delay": true,
	"max_delay":     true,
}

var allowedWebTerminalKeys = map[string]bool{
	"scrollback":    true,
	"font_size":     true,
	"min_font_size": true,
	"max_font_size": true,
}

var allowedNotificationsKeys = map[string]bool{
	"max_toasts":       true,
	"error_lifetime":   true,
	"warning_lifetime": true,
	"info_lifetime":    true,
}

// ValidateYAMLDocument validates the raw YAML content for structure, nulls, duplicates, and unknown keys.
func ValidateYAMLDocument(data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var docNode yaml.Node
	if err := dec.Decode(&docNode); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("empty YAML document")
		}
		return nil, fmt.Errorf("malformed YAML: %w", err)
	}

	var secondDoc yaml.Node
	if err := dec.Decode(&secondDoc); err == nil {
		return nil, fmt.Errorf("multiple YAML documents are not supported")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("malformed YAML in subsequent document: %w", err)
	}

	if docNode.Kind == yaml.DocumentNode && len(docNode.Content) > 0 {
		root := docNode.Content[0]
		if root.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("YAML root must be a mapping")
		}
		if err := validateMappingNode(root, "", allowedRootKeys); err != nil {
			return nil, err
		}
	}

	return &docNode, nil
}

func validateMappingNode(node *yaml.Node, pathPrefix string, allowedKeys map[string]bool) error {
	seenKeys := make(map[string]int)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		key := keyNode.Value
		fullPath := key
		if pathPrefix != "" {
			fullPath = pathPrefix + "." + key
		}

		if line, exists := seenKeys[key]; exists {
			return fmt.Errorf("duplicate key %q at line %d (previously at line %d)",
				fullPath, keyNode.Line, line)
		}
		seenKeys[key] = keyNode.Line

		if !allowedKeys[key] {
			return fmt.Errorf("unknown configuration key %q at line %d", fullPath, keyNode.Line)
		}

		if valNode.Tag == "!!null" || valNode.Value == "null" || valNode.Value == "~" {
			return fmt.Errorf("null values are not allowed for field %q at line %d", fullPath, valNode.Line)
		}

		if err := validateValueTypesAndChildren(valNode, fullPath); err != nil {
			return err
		}
	}
	return nil
}

func sectionAllowedKeys(section string) map[string]bool {
	switch section {
	case "server":
		return allowedServerKeys
	case "auth":
		return allowedAuthKeys
	case "tmux":
		return allowedTmuxKeys
	case "websocket":
		return allowedWebSocketKeys
	case keyTerminalSection:
		return allowedTerminalKeys
	case "shutdown":
		return allowedShutdownKeys
	case "web":
		return allowedWebKeys
	case "web.reconnect":
		return allowedReconnectKeys
	case "web.terminal":
		return allowedWebTerminalKeys
	case "web.notifications":
		return allowedNotificationsKeys
	default:
		return nil
	}
}

func validateValueTypesAndChildren(valNode *yaml.Node, fullPath string) error {
	if keys := sectionAllowedKeys(fullPath); keys != nil {
		if valNode.Kind != yaml.MappingNode {
			return fmt.Errorf("field %q must be a mapping", fullPath)
		}
		return validateMappingNode(valNode, fullPath, keys)
	}
	return validateScalarType(valNode, fullPath)
}

func isIntegerConfigField(field string) bool {
	switch field {
	case "version", "server.max_request_body_bytes", "websocket.max_input_message_bytes",
		"terminal.max_dimension", "terminal.staging_buffer_bytes",
		"terminal.output_high_water_bytes", "terminal.output_low_water_bytes",
		"web.terminal.scrollback", "web.terminal.font_size",
		"web.terminal.min_font_size", "web.terminal.max_font_size",
		"web.notifications.max_toasts":
		return true
	default:
		return false
	}
}

func isDurationConfigField(field string) bool {
	switch field {
	case "server.read_header_timeout", "server.idle_timeout",
		"auth.ticket_ttl", "auth.ticket_sweep_interval",
		"websocket.control_write_timeout", "websocket.output_write_timeout",
		"websocket.exit_write_timeout", "terminal.backpressure_poll_interval",
		"shutdown.attachment_timeout", "shutdown.http_timeout",
		"web.session_poll_interval", "web.activity_decay",
		"web.activity_throttle", "web.resize_debounce",
		"web.reconnect.initial_delay", "web.reconnect.max_delay",
		"web.notifications.error_lifetime", "web.notifications.warning_lifetime",
		"web.notifications.info_lifetime":
		return true
	default:
		return false
	}
}

func validateScalarType(valNode *yaml.Node, fullPath string) error {
	if valNode.Kind != yaml.ScalarNode {
		return fmt.Errorf("field %q must be a scalar value", fullPath)
	}

	if isIntegerConfigField(fullPath) {
		if valNode.Tag != tagYAMLInt {
			return fmt.Errorf("field %q must be an integer, got tag %s at line %d",
				fullPath, valNode.Tag, valNode.Line)
		}
		return nil
	}

	if isDurationConfigField(fullPath) {
		if valNode.Tag != tagYAMLString {
			return fmt.Errorf("field %q must be a unit-bearing duration string, got tag %s at line %d",
				fullPath, valNode.Tag, valNode.Line)
		}
		return nil
	}

	switch fullPath {
	case "server.addr", "auth.token", "tmux.path", "websocket.origin":
		if valNode.Tag != tagYAMLString {
			return fmt.Errorf("field %q must be a string, got tag %s at line %d", fullPath, valNode.Tag, valNode.Line)
		}
	}
	return nil
}

// ValidateConfig enforces ranges, formats, and cross-field relationships on effective Config.
func ValidateConfig(cfg *Config) error {
	if cfg.Version != 1 {
		return fmt.Errorf("field \"version\": unsupported version %d (only version 1 is supported)", cfg.Version)
	}

	if err := validateServerConfig(&cfg.Server); err != nil {
		return err
	}
	if err := validateAuthConfig(&cfg.Auth); err != nil {
		return err
	}
	if err := validateWebSocketConfig(&cfg.WebSocket); err != nil {
		return err
	}
	if err := validateTerminalConfig(&cfg.Terminal); err != nil {
		return err
	}
	if err := validateShutdownConfig(&cfg.Shutdown); err != nil {
		return err
	}
	return validateWebConfig(&cfg.Web)
}

func validateServerConfig(s *ServerConfig) error {
	_, portStr, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("field \"server.addr\": invalid address %q: %w", s.Addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < minPort || port > maxPort {
		return fmt.Errorf("field \"server.addr\": invalid port %q in %q (must be 1..65535)", portStr, s.Addr)
	}

	if err := validateDuration("server.read_header_timeout",
		s.ReadHeaderTimeout, minGeneralDuration, maxGeneralDuration); err != nil {
		return err
	}
	if err := validateDuration("server.idle_timeout",
		s.IdleTimeout, minGeneralDuration, maxGeneralDuration); err != nil {
		return err
	}

	if s.MaxRequestBodyBytes < minMaxRequestBodyBytes || s.MaxRequestBodyBytes > maxMaxRequestBodyBytes {
		return fmt.Errorf("field \"server.max_request_body_bytes\": %d out of range [%d, %d]",
			s.MaxRequestBodyBytes, minMaxRequestBodyBytes, maxMaxRequestBodyBytes)
	}
	return nil
}

func validateAuthConfig(a *AuthConfig) error {
	if a.Token != "" {
		if strings.TrimSpace(a.Token) != a.Token {
			return fmt.Errorf("field \"auth.token\": token contains leading or trailing whitespace")
		}
		for i := 0; i < len(a.Token); i++ {
			b := a.Token[i]
			if b < 0x21 || b > 0x7e {
				return fmt.Errorf("field \"auth.token\": token contains invalid header character at byte index %d", i)
			}
		}
	}

	if err := validateDuration("auth.ticket_ttl", a.TicketTTL, minGeneralDuration, maxTicketTTL); err != nil {
		return err
	}
	return validateDuration("auth.ticket_sweep_interval",
		a.TicketSweepInterval, minGeneralDuration, maxGeneralDuration)
}

func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("field \"websocket.origin\": invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("field \"websocket.origin\": scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("field \"websocket.origin\": missing host in origin %q", origin)
	}
	if u.User != nil {
		return fmt.Errorf("field \"websocket.origin\": userinfo is not allowed in origin")
	}
	if u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("field \"websocket.origin\": path, query, and fragment are not allowed in origin")
	}
	return nil
}

func validateWebSocketConfig(ws *WebSocketConfig) error {
	if ws.Origin != "" {
		if err := validateOrigin(ws.Origin); err != nil {
			return err
		}
	}

	if ws.MaxInputMessageBytes < minMaxInputMessageBytes || ws.MaxInputMessageBytes > maxMaxInputMessageBytes {
		return fmt.Errorf("field \"websocket.max_input_message_bytes\": %d out of range [%d, %d]",
			ws.MaxInputMessageBytes, minMaxInputMessageBytes, maxMaxInputMessageBytes)
	}

	if err := validateDuration("websocket.control_write_timeout",
		ws.ControlWriteTimeout, minGeneralDuration, maxGeneralDuration); err != nil {
		return err
	}
	if err := validateDuration("websocket.output_write_timeout",
		ws.OutputWriteTimeout, minGeneralDuration, maxGeneralDuration); err != nil {
		return err
	}
	return validateDuration("websocket.exit_write_timeout",
		ws.ExitWriteTimeout, minGeneralDuration, maxGeneralDuration)
}

func validateTerminalConfig(t *TerminalConfig) error {
	if t.MaxDimension < minTerminalDimension || t.MaxDimension > maxTerminalDimension {
		return fmt.Errorf("field \"terminal.max_dimension\": %d out of range [%d, %d]",
			t.MaxDimension, minTerminalDimension, maxTerminalDimension)
	}

	if t.StagingBufferBytes < minStagingBufferBytes || t.StagingBufferBytes > maxStagingBufferBytes {
		return fmt.Errorf("field \"terminal.staging_buffer_bytes\": %d out of range [%d, %d]",
			t.StagingBufferBytes, minStagingBufferBytes, maxStagingBufferBytes)
	}

	if t.OutputHighWaterBytes < minOutputHighWaterBytes || t.OutputHighWaterBytes > maxOutputHighWaterBytes {
		return fmt.Errorf("field \"terminal.output_high_water_bytes\": %d out of range [%d, %d]",
			t.OutputHighWaterBytes, minOutputHighWaterBytes, maxOutputHighWaterBytes)
	}

	if t.OutputLowWaterBytes <= 0 || t.OutputLowWaterBytes >= t.OutputHighWaterBytes {
		return fmt.Errorf("field \"terminal.output_low_water_bytes\": %d must satisfy 0 < low < high (%d)",
			t.OutputLowWaterBytes, t.OutputHighWaterBytes)
	}

	return validateDuration("terminal.backpressure_poll_interval",
		t.BackpressurePollInterval, minGeneralDuration, maxGeneralDuration)
}

func validateShutdownConfig(s *ShutdownConfig) error {
	if err := validateDuration("shutdown.attachment_timeout",
		s.AttachmentTimeout, minGeneralDuration, maxGeneralDuration); err != nil {
		return err
	}
	return validateDuration("shutdown.http_timeout",
		s.HTTPTimeout, minGeneralDuration, maxGeneralDuration)
}

func validateWebTimers(w *WebConfig) error {
	if err := validateBrowserDuration("web.session_poll_interval",
		w.SessionPollInterval, minGeneralDuration, maxBrowserDuration); err != nil {
		return err
	}
	if err := validateBrowserDuration("web.activity_decay",
		w.ActivityDecay, minGeneralDuration, maxActivityDecay); err != nil {
		return err
	}
	if err := validateBrowserDuration("web.activity_throttle",
		w.ActivityThrottle, minGeneralDuration, w.ActivityDecay.Duration()); err != nil {
		return fmt.Errorf("field \"web.activity_throttle\": must satisfy 1ms <= throttle <= activity_decay (%s): %w",
			w.ActivityDecay.Duration(), err)
	}
	if err := validateBrowserDuration("web.resize_debounce",
		w.ResizeDebounce, minGeneralDuration, maxBrowserDuration); err != nil {
		return err
	}
	if err := validateBrowserDuration("web.reconnect.initial_delay",
		w.Reconnect.InitialDelay, minGeneralDuration, w.Reconnect.MaxDelay.Duration()); err != nil {
		return fmt.Errorf("field \"web.reconnect.initial_delay\": must satisfy initial_delay <= max_delay (%s): %w",
			w.Reconnect.MaxDelay.Duration(), err)
	}
	return validateBrowserDuration("web.reconnect.max_delay",
		w.Reconnect.MaxDelay, minGeneralDuration, maxBrowserDuration)
}

func validateWebTerminal(w *WebTerminalConfig) error {
	if w.Scrollback < minScrollback || w.Scrollback > maxScrollback {
		return fmt.Errorf("field \"web.terminal.scrollback\": %d out of range [%d, %d]",
			w.Scrollback, minScrollback, maxScrollback)
	}
	if w.MinFontSize < minFontSizeBound || w.MinFontSize > maxFontSizeBound {
		return fmt.Errorf("field \"web.terminal.min_font_size\": %d out of range [%d, %d]",
			w.MinFontSize, minFontSizeBound, maxFontSizeBound)
	}
	if w.MaxFontSize < w.MinFontSize || w.MaxFontSize > maxFontSizeBound {
		return fmt.Errorf("field \"web.terminal.max_font_size\": %d must satisfy min_font_size (%d) <= max <= %d",
			w.MaxFontSize, w.MinFontSize, maxFontSizeBound)
	}
	if w.FontSize < w.MinFontSize || w.FontSize > w.MaxFontSize {
		return fmt.Errorf("field \"web.terminal.font_size\": %d must be within [%d, %d]",
			w.FontSize, w.MinFontSize, w.MaxFontSize)
	}
	return nil
}

func validateWebNotifications(n *NotificationsConfig) error {
	if n.MaxToasts < minToasts || n.MaxToasts > maxToasts {
		return fmt.Errorf("field \"web.notifications.max_toasts\": %d out of range [%d, %d]",
			n.MaxToasts, minToasts, maxToasts)
	}
	if err := validateBrowserDuration("web.notifications.error_lifetime",
		n.ErrorLifetime, minGeneralDuration, maxBrowserDuration); err != nil {
		return err
	}
	if err := validateBrowserDuration("web.notifications.warning_lifetime",
		n.WarningLifetime, minGeneralDuration, maxBrowserDuration); err != nil {
		return err
	}
	return validateBrowserDuration("web.notifications.info_lifetime",
		n.InfoLifetime, minGeneralDuration, maxBrowserDuration)
}

func validateWebConfig(w *WebConfig) error {
	if err := validateWebTimers(w); err != nil {
		return err
	}
	if err := validateWebTerminal(&w.Terminal); err != nil {
		return err
	}
	return validateWebNotifications(&w.Notifications)
}

func validateDuration(field string, d Duration, min, max time.Duration) error {
	td := d.Duration()
	if td < min || td > max {
		return fmt.Errorf("field %q: duration %s out of range [%s, %s]", field, td, min, max)
	}
	return nil
}

func validateBrowserDuration(field string, d Duration, min, max time.Duration) error {
	td := d.Duration()
	if td < min || td > max {
		return fmt.Errorf("field %q: duration %s out of range [%s, %s]", field, td, min, max)
	}
	if td%time.Millisecond != 0 {
		return fmt.Errorf("field %q: duration %s must be integer milliseconds", field, td)
	}
	return nil
}
