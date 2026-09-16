package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// record joins fields the way tmux's -F output does.
func record(fields ...string) string {
	return strings.Join(fields, paneFieldSep)
}

func TestParsePanesReadsEveryField(t *testing.T) {
	got := ParsePanes(record("demo", "1", "2", "1", "1", "0", "zsh", "编辑器 | 管道", "nvim"))
	if len(got) != 1 {
		t.Fatalf("got %d panes, want 1", len(got))
	}
	p := got[0]
	if p.Session != "demo" {
		t.Errorf("session = %q, want demo", p.Session)
	}
	if p.WindowIndex != 1 || p.PaneIndex != 2 {
		t.Errorf("indexes = %d/%d, want 1/2", p.WindowIndex, p.PaneIndex)
	}
	if !p.WindowActive || !p.PaneActive {
		t.Errorf("expected both active flags set: %+v", p)
	}
	if p.Dead {
		t.Errorf("pane should not be marked dead")
	}
	// Free-form fields must survive verbatim, including non-ASCII and a pipe.
	if p.WindowName != "zsh" {
		t.Errorf("window name = %q, want zsh", p.WindowName)
	}
	if p.Title != "编辑器 | 管道" {
		t.Errorf("title = %q", p.Title)
	}
	if p.CurrentCommand != "nvim" {
		t.Errorf("command = %q, want nvim", p.CurrentCommand)
	}
}

func TestParsePanesReadsDeadPanes(t *testing.T) {
	got := ParsePanes(record("s", "0", "0", "1", "0", "1", "w", "t", "c"))
	if len(got) != 1 {
		t.Fatalf("got %d panes, want 1", len(got))
	}
	if !got[0].Dead || got[0].PaneActive {
		t.Errorf("expected dead and not active: %+v", got[0])
	}
}

// A title containing the field separator makes a record ambiguous. Dropping it is
// the point: guessing would display a title under the window-name label as fact.
func TestParsePanesDropsRecordsWithExtraFields(t *testing.T) {
	bad := record("demo", "0", "0", "1", "1", "0", "zsh", "title"+paneFieldSep+"embedded", "nvim")
	good := record("keep", "0", "0", "1", "1", "0", "zsh", "", "bash")

	got := ParsePanes(bad + "\n" + good)
	if len(got) != 1 {
		t.Fatalf("got %d panes, want only the well-formed record", len(got))
	}
	if got[0].Session != "keep" {
		t.Errorf("kept session = %q, want keep", got[0].Session)
	}
}

func TestParsePanesDropsRecordsWithTooFewFields(t *testing.T) {
	got := ParsePanes(record("demo", "0", "0", "1", "1", "0", "zsh"))
	if len(got) != 0 {
		t.Fatalf("got %d panes, want none", len(got))
	}
}

func TestParsePanesToleratesBlankLinesAndCarriageReturns(t *testing.T) {
	one := record("a", "0", "0", "1", "1", "0", "w", "t", "c")
	got := ParsePanes("\n" + one + "\r\n" + one + "\n")
	if len(got) != 2 {
		t.Fatalf("got %d panes, want 2", len(got))
	}
}

func TestParsePanesTreatsUnparseableIndexesAsZero(t *testing.T) {
	got := ParsePanes(record("a", "", "x", "1", "1", "0", "w", "t", "c"))
	if len(got) != 1 {
		t.Fatalf("got %d panes, want 1", len(got))
	}
	if got[0].WindowIndex != 0 || got[0].PaneIndex != 0 {
		t.Errorf("indexes = %d/%d, want 0/0", got[0].WindowIndex, got[0].PaneIndex)
	}
}

// The format string and the parser must agree, or every record is silently dropped.
func TestPaneFormatFieldCountMatchesParser(t *testing.T) {
	fields := strings.Split(PaneFormat, paneFieldSep)
	if len(fields) != paneFieldCount {
		t.Fatalf("PaneFormat emits %d fields, parser expects %d", len(fields), paneFieldCount)
	}
}

func TestListPanesReportsWhenTmuxIsUnavailable(t *testing.T) {
	c := NewClient("/nonexistent/tmux-binary-for-test", "")
	_, err := c.ListPanes(context.Background())
	if err == nil {
		t.Fatalf("expected an error when tmux cannot be resolved")
	}
	// A caller has to be able to tell "tmux is not installed" from "there are no
	// panes", which is what an untyped error or an empty slice would conflate.
	if !errors.Is(err, session.ErrTmuxNotFound) {
		t.Errorf("error = %v, want it to wrap session.ErrTmuxNotFound", err)
	}
}

// The one failure the no-server branch must not swallow: tmux exists, runs, and
// fails for some other reason. Reporting that as "no panes" would hide a broken
// server behind a silently empty listing.
func TestListPanesReportsAFailedQuery(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "tmux")
	script := "#!/bin/sh\necho 'server exited unexpectedly' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	c := NewClient(fake, "")

	_, err := c.ListPanes(context.Background())
	if err == nil {
		t.Fatalf("expected an error when tmux runs but the query fails")
	}
	if !strings.Contains(err.Error(), "server exited unexpectedly") {
		t.Errorf("error should carry tmux's message, got %v", err)
	}
	// It must not be mistaken for the no-server case, which is not an error.
	if strings.Contains(err.Error(), "exec ") {
		t.Errorf("a non-zero exit was reported as an exec failure: %v", err)
	}
}

func TestListPanesReportsEveryPane(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	if _, err := c.Create(ctx, "panes"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	// split-window takes a pane target, so the exact session name is followed by
	// ":" to select its current window rather than being read as a pane name.
	paneTarget := ExactTarget("panes") + ":"
	_, stderr, code, err := c.Exec(ctx, "split-window", "-h", "-t", paneTarget)
	if err != nil || code != 0 {
		t.Fatalf("split-window failed: code=%d err=%v stderr=%s", code, err, stderr)
	}

	panes, err := c.ListPanes(ctx)
	if err != nil {
		t.Fatalf("ListPanes failed: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("got %d panes, want 2", len(panes))
	}
	for _, p := range panes {
		if p.Session != "panes" {
			t.Errorf("session = %q, want panes", p.Session)
		}
		// tmux derives the window name from the running command, and a real pane
		// always has one; an empty value here would mean the format string is wrong.
		if p.WindowName == "" || p.CurrentCommand == "" {
			t.Errorf("expected a window name and command, got %+v", p)
		}
	}

	// The end-to-end point of the format string: panes must reduce to a summary.
	if _, ok := session.SelectPaneSummaries(panes)["panes"]; !ok {
		t.Errorf("expected a summary for session panes")
	}
}

func TestListPanesWithoutAServer(t *testing.T) {
	if !tmuxAvailable(t) {
		t.Skip("tmux not available")
	}
	t.Setenv("TMUX_TMPDIR", testutil.SocketDir(t))
	c := NewClient("", socketName)

	panes, err := c.ListPanes(context.Background())
	if err != nil {
		t.Fatalf("a missing server must not be an error: %v", err)
	}
	if len(panes) != 0 {
		t.Fatalf("got %d panes, want none", len(panes))
	}
}
