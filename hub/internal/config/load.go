package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrVersion indicates that --version was requested and the caller should display version and exit.
var ErrVersion = errors.New("version requested")

// DefaultImplicitConfigFile is the default file looked for in current working directory if --config is omitted.
const DefaultImplicitConfigFile = "visual-tmux-client.yaml"

type rawConfig struct {
	Version   *int                `yaml:"version"`
	Server    *rawServerConfig    `yaml:"server"`
	Auth      *rawAuthConfig      `yaml:"auth"`
	Tmux      *rawTmuxConfig      `yaml:"tmux"`
	WebSocket *rawWebSocketConfig `yaml:"websocket"`
	Terminal  *rawTerminalConfig  `yaml:"terminal"`
	Shutdown  *rawShutdownConfig  `yaml:"shutdown"`
	Web       *rawWebConfig       `yaml:"web"`
}

type rawServerConfig struct {
	Addr                *string   `yaml:"addr"`
	ReadHeaderTimeout   *Duration `yaml:"read_header_timeout"`
	IdleTimeout         *Duration `yaml:"idle_timeout"`
	MaxRequestBodyBytes *int64    `yaml:"max_request_body_bytes"`
}

type rawAuthConfig struct {
	Token               *string   `yaml:"token"`
	TicketTTL           *Duration `yaml:"ticket_ttl"`
	TicketSweepInterval *Duration `yaml:"ticket_sweep_interval"`
}

type rawTmuxConfig struct {
	Path *string `yaml:"path"`
}

type rawWebSocketConfig struct {
	Origin               *string   `yaml:"origin"`
	MaxInputMessageBytes *int64    `yaml:"max_input_message_bytes"`
	ControlWriteTimeout  *Duration `yaml:"control_write_timeout"`
	OutputWriteTimeout   *Duration `yaml:"output_write_timeout"`
	ExitWriteTimeout     *Duration `yaml:"exit_write_timeout"`
}

type rawTerminalConfig struct {
	MaxDimension             *int      `yaml:"max_dimension"`
	StagingBufferBytes       *int      `yaml:"staging_buffer_bytes"`
	OutputHighWaterBytes     *int      `yaml:"output_high_water_bytes"`
	OutputLowWaterBytes      *int      `yaml:"output_low_water_bytes"`
	BackpressurePollInterval *Duration `yaml:"backpressure_poll_interval"`
}

type rawShutdownConfig struct {
	AttachmentTimeout *Duration `yaml:"attachment_timeout"`
	HTTPTimeout       *Duration `yaml:"http_timeout"`
}

type rawWebConfig struct {
	SessionPollInterval *Duration               `yaml:"session_poll_interval"`
	ActivityDecay       *Duration               `yaml:"activity_decay"`
	ActivityThrottle    *Duration               `yaml:"activity_throttle"`
	ResizeDebounce      *Duration               `yaml:"resize_debounce"`
	Reconnect           *rawReconnectConfig     `yaml:"reconnect"`
	Terminal            *rawWebTerminalConfig   `yaml:"terminal"`
	Notifications       *rawNotificationsConfig `yaml:"notifications"`
}

type rawReconnectConfig struct {
	InitialDelay *Duration `yaml:"initial_delay"`
	MaxDelay     *Duration `yaml:"max_delay"`
}

type rawWebTerminalConfig struct {
	Scrollback  *int `yaml:"scrollback"`
	FontSize    *int `yaml:"font_size"`
	MinFontSize *int `yaml:"min_font_size"`
	MaxFontSize *int `yaml:"max_font_size"`
}

type rawNotificationsConfig struct {
	MaxToasts       *int      `yaml:"max_toasts"`
	ErrorLifetime   *Duration `yaml:"error_lifetime"`
	WarningLifetime *Duration `yaml:"warning_lifetime"`
	InfoLifetime    *Duration `yaml:"info_lifetime"`
}

