package session

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeBackend struct {
	listCalls   int
	createCalls int
	renameCalls int
	killCalls   int

	createErr error
	renameErr error
	killErr   error
	listErr   error

	sessions []Session
}

func (f *fakeBackend) List(ctx context.Context) ([]Session, error) {
	f.listCalls++
	return f.sessions, f.listErr
}

func (f *fakeBackend) Create(ctx context.Context, name string) (*Session, error) {
	f.createCalls++
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &Session{Name: name, Windows: 1}, nil
}

func (f *fakeBackend) Rename(ctx context.Context, oldName, newName string) error {
	f.renameCalls++
	return f.renameErr
}

func (f *fakeBackend) Kill(ctx context.Context, name string) error {
	f.killCalls++
	return f.killErr
}

func TestInvalidNameDoesNotCallBackend(t *testing.T) {
	invalidNames := []string{
		"",
		"has:colon",
		"has.dot",
		"has/slash",
		"has\\backslash",
		"has：fullwidthcolon",
		"has．fullwidthdot",
		" leading",
		"trailing ",
		"has\nnewline",
		strings.Repeat("a", MaxSessionNameRunes+1),
	}

	for _, name := range invalidNames {
		if name != "" {
			t.Run("Create_"+name, func(t *testing.T) {
				backend := &fakeBackend{}
				svc := NewService(backend)

				_, err := svc.CreateSession(context.Background(), name)
				if !errors.Is(err, ErrInvalidName) {
					t.Fatalf("expected ErrInvalidName, got: %v", err)
				}
				if backend.createCalls != 0 {
					t.Errorf("backend should not have been called for invalid name %q", name)
				}
			})
		}

		t.Run("RenameOld_"+name, func(t *testing.T) {
			backend := &fakeBackend{}
			svc := NewService(backend)

			err := svc.RenameSession(context.Background(), name, "valid-new")
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("expected ErrInvalidName, got: %v", err)
			}
			if backend.renameCalls != 0 {
				t.Errorf("backend should not have been called for invalid old name %q", name)
			}
		})

		t.Run("RenameNew_"+name, func(t *testing.T) {
			backend := &fakeBackend{}
			svc := NewService(backend)

			err := svc.RenameSession(context.Background(), "valid-old", name)
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("expected ErrInvalidName, got: %v", err)
			}
			if backend.renameCalls != 0 {
				t.Errorf("backend should not have been called for invalid new name %q", name)
			}
		})

		t.Run("Kill_"+name, func(t *testing.T) {
			backend := &fakeBackend{}
			svc := NewService(backend)

			err := svc.KillSession(context.Background(), name)
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("expected ErrInvalidName, got: %v", err)
			}
			if backend.killCalls != 0 {
				t.Errorf("backend should not have been called for invalid name %q", name)
			}
		})
	}
}

func TestCreateSession_EmptyNameDelegatesToBackend(t *testing.T) {
	backend := &fakeBackend{}
	svc := NewService(backend)

	sess, err := svc.CreateSession(context.Background(), "")
	if err != nil {
		t.Fatalf("expected nil error for empty session name, got: %v", err)
	}
	if backend.createCalls != 1 {
		t.Errorf("backend.Create should have been called once for empty name, got %d", backend.createCalls)
	}
	if sess == nil {
		t.Fatal("expected non-nil session returned")
	}
}

func TestValidOperationsAndErrorDistinction(t *testing.T) {
	ctx := context.Background()

	// 1. Successful Create
	backend := &fakeBackend{}
	svc := NewService(backend)
	sess, err := svc.CreateSession(ctx, "my-session-开发")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if sess.Name != "my-session-开发" || backend.createCalls != 1 {
		t.Errorf("expected session name and 1 create call, got %v (%d calls)", sess, backend.createCalls)
	}

	// 2. Name in use error
	backend.createErr = ErrNameInUse
	_, err = svc.CreateSession(ctx, "my-session")
	if !errors.Is(err, ErrNameInUse) {
		t.Errorf("expected ErrNameInUse, got: %v", err)
	}

	// 3. Rename not found
	backend.renameErr = ErrNotFound
	err = svc.RenameSession(ctx, "old-name", "new-name")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}

	// 4. Kill tmux not found
	backend.killErr = ErrTmuxNotFound
	err = svc.KillSession(ctx, "target-session")
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Errorf("expected ErrTmuxNotFound, got: %v", err)
	}

	// 5. List sessions
	expectedSessions := []Session{{Name: "s1", Windows: 1}, {Name: "s2", Windows: 2}}
	backend.sessions = expectedSessions
	list, err := svc.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(list) != 2 || backend.listCalls != 1 {
		t.Errorf("expected 2 sessions and 1 list call, got %d sessions, %d calls", len(list), backend.listCalls)
	}
}

