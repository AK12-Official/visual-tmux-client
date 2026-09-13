package config

// Source represents where a specific configuration value originated.
type Source string

const (
	// SourceDefault indicates the value came from built-in defaults.
	SourceDefault Source = "default"
	// SourceYAML indicates the value was specified in a YAML configuration file.
	SourceYAML Source = "yaml"
	// SourceEnvironment indicates the value was supplied by an environment variable.
	SourceEnvironment Source = "environment"
	// SourceCLI indicates the value was supplied via an explicit CLI argument.
	SourceCLI Source = "cli"
	// SourceGenerated indicates the credential was generated automatically.
	SourceGenerated Source = "generated"
)

// Provenance records the origin of key configuration values and the configuration file path.
type Provenance struct {
	ConfigFile     string // Resolved configuration file path, or empty if none loaded
	ExplicitConfig bool   // Whether --config was explicitly provided
	AddrSource     Source // Source of server.addr
	TokenSource    Source // Source of auth.token
	OriginSource   Source // Source of websocket.origin
	TmuxPathSource Source // Source of tmux.path
}