// EnvLookup is an injectable environment lookup function (e.g. os.LookupEnv).
type EnvLookup func(key string) (string, bool)

type parsedFlags struct {
	explicitAddr   bool
	explicitConfig bool
	configPath     string
	addr           string
}

func parseCommandLineFlags(args []string) (*parsedFlags, error) {
	fs := flag.NewFlagSet("visual-tmux-client", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	showHelp := fs.Bool("help", false, "show help")
	showHelpShort := fs.Bool("h", false, "show help")
	showVersion := fs.Bool("version", false, "print version and exit")
	configPath := fs.String("config", "", "path to YAML configuration file")
	addr := fs.String("addr", "", "listen address (host:port)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, flag.ErrHelp
		}
		return nil, fmt.Errorf("error parsing arguments: %w", err)
	}

	if *showHelp || *showHelpShort {
		return nil, flag.ErrHelp
	}
	if *showVersion {
		return nil, ErrVersion
	}

	pf := &parsedFlags{
		configPath: *configPath,
		addr:       *addr,
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			pf.explicitAddr = true
		}
		if f.Name == "config" {
			pf.explicitConfig = true
		}
	})
	return pf, nil
}

func readConfigFile(workDir string, pf *parsedFlags, lookupEnv EnvLookup) (string, []byte, bool, error) {
	var p string
	var explicit bool
	if pf.explicitConfig {
		p = pf.configPath
		explicit = true
	} else if envPath, ok := lookupEnv("VISUAL_TMUX_CLIENT_CONFIG"); ok && envPath != "" {
		p = envPath
		explicit = true
	}

	if explicit {
		rawPath := p
		if !filepath.IsAbs(p) {
			p = filepath.Join(workDir, p)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", nil, true, fmt.Errorf("failed to read configuration file %q: %w", rawPath, err)
		}
		return p, data, true, nil
	}

	implicitPath := filepath.Join(workDir, DefaultImplicitConfigFile)
	data, err := os.ReadFile(implicitPath)
	if err == nil {
		return implicitPath, data, false, nil
	}
	if !os.IsNotExist(err) {
		return "", nil, false, fmt.Errorf("failed to read implicit configuration file %q: %w",
			DefaultImplicitConfigFile, err)
	}
	return "", nil, false, nil
}

// Load parses command-line arguments, checks for version/help, loads configuration from YAML,
// applies environment variable overrides and explicit CLI flags, and validates the result.
func Load(args []string, workDir string, lookupEnv EnvLookup, version string) (*Config, *Provenance, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	pf, err := parseCommandLineFlags(args)
	if err != nil {
		return nil, nil, err
	}

	yamlPath, yamlData, explicitConfig, err := readConfigFile(workDir, pf, lookupEnv)
	if err != nil {
		return nil, nil, err
	}

	cfg := DefaultConfig()
	prov := Provenance{
		AddrSource:     SourceDefault,
		TokenSource:    SourceDefault,
		OriginSource:   SourceDefault,
		TmuxPathSource: SourceDefault,
		ExplicitConfig: explicitConfig,
	}

	if yamlData != nil {
		prov.ConfigFile = yamlPath
		if err := applyYAML(&cfg, &prov, yamlData, filepath.Dir(yamlPath)); err != nil {
			return nil, nil, fmt.Errorf("invalid configuration in %s: %w", yamlPath, err)
		}
	}

	applyEnv(&cfg, &prov, lookupEnv, workDir)

	if pf.explicitAddr {
		cfg.Server.Addr = pf.addr
		prov.AddrSource = SourceCLI
	}

	if err := ValidateConfig(&cfg); err != nil {
		return nil, nil, err
	}

	if cfg.Auth.Token == "" {
		prov.TokenSource = SourceGenerated
	}

	return &cfg, &prov, nil
}

