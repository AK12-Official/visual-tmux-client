package session

import "context"

// Backend defines the interface required by the session service to manage sessions.
type Backend interface {
	List(ctx context.Context) ([]Session, error)
	Create(ctx context.Context, name string) (*Session, error)
	Rename(ctx context.Context, oldName, newName string) error
	Kill(ctx context.Context, name string) error
}
