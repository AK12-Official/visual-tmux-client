package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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

func TestSelectPaneSummariesBreaksTiesByIndex(t *testing.T) {
	t.Run("the lowest window index wins", func(t *testing.T) {
		got := SelectPaneSummaries([]Pane{
			{Session: "s", PaneActive: true, WindowIndex: 2, WindowName: "second-window"},
			{Session: "s", PaneActive: true, WindowIndex: 1, WindowName: "first-window"},
		})
		if got["s"].WindowName != "first-window" {
			t.Errorf("window name = %q, want %q", got["s"].WindowName, "first-window")
		}
	})

	t.Run("the lowest pane index decides within a window", func(t *testing.T) {
		// A reachable tie: these are the inactive panes of the active window, so
		// they share a rank and only the index separates them. Two panes that are
		// both window-active and pane-active cannot occur -- exactly one pane
		// server-wide carries both -- so a fixture built that way would pin a tie
		// the selector can never be asked about.
		got := SelectPaneSummaries([]Pane{
			{Session: "s", WindowActive: true, PaneIndex: 3, WindowName: "pane-three"},
			{Session: "s", WindowActive: true, PaneIndex: 1, WindowName: "pane-one"},
		})
		if got["s"].WindowName != "pane-one" {
			t.Errorf("window name = %q, want %q", got["s"].WindowName, "pane-one")
		}
	})

	t.Run("emission order does not decide an equal-ranked tie", func(t *testing.T) {
		panes := []Pane{
			{Session: "s", PaneActive: true, WindowIndex: 1, WindowName: "first-window"},
			{Session: "s", PaneActive: true, WindowIndex: 2, WindowName: "second-window"},
		}
		reversed := []Pane{panes[1], panes[0]}
		if got, want := SelectPaneSummaries(panes)["s"], SelectPaneSummaries(reversed)["s"]; got != want {
			t.Errorf("tie resolved by input order: %q vs %q", got.WindowName, want.WindowName)
		}
	})

	t.Run("rank still outranks a lower index", func(t *testing.T) {
		got := SelectPaneSummaries([]Pane{
			{Session: "s", WindowIndex: 9, WindowActive: true, PaneActive: true, WindowName: "active-both"},
			{Session: "s", WindowIndex: 0, WindowName: "ordinary"},
		})
		if got["s"].WindowName != "active-both" {
			t.Errorf("window name = %q, want %q", got["s"].WindowName, "active-both")
		}
	})
}

// A pane title is set by whatever program runs in the pane, so it is unbounded
// input: tmux stores and reports it verbatim. The summary is what every client
// receives on every poll, so it must not carry the whole thing.
func TestSelectPaneSummariesClampsOversizedFields(t *testing.T) {
	oversized := strings.Repeat("x", maxPaneFieldRunes*10)
	got := SelectPaneSummaries([]Pane{{
		Session:        "s",
		WindowName:     oversized,
		Title:          oversized,
		CurrentCommand: oversized,
	}})
	summary, ok := got["s"]
	if !ok {
		t.Fatalf("expected a summary")
	}
	for label, value := range map[string]string{
		"window name": summary.WindowName,
		"title":       summary.Title,
		"command":     summary.CurrentCommand,
	} {
		if runes := utf8.RuneCountInString(value); runes != maxPaneFieldRunes {
			t.Errorf("%s has %d runes, want %d", label, runes, maxPaneFieldRunes)
		}
	}
}

func TestSelectPaneSummariesKeepsFieldsWithinTheCap(t *testing.T) {
	atLimit := strings.Repeat("x", maxPaneFieldRunes)
	got := SelectPaneSummaries([]Pane{{
		Session:        "s",
		WindowName:     atLimit,
		Title:          atLimit,
		CurrentCommand: atLimit,
	}})
	summary := got["s"]
	if summary.WindowName != atLimit || summary.Title != atLimit || summary.CurrentCommand != atLimit {
		t.Errorf("a value exactly at the cap was altered")
	}
}

func TestSelectPaneSummariesClampsByRuneNotByte(t *testing.T) {
	multibyte := strings.Repeat("界", maxPaneFieldRunes*2)
	got := SelectPaneSummaries([]Pane{{Session: "s", Title: multibyte}})
	clamped := got["s"].Title
	if !utf8.ValidString(clamped) {
		t.Errorf("clamping split a multi-byte character: %q", clamped)
	}
	if runes := utf8.RuneCountInString(clamped); runes != maxPaneFieldRunes {
		t.Errorf("clamped title has %d runes, want %d", runes, maxPaneFieldRunes)
	}
}
