package http

import "github.com/AK12-Official/visual-tmux-client/hub/internal/config"

// PublicClientConfig defines the unauthenticated configuration envelope returned to browsers.
type PublicClientConfig struct {
	Version int         `json:"version"`
	Web     PublicWeb   `json:"web"`
	Files   PublicFiles `json:"files"`
}

// PublicFiles tells the browser whether the file manager is available, and what
// its per-file limit is, so the entry point can be left out rather than offered
// and refused, and a refusal can name the limit instead of a wire code.
//
// Neither value is a boundary. Enabled grants nothing: every operation is gated
// on its own, and MaxFileSize is the same bound the service already enforces.
type PublicFiles struct {
	Enabled     bool  `json:"enabled"`
	MaxFileSize int64 `json:"max_file_size"`
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
func NewPublicClientConfig(web config.WebConfig, filesEnabled bool, maxFileSize int64) PublicClientConfig {
	return PublicClientConfig{
		Version: 1,
		Files:   PublicFiles{Enabled: filesEnabled, MaxFileSize: maxFileSize},
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

// The kinds a create request may name, in the field that decides what is created
// at the path.
//
// The field is required and has no default. Reading an absent value as "a file"
// would answer a request that never said what it wanted with a file at that
// path, and report success: a caller that meant a directory -- and misspelled
// the kind, or asked an older hub -- would be told its directory was created and
// would find a file. Both values are named here so the route can refuse anything
// that is neither.
const (
	fileKindFile      = "file"
	fileKindDirectory = "dir"
)

type createFileRequest struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type renameFileRequest struct {
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
}

type deleteFileRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

// writeFileResponse returns the target's resulting modification time, so the
// browser can keep editing without re-reading the file -- and without guessing
// a value that would make its next save look like a conflict.
//
// Two precisions are reported because they answer different questions.
// `mtime` is milliseconds, which is what a JavaScript number holds exactly and
// what the client displays and compares for nothing but reporting. `mtime_nanos`
// is the exact time the filesystem recorded, carried as a decimal string
// because it exceeds JavaScript's safe integer range: a number would be rounded
// on arrival, and the client would then send back a time that matches no file.
// It is the value the next save is checked against, so two edits inside one
// millisecond are two edits rather than an unconflicted overwrite.
type writeFileResponse struct {
	Mtime      int64  `json:"mtime"`
	MtimeNanos string `json:"mtime_nanos"`
}

// workingDirectoryResponse seeds the file manager's starting directory.
//
// Substituted is set when the session's own directory falls outside a configured
// boundary, in which case Path names a permitted directory instead. It is
// omitted rather than sent as false, so an unrestricted hub answers exactly as
// it did before this field existed.
type workingDirectoryResponse struct {
	Path        string `json:"path"`
	Substituted bool   `json:"substituted,omitempty"`
}
