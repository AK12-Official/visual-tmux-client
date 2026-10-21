// Package host defines host identities shared by routing and runtime adapters.
package host

// ID is an opaque, stable registry identity, never an SSH address.
type ID string

// Revision identifies a persisted configuration version. Zero is not a version.
type Revision uint64

type ConnectionType string

const (
	ConnectionTypeLocal ConnectionType = "local"
	ConnectionTypeSSH   ConnectionType = "ssh"
)

const (
	LocalID       ID       = "local"
	LocalRevision Revision = 1
)

// Target captures the destination of work so a later configuration edit cannot
// silently redirect it. Session is empty for host-wide operations.
type Target struct {
	HostID             ID       `json:"hostId"`
	Revision           Revision `json:"revision"`
	ConnectionRevision Revision `json:"connectionRevision,omitempty"`
	Session            string   `json:"session,omitempty"`
}

// ErrorCode is a stable, safe error category for authenticated API responses.
type ErrorCode string

const (
	ErrNotFound       ErrorCode = "host_not_found"
	ErrUnavailable    ErrorCode = "host_unavailable"
	ErrUnsupported    ErrorCode = "host_unsupported"
	ErrOutcomeUnknown ErrorCode = "outcome_unknown"
	ErrStaleRevision  ErrorCode = "host_revision_conflict"
	ErrDuplicateLocal ErrorCode = "duplicate_local_host"
)

func (c ErrorCode) Error() string { return string(c) }

// Error retains a cause for internal inspection without including potentially
// sensitive transport diagnostics in its printable message.
type Error struct {
	Target Target
	Code   ErrorCode
	Cause  error
}

func (e *Error) Error() string        { return e.Code.Error() }
func (e *Error) Unwrap() error        { return e.Cause }
func (e *Error) Is(target error) bool { return target == e.Code }
