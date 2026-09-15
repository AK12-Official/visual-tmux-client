package session

import "testing"

func TestSelectPaneSummariesRanksRepresentativePanes(t *testing.T) {
	cases := []struct {
		name  string
		panes []Pane
		want  string
	}{
		{
			name: "active pane of the active window outranks an active pane",
			panes: []Pane{
				{Session: "s", PaneActive: true, WindowName: "active-pane"},
				{Session: "s", WindowActive: true, PaneActive: true, WindowName: "active-both"},
			},
			want: "active-both",
		},
		{
			name: "active pane outranks a pane merely in the active window",
			panes: []Pane{
				{Session: "s", WindowActive: true, WindowName: "active-window"},
				{Session: "s", PaneActive: true, WindowName: "active-pane"},
			},
			want: "active-pane",
		},
		{
			name: "a pane in the active window outranks an ordinary pane",
			panes: []Pane{
				{Session: "s", WindowName: "ordinary"},
				{Session: "s", WindowActive: true, WindowName: "active-window"},
			},
			want: "active-window",
		},
		{
			name: "input order does not decide the ranking",
			panes: []Pane{
				{Session: "s", WindowActive: true, PaneActive: true, WindowName: "active-both"},
				{Session: "s", WindowActive: true, WindowName: "active-window"},
			},
			want: "active-both",
		},
		{
			name: "a lone ordinary pane is still representative",
			panes: []Pane{
				{Session: "s", WindowName: "ordinary"},
			},
			want: "ordinary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			summary, ok := SelectPaneSummaries(tc.panes)["s"]
			if !ok {
				t.Fatalf("expected a summary for session s")
			}
			if summary.WindowName != tc.want {
				t.Errorf("window name = %q, want %q", summary.WindowName, tc.want)
			}
		})
	}
}

func TestSelectPaneSummariesIgnoresDeadPanes(t *testing.T) {
	t.Run("a session whose only pane has exited has no summary", func(t *testing.T) {
		got := SelectPaneSummaries([]Pane{
			{Session: "ended", Dead: true, WindowActive: true, PaneActive: true, WindowName: "gone"},
		})
		if _, ok := got["ended"]; ok {
			t.Fatalf("expected no summary, got %+v", got["ended"])
		}
	})

	t.Run("a dead pane never displaces a live one", func(t *testing.T) {
		got := SelectPaneSummaries([]Pane{
			{Session: "s", Dead: true, WindowActive: true, PaneActive: true, WindowName: "dead"},
			{Session: "s", WindowName: "live"},
		})
		summary, ok := got["s"]
		if !ok {
			t.Fatalf("expected a summary from the live pane")
		}
		if summary.WindowName != "live" {
			t.Errorf("window name = %q, want %q", summary.WindowName, "live")
		}
	})
}

func TestSelectPaneSummariesPreservesFieldsVerbatim(t *testing.T) {
	got := SelectPaneSummaries([]Pane{{
		Session:        "s",
		WindowActive:   true,
		PaneActive:     true,
		WindowName:     "窗口 one",
		Title:          "标题 | with pipe",
		CurrentCommand: "nvim",
	}})
	summary, ok := got["s"]
	if !ok {
		t.Fatalf("expected a summary")
	}
	if summary.WindowName != "窗口 one" {
		t.Errorf("window name = %q", summary.WindowName)
	}
	if summary.Title != "标题 | with pipe" {
		t.Errorf("title = %q", summary.Title)
	}
	if summary.CurrentCommand != "nvim" {
		t.Errorf("command = %q", summary.CurrentCommand)
	}
	if !summary.WindowActive {
		t.Errorf("window_active was not carried through")
	}
}

func TestSelectPaneSummariesIsPerSession(t *testing.T) {
	got := SelectPaneSummaries([]Pane{
		{Session: "a", PaneActive: true, WindowName: "a-win", CurrentCommand: "vim"},
		{Session: "b", PaneActive: true, WindowName: "b-win", CurrentCommand: "zsh"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d summaries, want 2", len(got))
	}
	if got["a"].CurrentCommand != "vim" || got["b"].CurrentCommand != "zsh" {
		t.Errorf("summaries are not per-session: %+v", got)
	}
}

func TestSelectPaneSummariesEmptyInput(t *testing.T) {
	if got := SelectPaneSummaries(nil); len(got) != 0 {
		t.Fatalf("got %d summaries, want none", len(got))
	}
}
