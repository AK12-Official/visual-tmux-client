package session

// Session represents a single terminal multiplexer session.
type Session struct {
	Name     string `json:"name"`
	Windows  int    `json:"windows"`
	Attached int    `json:"attached"`
	Created  int64  `json:"created"`
	// Pane summarises what the session is currently doing. It is supplementary
	// and omitted when no representative pane could be determined.
	Pane *PaneSummary `json:"pane,omitempty"`
}
