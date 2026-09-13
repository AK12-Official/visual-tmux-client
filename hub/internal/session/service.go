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

// ListSessions delegates to the backend to return all active sessions.
func (s *Service) ListSessions(ctx context.Context) ([]Session, error) {
	return s.backend.List(ctx)
}

// CreateSession validates the requested session name before instructing the backend to create it.
func (s *Service) CreateSession(ctx context.Context, name string) (*Session, error) {
	if err := ValidateSessionName(name); err != nil {
		return nil, err
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
	if fc, ok := s.backend.(fastChecker); ok {
		return fc.HasSession(ctx, name)
	}
	sess, err := s.GetSession(ctx, name)
	return err == nil && sess != nil
}
