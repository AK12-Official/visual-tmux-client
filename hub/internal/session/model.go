package session

// Session represents a single terminal multiplexer session.
type Session struct {
	Name     string `json:"name"`
	Windows  int    `json:"windows"`
	Attached int    `json:"attached"`
	Created  int64  `json:"created"`
}
