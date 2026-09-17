package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// record joins fields the way tmux's -F output does.
func record(fields ...string) string {
	return strings.Join(fields, paneFieldSep)
}

// fakeTmux points a Client at a script instead of a real tmux, so the command a
// method builds can be checked without starting a server.
func fakeTmux(t *testing.T, body string) *Client {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-tmux")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewClient(bin, "")
}

// The target has to be a pane target: a bare `=work` is only a session target,
// which tmux accepts while resolving nothing, printing an empty line and exiting
// zero. That reads as "no directory" instead of as the targeting mistake it is.
func TestPaneWorkingDirectoryTargetsTheExactSession(t *testing.T) {
	recorded := filepath.Join(t.TempDir(), "args")
	c := fakeTmux(t, fmt.Sprintf(
		"printf '%%s\\n' \"$@\" > %s\nprintf '%%s\\n' '/srv/project'\n", recorded))

	dir, err := c.PaneWorkingDirectory(context.Background(), "work")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory failed: %v", err)
	}
	if dir != "/srv/project" {
		t.Errorf("expected the pane directory, got %q", dir)
	}

	data, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"display-message", "-p", "-t", "=work:", "#{pane_current_path}"}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(got, want) {
		t.Errorf("expected the arguments %v, got %v", want, got)
	}
}