func applyYAML(cfg *Config, prov *Provenance, data []byte, configDir string) error {
	docNode, err := ValidateYAMLDocument(data)
	if err != nil {
		return err
	}

	var raw rawConfig
	if err := docNode.Decode(&raw); err != nil {
		return fmt.Errorf("failed to decode YAML: %w", err)
	}

	if raw.Version != nil {
		cfg.Version = *raw.Version
	} else {
		cfg.Version = 1
	}

	applyServerYAML(cfg, prov, raw.Server)
	applyAuthYAML(cfg, prov, raw.Auth)
	applyTmuxYAML(cfg, prov, raw.Tmux, configDir)
	applyWebSocketYAML(cfg, prov, raw.WebSocket)
	applyTerminalYAML(cfg, raw.Terminal)
	applyShutdownYAML(cfg, raw.Shutdown)
	applyWebYAML(cfg, raw.Web)
	return nil
}

func applyServerYAML(cfg *Config, prov *Provenance, s *rawServerConfig) {
	if s == nil {
		return
	}
	if s.Addr != nil {
		cfg.Server.Addr = *s.Addr
		prov.AddrSource = SourceYAML
	}
	if s.ReadHeaderTimeout != nil {
		cfg.Server.ReadHeaderTimeout = *s.ReadHeaderTimeout
	}
	if s.IdleTimeout != nil {
		cfg.Server.IdleTimeout = *s.IdleTimeout
	}
	if s.MaxRequestBodyBytes != nil {
		cfg.Server.MaxRequestBodyBytes = *s.MaxRequestBodyBytes
	}
}

func applyAuthYAML(cfg *Config, prov *Provenance, a *rawAuthConfig) {
	if a == nil {
		return
	}
	if a.Token != nil {
		cfg.Auth.Token = *a.Token
		prov.TokenSource = SourceYAML
	}
	if a.TicketTTL != nil {
		cfg.Auth.TicketTTL = *a.TicketTTL
	}
	if a.TicketSweepInterval != nil {
		cfg.Auth.TicketSweepInterval = *a.TicketSweepInterval
	}
}

func applyTmuxYAML(cfg *Config, prov *Provenance, t *rawTmuxConfig, configDir string) {
	if t == nil || t.Path == nil {
		return
	}
	p := *t.Path
	if p != "" && !filepath.IsAbs(p) {
		p = filepath.Clean(filepath.Join(configDir, p))
	}
	cfg.Tmux.Path = p
	prov.TmuxPathSource = SourceYAML
}

func applyWebSocketYAML(cfg *Config, prov *Provenance, ws *rawWebSocketConfig) {
	if ws == nil {
		return
	}
	if ws.Origin != nil {
		cfg.WebSocket.Origin = *ws.Origin
		prov.OriginSource = SourceYAML
	}
	if ws.MaxInputMessageBytes != nil {
		cfg.WebSocket.MaxInputMessageBytes = *ws.MaxInputMessageBytes
	}
	if ws.ControlWriteTimeout != nil {
		cfg.WebSocket.ControlWriteTimeout = *ws.ControlWriteTimeout
	}
	if ws.OutputWriteTimeout != nil {
		cfg.WebSocket.OutputWriteTimeout = *ws.OutputWriteTimeout
	}
	if ws.ExitWriteTimeout != nil {
		cfg.WebSocket.ExitWriteTimeout = *ws.ExitWriteTimeout
	}
}

func applyTerminalYAML(cfg *Config, term *rawTerminalConfig) {
	if term == nil {
		return
	}
	if term.MaxDimension != nil {
		cfg.Terminal.MaxDimension = *term.MaxDimension
	}
	if term.StagingBufferBytes != nil {
		cfg.Terminal.StagingBufferBytes = *term.StagingBufferBytes
	}
	if term.OutputHighWaterBytes != nil {
		cfg.Terminal.OutputHighWaterBytes = *term.OutputHighWaterBytes
	}
	if term.OutputLowWaterBytes != nil {
		cfg.Terminal.OutputLowWaterBytes = *term.OutputLowWaterBytes
	}
	if term.BackpressurePollInterval != nil {
		cfg.Terminal.BackpressurePollInterval = *term.BackpressurePollInterval
	}
}

