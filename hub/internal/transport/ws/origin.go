package ws

// CheckOrigin reports whether a WebSocket upgrade's Origin header is
// acceptable for an explicitly configured origin. Requests without an Origin
// (non-browser clients such as curl and CLI websocket tools) are allowed; a
// declared origin must equal the configured public origin exactly.
func CheckOrigin(origin, configured string) bool {
	if origin == "" {
		return true
	}
	return origin == configured
}