// The fake above pins the argument vector; this one pins that the command
// actually works against a real server, which is the part a wrong target or a
// wrong format would break.
func TestPaneWorkingDirectoryAgainstARealServer(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Create(ctx, "work"); err != nil {
		t.Fatalf("create a session to ask about: %v", err)
	}

	dir, err := c.PaneWorkingDirectory(ctx, "work")
	if err != nil {
		t.Fatalf("PaneWorkingDirectory: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("expected an absolute directory, got %q", dir)
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		t.Errorf("expected %q to be a directory that exists: %v", dir, statErr)
	}
}

func TestPaneWorkingDirectoryReportsAnAbsentSession(t *testing.T) {
	c := fakeTmux(t, "echo \"can't find session: absent\" >&2\nexit 1\n")

	if _, err := c.PaneWorkingDirectory(context.Background(), "absent"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected not-found, got: %v", err)
	}
}

// An unknown format expands to nothing rather than failing, and an empty string
// is not a directory the file manager could open at.
func TestPaneWorkingDirectoryReportsAnEmptyAnswer(t *testing.T) {
	c := fakeTmux(t, "printf '\\n'\n")

	if _, err := c.PaneWorkingDirectory(context.Background(), "work"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected not-found, got: %v", err)
	}
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
	// The count alone would pass without the trailing-CR strip: a stray \r lands
	// in the last field, which nothing else here reads. Assert the fields, so the
	// trim is what the test is actually testing.
	for i, want := range []struct{ window, title, command string }{
		{"w", "t", "c"},
		{"w", "t", "c"},
	} {
		if got[i].WindowName != want.window || got[i].Title != want.title ||
			got[i].CurrentCommand != want.command {
			t.Errorf("record %d = %q/%q/%q, want %q/%q/%q (a CRLF line end must not reach a field)",
				i, got[i].WindowName, got[i].Title, got[i].CurrentCommand,
				want.window, want.title, want.command)
		}
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

// tmux versions used by Ubuntu CI escape control bytes in command output.
// Pin the wire format to printable bytes, independently of record(), so a
// regression to a control-character separator fails even on newer local tmux.
func TestPaneFormatUsesPrintableDelimiter(t *testing.T) {
	for _, r := range PaneFormat {
		if r < ' ' || r > '~' {
			t.Fatalf("PaneFormat contains non-printable ASCII: %q", PaneFormat)
		}
	}
	const line = `panes|vtc-pane|0|vtc-pane|1|vtc-pane|1|vtc-pane|1|vtc-pane|0|vtc-pane|bash` +
		`|vtc-pane|编辑器 | literal \037|vtc-pane|bash`
	panes := ParsePanes(line)
	if len(panes) != 1 || panes[0].Title != `编辑器 | literal \037` || panes[0].PaneIndex != 1 {
		t.Fatalf("printable wire record parsed incorrectly: %+v", panes)
	}
}

// The count above is not enough on its own: ParsePanes reads fields by position,
// so a permuted format string would still parse, and would report one field's
// value as another's -- a pane title shown as the window name, say -- with the
// count, the parser's own fixture and every rendering test still green. The
// order is therefore pinned literally, next to the parser that depends on it.
func TestPaneFormatFieldOrderIsPinnedToTheParser(t *testing.T) {
	const want = "#{session_name}" + paneFieldSep +
		"#{window_index}" + paneFieldSep +
		"#{pane_index}" + paneFieldSep +
		"#{window_active}" + paneFieldSep +
		"#{pane_active}" + paneFieldSep +
		"#{pane_dead}" + paneFieldSep +
		"#{window_name}" + paneFieldSep +
		"#{pane_title}" + paneFieldSep +
		"#{pane_current_command}"
	if PaneFormat != want {
		t.Fatalf("PaneFormat =\n  %q\nwant\n  %q\n(reordering it also reorders ParsePanes's "+
			"field indexing)", PaneFormat, want)
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
		raw, stderr, code, err := c.Exec(ctx, "list-panes", "-a", "-F", PaneFormat)
		t.Fatalf("got %d panes, want 2; diagnostic list-panes: code=%d err=%v stderr=%q out=%q",
			len(panes), code, err, stderr, raw)
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

// The parser reads records by line and fields by separator, and the field count
// is the only thing it checks. A record whose *field* carried the record
// terminator is therefore not detected, and what a break does depends on where it
// is -- so both outcomes are pinned here.
//
// The first is the one that displaces a summary: the break is in the title, the
// fields before it are too few, so the pane's own record is dropped and the tail
// is parsed in its place, naming whichever session the fragment starts with. The
// second shows that a break in the *last* field forges just as well and loses
// nothing at all: the pane's own record survives with a truncated command, and
// the tail is an extra record that no pane stands behind.
//
// Both are here to be stated plainly, so that the field count is not mistaken for
// escaping: it validates the shape of what it was given, and says nothing about
// whether a value inside it held one field or two. What keeps the terminator out
// of the fields is tmux, which the test below asks a real server about.
func TestALineBreakInAFieldWouldNotBeDetected(t *testing.T) {
	forged := record(
		"evil",
		"0", "0", "1", "1", "0", "w",
		// A title ending in a line break and the first eight fields of a record for
		// `victim`; the ninth is the command field that follows the title, which a
		// forger does not control but does not need to.
		"note\n"+
			strings.Join([]string{"victim", "9", "9", "1", "1", "0", "forged", "forged"}, paneFieldSep),
		"sh",
	)

	panes := ParsePanes(forged + "\n")
	if len(panes) != 1 {
		t.Fatalf("got %d panes, want 1 (the fragment): %+v", len(panes), panes)
	}
	if panes[0].Session != "victim" {
		t.Fatalf("expected the tail to parse as a record for victim, got %+v", panes[0])
	}
	// Nothing about it is flagged as suspect, which is the point: the record count
	// is one, the fields are all present, and the summary chosen for `victim` would
	// be this one.
	if panes[0].WindowIndex != 9 || panes[0].PaneIndex != 9 {
		t.Errorf("expected the forged indexes, got %+v", panes[0])
	}

	// The same break in the last field, which is the field a program names itself
	// and the one whose tmux behaviour no test here can cover. It forges a record
	// all the same: the pane's own record is read with a truncated command, and the
	// tail is a second record naming `victim`.
	truncated := ParsePanes(record("evil", "0", "0", "1", "1", "0", "w", "t",
		"cmd\n"+strings.Join(
			[]string{"victim", "9", "9", "1", "1", "0", "forged", "forged", "forged"}, paneFieldSep),
	) + "\n")
	if len(truncated) != 2 {
		t.Fatalf("got %d panes, want the pane and the fragment: %+v", len(truncated), truncated)
	}
	if truncated[0].Session != "evil" || truncated[0].CurrentCommand != "cmd" {
		t.Errorf("expected the pane itself, read with a truncated command, got %+v", truncated[0])
	}
	if truncated[1].Session != "victim" {
		t.Errorf("expected the tail to parse as a record for victim, got %+v", truncated[1])
	}
}

// The names and the title cannot carry one either, and against tmux itself
// rather than against a fake: a session name and a window name containing a
// newline are refused outright when they are set, and select-pane leaves a title
// containing one as it was -- silently, which is why what is asserted for that
// one is the title rather than a status. This is the assumption the parser above
// rests on, and it is not something this code has any say in; see the comment on
// ParsePanes for the field the tests cannot cover.
func TestNamesTmuxRefusesToLetCarryALineBreak(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	if _, err := c.Create(ctx, "work"); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	for _, args := range [][]string{
		{"rename-session", "-t", "=work", "two\nlines"},
		{"rename-window", "-t", "=work", "two\nlines"},
	} {
		_, stderr, code, err := c.Exec(ctx, args...)
		if err != nil {
			t.Fatalf("%v failed: %v", args, err)
		}
		if code == 0 {
			t.Errorf("%v was accepted: a name carrying a line break is a record that was split", args)
		}
		if !strings.Contains(stderr, "invalid") {
			t.Errorf("%v was refused with %q, which does not say the name was the problem", args, stderr)
		}
	}

	// A title is refused silently -- the exit status is zero and the title is left
	// as it was -- so the title is what is asked, not the status.
	if _, stderr, code, err := c.Exec(ctx, "select-pane", "-t", "=work:", "-T", "plain"); err != nil || code != 0 {
		t.Fatalf("select-pane failed: code=%d err=%v stderr=%q", code, err, stderr)
	}
	// Refused or not is not read here: what the title is afterwards is the
	// assertion, and a tmux that refused loudly would leave it as it was too.
	_, _, _, _ = c.Exec( //nolint:errcheck // the title is what is asserted below
		ctx, "select-pane", "-t", "=work:", "-T", "two\nlines")
	title, _, _, err := c.Exec(ctx, "list-panes", "-t", "=work", "-F", "#{pane_title}")
	if err != nil {
		t.Fatalf("list-panes failed: %v", err)
	}
	if got := strings.TrimSuffix(title, "\n"); got != "plain" {
		t.Errorf("expected the title to be left as it was, got %q", got)
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
