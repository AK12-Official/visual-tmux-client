package session

import (
	"context"
	"errors"
	"testing"
)

// paneBackend is a fakeBackend that can also report panes, exercising the
// optional pane-listing capability.
type paneBackend struct {
	fakeBackend
	panes    []Pane
	panesErr error
}

func (p *paneBackend) ListPanes(ctx context.Context) ([]Pane, error) {
	return p.panes, p.panesErr
}

func TestListSessionsAnnotatesWithPaneSummary(t *testing.T) {
	backend := &paneBackend{
		fakeBackend: fakeBackend{sessions: []Session{{Name: "demo", Windows: 2}}},
		panes: []Pane{{
			Session:        "demo",
			WindowActive:   true,
			PaneActive:     true,
			WindowName:     "zsh",
			Title:          "editor",
			CurrentCommand: "nvim",
		}},
	}

	sessions, err := NewService(backend).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Pane == nil {
		t.Fatalf("expected a pane summary")
	}
	if sessions[0].Pane.CurrentCommand != "nvim" || sessions[0].Pane.WindowName != "zsh" {
		t.Errorf("unexpected summary: %+v", sessions[0].Pane)
	}
	// The listing's own fields must survive annotation.
	if sessions[0].Windows != 2 {
		t.Errorf("windows = %d, want 2", sessions[0].Windows)
	}
}

func TestListSessionsOmitsSummaryWhenEveryPaneHasExited(t *testing.T) {
	backend := &paneBackend{
		fakeBackend: fakeBackend{sessions: []Session{{Name: "ended"}}},
		panes:       []Pane{{Session: "ended", Dead: true, WindowName: "gone"}},
	}

	sessions, err := NewService(backend).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("a session with no live pane must still be listed, got %+v", sessions)
	}
	if sessions[0].Pane != nil {
		t.Errorf("expected no summary, got %+v", sessions[0].Pane)
	}
}

func TestListSessionsSurvivesAFailingPaneQuery(t *testing.T) {
	backend := &paneBackend{
		fakeBackend: fakeBackend{sessions: []Session{{Name: "demo"}}},
		panesErr:    errors.New("tmux list-panes: boom"),
	}

	sessions, err := NewService(backend).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("a failing pane query must not fail the listing: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Pane != nil {
		t.Errorf("expected no summary, got %+v", sessions[0].Pane)
	}
}

func TestListSessionsWithoutPaneSupport(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{{Name: "demo"}, {Name: "other"}}}

	sessions, err := NewService(backend).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("a backend without pane support must still list sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}
	for _, s := range sessions {
		if s.Pane != nil {
			t.Errorf("session %q unexpectedly carries a summary", s.Name)
		}
	}
}

func TestListSessionsStillFailsWhenTheListingFails(t *testing.T) {
	backend := &paneBackend{
		fakeBackend: fakeBackend{listErr: ErrTmuxNotFound},
		panes:       []Pane{{Session: "demo", PaneActive: true, WindowName: "w"}},
	}

	if _, err := NewService(backend).ListSessions(context.Background()); !errors.Is(err, ErrTmuxNotFound) {
		t.Fatalf("err = %v, want ErrTmuxNotFound", err)
	}
}

func TestListSessionsAssignsSummariesPerSession(t *testing.T) {
	backend := &paneBackend{
		fakeBackend: fakeBackend{sessions: []Session{{Name: "a"}, {Name: "b"}, {Name: "c"}}},
		panes: []Pane{
			{Session: "a", PaneActive: true, WindowName: "a-win", CurrentCommand: "vim"},
			{Session: "b", PaneActive: true, WindowName: "b-win", CurrentCommand: "zsh"},
		},
	}

	sessions, err := NewService(backend).ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	byName := make(map[string]*PaneSummary, len(sessions))
	for i := range sessions {
		byName[sessions[i].Name] = sessions[i].Pane
	}
	if byName["a"] == nil || byName["a"].CurrentCommand != "vim" {
		t.Errorf("session a summary = %+v", byName["a"])
	}
	if byName["b"] == nil || byName["b"].CurrentCommand != "zsh" {
		t.Errorf("session b summary = %+v", byName["b"])
	}
	if byName["c"] != nil {
		t.Errorf("session c should have no summary, got %+v", byName["c"])
	}
}