func applyShutdownYAML(cfg *Config, s *rawShutdownConfig) {
	if s == nil {
		return
	}
	if s.AttachmentTimeout != nil {
		cfg.Shutdown.AttachmentTimeout = *s.AttachmentTimeout
	}
	if s.HTTPTimeout != nil {
		cfg.Shutdown.HTTPTimeout = *s.HTTPTimeout
	}
}

func applyWebYAML(cfg *Config, w *rawWebConfig) {
	if w == nil {
		return
	}
	if w.SessionPollInterval != nil {
		cfg.Web.SessionPollInterval = *w.SessionPollInterval
	}
	if w.ActivityDecay != nil {
		cfg.Web.ActivityDecay = *w.ActivityDecay
	}
	if w.ActivityThrottle != nil {
		cfg.Web.ActivityThrottle = *w.ActivityThrottle
	}
	if w.ResizeDebounce != nil {
		cfg.Web.ResizeDebounce = *w.ResizeDebounce
	}
	applyWebReconnectYAML(cfg, w.Reconnect)
	applyWebTerminalYAML(cfg, w.Terminal)
	applyWebNotificationsYAML(cfg, w.Notifications)
}

func applyWebReconnectYAML(cfg *Config, r *rawReconnectConfig) {
	if r == nil {
		return
	}
	if r.InitialDelay != nil {
		cfg.Web.Reconnect.InitialDelay = *r.InitialDelay
	}
	if r.MaxDelay != nil {
		cfg.Web.Reconnect.MaxDelay = *r.MaxDelay
	}
}

func applyWebTerminalYAML(cfg *Config, t *rawWebTerminalConfig) {
	if t == nil {
		return
	}
	if t.Scrollback != nil {
		cfg.Web.Terminal.Scrollback = *t.Scrollback
	}
	if t.FontSize != nil {
		cfg.Web.Terminal.FontSize = *t.FontSize
	}
	if t.MinFontSize != nil {
		cfg.Web.Terminal.MinFontSize = *t.MinFontSize
	}
	if t.MaxFontSize != nil {
		cfg.Web.Terminal.MaxFontSize = *t.MaxFontSize
	}
}

func applyWebNotificationsYAML(cfg *Config, n *rawNotificationsConfig) {
	if n == nil {
		return
	}
	if n.MaxToasts != nil {
		cfg.Web.Notifications.MaxToasts = *n.MaxToasts
	}
	if n.ErrorLifetime != nil {
		cfg.Web.Notifications.ErrorLifetime = *n.ErrorLifetime
	}
	if n.WarningLifetime != nil {
		cfg.Web.Notifications.WarningLifetime = *n.WarningLifetime
	}
	if n.InfoLifetime != nil {
		cfg.Web.Notifications.InfoLifetime = *n.InfoLifetime
	}
}

func applyEnv(cfg *Config, prov *Provenance, lookupEnv EnvLookup, workDir string) {
	if val, ok := lookupEnv("VISUAL_TMUX_CLIENT_ADDR"); ok {
		cfg.Server.Addr = val
		prov.AddrSource = SourceEnvironment
	}
	if val, ok := lookupEnv("VISUAL_TMUX_CLIENT_TOKEN"); ok {
		cfg.Auth.Token = val
		prov.TokenSource = SourceEnvironment
	}
	if val, ok := lookupEnv("VISUAL_TMUX_CLIENT_ORIGIN"); ok {
		cfg.WebSocket.Origin = val
		prov.OriginSource = SourceEnvironment
	}
	if val, ok := lookupEnv("VISUAL_TMUX_CLIENT_TMUX_PATH"); ok {
		if val != "" && !filepath.IsAbs(val) {
			val = filepath.Clean(filepath.Join(workDir, val))
		}
		cfg.Tmux.Path = val
		prov.TmuxPathSource = SourceEnvironment
	}
}
