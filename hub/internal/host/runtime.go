package host

import (
	"context"
	"io"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
)

// Sessions provides session operations on one captured host runtime.
type Sessions interface {
	ListSessions(ctx context.Context) ([]session.Session, error)
	CreateSession(ctx context.Context, name string) (*session.Session, error)
	RenameSession(ctx context.Context, oldName, newName string) error
	KillSession(ctx context.Context, name string) error
	GetSession(ctx context.Context, name string) (*session.Session, error)
	PaneWorkingDirectory(ctx context.Context, name string) (string, error)
}

// Files provides file operations independently of terminal support.
type Files interface {
	Enabled() bool
	StartDirectory(candidate string) (string, bool)
	List(ctx context.Context, path string) (files.ListResult, error)
	Read(ctx context.Context, path string) (files.ReadResult, error)
	Write(
		ctx context.Context, path string, body io.Reader, declaredSize int64,
		expected *files.ExpectedMtime, allowOtherNames bool,
	) (files.WriteResult, error)
	Create(ctx context.Context, path string, isDir bool) error
	Rename(ctx context.Context, path, newPath string) error
	Delete(ctx context.Context, path string, recursive bool) error
}

// Runtime groups independent services belonging to one configuration revision.
// A resolver returns a value snapshot; callers must not resolve again mid-request.
type Runtime struct {
	Target    Target
	Sessions  Sessions
	Files     Files
	Processes terminal.ProcessFactory
}

// Resolver selects a runtime without initiating remote work.
type Resolver interface {
	Resolve(context.Context, ID) (Runtime, error)
}

type localResolver struct{ runtime Runtime }

// NewLocalResolver exposes only the immutable built-in local host.
func NewLocalResolver(s Sessions, f Files, p terminal.ProcessFactory) Resolver {
	return &localResolver{runtime: Runtime{Target: Target{HostID: LocalID, Revision: LocalRevision, ConnectionRevision: LocalRevision}, Sessions: s, Files: f, Processes: p}}
}

func (r *localResolver) Resolve(ctx context.Context, id ID) (Runtime, error) {
	if err := ctx.Err(); err != nil {
		return Runtime{}, err
	}
	if id != LocalID {
		return Runtime{}, &Error{Target: Target{HostID: id}, Code: ErrNotFound}
	}
	return r.runtime, nil
}

type multiHostResolver struct {
	local   Runtime
	reg     *Registry
	connMgr *ConnectionManager
}

// NewMultiHostResolver resolves local and registered remote hosts without initiating remote work.
func NewMultiHostResolver(local Runtime, reg *Registry, connMgr *ConnectionManager) Resolver {
	return &multiHostResolver{local: local, reg: reg, connMgr: connMgr}
}

func (r *multiHostResolver) Resolve(ctx context.Context, id ID) (Runtime, error) {
	if err := ctx.Err(); err != nil {
		return Runtime{}, err
	}
	if id == "" || id == LocalID {
		return r.local, nil
	}
	if r.reg == nil || r.connMgr == nil {
		return Runtime{}, &Error{Target: Target{HostID: id}, Code: ErrNotFound}
	}
	if _, err := r.reg.Get(ctx, id); err != nil {
		return Runtime{}, &Error{Target: Target{HostID: id}, Code: ErrNotFound, Cause: err}
	}
	lease, err := r.connMgr.Lease(ctx, id)
	if err != nil {
		return Runtime{}, err
	}
	sessions := NewRemoteSessionService(lease, "", "tmux")
	processes := NewRemoteProcessFactory(lease, "tmux")
	fileService := NewRemoteFileService(lease, 0, 0, "")
	return Runtime{
		Target:    lease.Target,
		Sessions:  sessions,
		Files:     fileService,
		Processes: processes,
	}, nil
}