func TestValidateSessionName_SecurityEdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		sessName  string
		wantValid bool
	}{
		{"valid ascii", "my-session-1", true},
		{"valid unicode runes", "项目开发-测试-🚀", true},
		{"exact 64 runes", strings.Repeat("测", 64), true},
		{"exceeds 64 runes", strings.Repeat("测", 65), false},
		{"invalid utf8", "\xff\xfe\xfd", false},
		{"empty string", "", false},
		{"leading space", " session", false},
		{"trailing space", "session ", false},
		{"leading ideographic space", "\u3000session", false},
		{"trailing ideographic space", "session\u3000", false},
		{"leading tab", "\tsession", false},
		{"trailing newline", "session\n", false},
		{"null byte injection", "sess\x00ion", false},
		{"ansi escape injection", "sess\x1b[31minjection", false},
		{"tmux delimiter colon", "sess:0", false},
		{"tmux delimiter dot", "sess.0", false},
		{"unix path separator slash", "path/session", false},
		{"windows path separator backslash", "path\\session", false},
		{"full width colon lookalike", "sess：0", false},
		{"full width dot lookalike", "sess．0", false},
		{"tmux command separator semicolon", "sess;0", false},
		{"full width semicolon lookalike", "sess；0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSessionName(tt.sessName)
			if tt.wantValid && err != nil {
				t.Errorf("expected valid for %q, got error: %v", tt.sessName, err)
			}
			if !tt.wantValid && err == nil {
				t.Errorf("expected error for %q, got valid", tt.sessName)
			}
		})
	}
}

type mockFastBackend struct {
	fakeBackend
	getCalls int
	hasCalls int
}

func (m *mockFastBackend) Get(ctx context.Context, name string) (*Session, error) {
	m.getCalls++
	for i := range m.sessions {
		if m.sessions[i].Name == name {
			return &m.sessions[i], nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockFastBackend) HasSession(ctx context.Context, name string) bool {
	m.hasCalls++
	s, err := m.Get(ctx, name)
	return err == nil && s != nil
}

func TestService_GetAndHasSession_FastInterfacesAndFallback(t *testing.T) {
	ctx := context.Background()

	// 1. Backend implementing fastGetter and fastChecker
	fast := &mockFastBackend{
		fakeBackend: fakeBackend{
			sessions: []Session{{Name: "fast-sess", Windows: 1}},
		},
	}
	svcFast := NewService(fast)

	s, err := svcFast.GetSession(ctx, "fast-sess")
	if err != nil || s.Name != "fast-sess" {
		t.Fatalf("fast GetSession failed: %v", err)
	}
	if fast.getCalls != 1 {
		t.Errorf("expected fast Get to be called, got %d", fast.getCalls)
	}

	if !svcFast.HasSession(ctx, "fast-sess") {
		t.Errorf("expected HasSession true for fast-sess")
	}
	if fast.hasCalls != 1 {
		t.Errorf("expected fast HasSession to be called, got %d", fast.hasCalls)
	}

	// 2. Fallback backend (implements only Backend)
	fallback := &fakeBackend{
		sessions: []Session{{Name: "fallback-sess", Windows: 2}},
	}
	svcFallback := NewService(fallback)

	s2, err := svcFallback.GetSession(ctx, "fallback-sess")
	if err != nil || s2.Name != "fallback-sess" {
		t.Fatalf("fallback GetSession failed: %v", err)
	}
	if fallback.listCalls != 1 {
		t.Errorf("expected List to be called as fallback, got %d", fallback.listCalls)
	}

	if !svcFallback.HasSession(ctx, "fallback-sess") {
		t.Errorf("expected HasSession true via fallback")
	}

	// 3. Invalid session name returns ErrInvalidName without touching backend
	_, err = svcFast.GetSession(ctx, "invalid:name")
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("expected ErrInvalidName for GetSession with colon, got: %v", err)
	}
}
