package session

import "context"

// Service provides session business orchestration and name validation.
type Service struct {
	backend Backend
}

// NewService constructs a Service with the given backend.
func NewService(backend Backend) *Service {
	return &Service{backend: backend}
}

// paneLister is implemented by backends that can report panes across sessions.
// It is optional: a backend without it yields sessions with no pane summary.
type paneLister interface {
	ListPanes(ctx context.Context) ([]Pane, error)
}

// ListSessions delegates to the backend to return all active sessions,
// annotating each with a summary of its most representative pane when the
// backend can supply one.
//
// Pane summaries are supplementary: a backend that cannot list panes, or whose
// pane query fails, still returns the session list rather than an error, so an
// optional nicety can never take the whole listing down.
func (s *Service) ListSessions(ctx context.Context) ([]Session, error) {
	sessions, err := s.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	pl, ok := s.backend.(paneLister)
	if !ok {
		return sessions, nil
	}
	panes, err := pl.ListPanes(ctx)
	if err != nil {
		return sessions, nil
	}
	summaries := SelectPaneSummaries(panes)
	for i := range sessions {
		if summary, found := summaries[sessions[i].Name]; found {
			sessions[i].Pane = &summary
		}
	}
	return sessions, nil
}

// CreateSession validates the requested session name before instructing the backend to create it.
// When name is empty, validation is skipped so the backend can generate a unique name.
func (s *Service) CreateSession(ctx context.Context, name string) (*Session, error) {
	if name != "" {
		if err := ValidateSessionName(name); err != nil {
			return nil, err
		}
	}
	return s.backend.Create(ctx, name)
}

// RenameSession validates both session names before instructing the backend to rename the session.
func (s *Service) RenameSession(ctx context.Context, oldName, newName string) error {
	if err := ValidateSessionName(oldName); err != nil {
		return err
	}
	if err := ValidateSessionName(newName); err != nil {
		return err
	}
	return s.backend.Rename(ctx, oldName, newName)
}

// KillSession validates the target session name before instructing the backend to kill it.
func (s *Service) KillSession(ctx context.Context, name string) error {
	if err := ValidateSessionName(name); err != nil {
		return err
	}
	return s.backend.Kill(ctx, name)
}

type fastGetter interface {
	Get(ctx context.Context, name string) (*Session, error)
}

type fastChecker interface {
	HasSession(ctx context.Context, name string) bool
}

// GetSession retrieves a single session by name.
func (s *Service) GetSession(ctx context.Context, name string) (*Session, error) {
	if err := ValidateSessionName(name); err != nil {
		return nil, err
	}
	if fg, ok := s.backend.(fastGetter); ok {
		return fg.Get(ctx, name)
	}
	sessions, err := s.backend.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].Name == name {
			return &sessions[i], nil
		}
	}
	return nil, ErrNotFound
}

// HasSession checks if a session exists.
func (s *Service) HasSession(ctx context.Context, name string) bool {
	if err := ValidateSessionName(name); err != nil {
		return false
	}
	if fc, ok := s.backend.(fastChecker); ok {
		return fc.HasSession(ctx, name)
	}
	sess, err := s.GetSession(ctx, name)
	return err == nil && sess != nil
}

type paneDirectoryReader interface {
	PaneWorkingDirectory(ctx context.Context, name string) (string, error)
}

// PaneWorkingDirectory returns the working directory of a session's active pane.
// It is optional, like the pane summary: a backend that cannot answer reports
// the directory as unavailable rather than failing, so a capability the file
// manager wants can never take the surrounding operation down with it.
func (s *Service) PaneWorkingDirectory(ctx context.Context, name string) (string, error) {
	if err := ValidateSessionName(name); err != nil {
		return "", err
	}
	reader, ok := s.backend.(paneDirectoryReader)
	if !ok {
		return "", ErrNotFound
	}
	return reader.PaneWorkingDirectory(ctx, name)
}
