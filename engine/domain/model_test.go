package domain

import "testing"

func TestModel_HostLifecycle(t *testing.T) {
	m := NewModel()

	h := &Host{ID: HostID("local"), Address: "/tmp/tmux-1000/default", Status: HostStatusConnected}
	m.UpsertHost(h)

	got := m.Host(HostID("local"))
	if got == nil {
		t.Fatalf("expected host to be found after UpsertHost")
	}
	if got.Status != HostStatusConnected {
		t.Errorf("expected status Connected, got %v", got.Status)
	}

	m.RemoveHost(HostID("local"))
	if m.Host(HostID("local")) != nil {
		t.Errorf("expected host to be removed")
	}
}

func TestModel_SessionWindowPaneHierarchy(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	if s := m.Session(key); s == nil {
		t.Fatalf("expected session to be found after UpsertSession")
	}

	m.UpsertWindow(key, &Window{ID: "@1", Name: "main"})
	w := m.Window(key, "@1")
	if w == nil {
		t.Fatalf("expected window to be found after UpsertWindow")
	}
	if w.SessionKey != key {
		t.Errorf("expected window.SessionKey to be set to %v, got %v", key, w.SessionKey)
	}

	m.UpsertPane(key, "@1", &Pane{ID: "%3", X: 5, Y: 10, Width: 80, Height: 24})
	p := m.Pane("%3")
	if p == nil {
		t.Fatalf("expected pane to be found by direct ID lookup after UpsertPane")
	}
	if p.SessionKey != key || p.WindowID != "@1" {
		t.Errorf("expected pane to be stamped with session key %v and window @1, got %v / %s", key, p.SessionKey, p.WindowID)
	}
	if p.X != 5 || p.Y != 10 || p.Width != 80 || p.Height != 24 {
		t.Errorf("expected pane geometry X=5 Y=10 Width=80 Height=24, got X=%d Y=%d Width=%d Height=%d", p.X, p.Y, p.Width, p.Height)
	}

	w = m.Window(key, "@1")
	if wp := w.Panes["%3"]; wp == nil || wp.X != 5 || wp.Y != 10 {
		t.Errorf("expected window snapshot's pane to carry the same geometry, got %+v", wp)
	}
}

func TestModel_SessionKeyIsCompound(t *testing.T) {
	m := NewModel()
	keyA := SessionKey{HostID: "hostA", Name: "work"}
	keyB := SessionKey{HostID: "hostB", Name: "work"}

	m.UpsertSession(&Session{Key: keyA, ID: "$0"})
	m.UpsertSession(&Session{Key: keyB, ID: "$0"})

	if len(m.Sessions()) != 2 {
		t.Fatalf("expected two distinct sessions with same Name but different HostID, got %d", len(m.Sessions()))
	}
}

func TestModel_RemoveSessionCascadesToPaneIndex(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertWindow(key, &Window{ID: "@1"})
	m.UpsertPane(key, "@1", &Pane{ID: "%3"})

	if m.Pane("%3") == nil {
		t.Fatalf("expected pane to exist before session removal")
	}

	m.RemoveSession(key)

	if m.Session(key) != nil {
		t.Errorf("expected session to be removed")
	}
	if m.Pane("%3") != nil {
		t.Errorf("expected pane index entry to be cleaned up when session is removed")
	}
}

func TestModel_RemoveWindowCascadesToPaneIndex(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertWindow(key, &Window{ID: "@1"})
	m.UpsertPane(key, "@1", &Pane{ID: "%3"})

	m.RemoveWindow(key, "@1")

	if m.Window(key, "@1") != nil {
		t.Errorf("expected window to be removed")
	}
	if m.Pane("%3") != nil {
		t.Errorf("expected pane index entry to be cleaned up when window is removed")
	}
}

func TestModel_RemovePane(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertWindow(key, &Window{ID: "@1"})
	m.UpsertPane(key, "@1", &Pane{ID: "%3"})

	m.RemovePane("%3")

	if m.Pane("%3") != nil {
		t.Errorf("expected pane to be removed from index")
	}
	w := m.Window(key, "@1")
	if w == nil {
		t.Fatalf("expected window to still exist")
	}
	if _, ok := w.Panes["%3"]; ok {
		t.Errorf("expected pane to be removed from its window's Panes map")
	}
}

func TestModel_UpsertPaneNoOpWhenSessionOrWindowMissing(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "ghost"}

	// No session exists at all.
	m.UpsertPane(key, "@1", &Pane{ID: "%9"})
	if m.Pane("%9") != nil {
		t.Errorf("expected no-op when session does not exist")
	}

	// Session exists but window does not.
	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertPane(key, "@missing", &Pane{ID: "%9"})
	if m.Pane("%9") != nil {
		t.Errorf("expected no-op when window does not exist")
	}
}

func TestModel_RenameSessionMovesKeyAndRestampsChildren(t *testing.T) {
	m := NewModel()
	oldKey := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: oldKey, ID: "$0"})
	m.UpsertWindow(oldKey, &Window{ID: "@1"})
	m.UpsertPane(oldKey, "@1", &Pane{ID: "%3"})

	renamed := m.RenameSession(oldKey, "renamed")
	if renamed == nil {
		t.Fatalf("expected RenameSession to return the renamed session")
	}
	newKey := SessionKey{HostID: "local", Name: "renamed"}
	if renamed.Key != newKey {
		t.Errorf("expected renamed session's Key to be %v, got %v", newKey, renamed.Key)
	}

	if m.Session(oldKey) != nil {
		t.Errorf("expected old session key to no longer resolve")
	}
	got := m.Session(newKey)
	if got == nil {
		t.Fatalf("expected session to be found under its new key")
	}
	w := got.Windows["@1"]
	if w == nil || w.SessionKey != newKey {
		t.Errorf("expected window to be restamped with new session key, got %+v", w)
	}

	p := m.Pane("%3")
	if p == nil {
		t.Fatalf("expected pane to still be found by direct ID lookup after rename")
	}
	if p.SessionKey != newKey {
		t.Errorf("expected pane to be restamped with new session key, got %v", p.SessionKey)
	}
}

func TestModel_RenameSessionNoOpWhenMissing(t *testing.T) {
	m := NewModel()
	got := m.RenameSession(SessionKey{HostID: "local", Name: "ghost"}, "whatever")
	if got != nil {
		t.Errorf("expected nil when renaming a session that does not exist, got %+v", got)
	}
}

func TestModel_SetActivePaneClearsSiblings(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertWindow(key, &Window{ID: "@1"})
	m.UpsertPane(key, "@1", &Pane{ID: "%1", Active: true})
	m.UpsertPane(key, "@1", &Pane{ID: "%2", Active: false})

	m.SetActivePane(key, "@1", "%2")

	w := m.Window(key, "@1")
	if w.Panes["%1"].Active {
		t.Errorf("expected %%1 to no longer be active")
	}
	if !w.Panes["%2"].Active {
		t.Errorf("expected %%2 to be active")
	}
}

func TestModel_SetActivePaneNoOpWhenPaneMissing(t *testing.T) {
	m := NewModel()
	key := SessionKey{HostID: "local", Name: "alpha"}

	m.UpsertSession(&Session{Key: key, ID: "$0"})
	m.UpsertWindow(key, &Window{ID: "@1"})
	m.UpsertPane(key, "@1", &Pane{ID: "%1", Active: true})

	m.SetActivePane(key, "@1", "%missing")

	w := m.Window(key, "@1")
	if !w.Panes["%1"].Active {
		t.Errorf("expected %%1 to remain active when target pane is missing")
	}
}
