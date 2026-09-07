package tmuxcm

import (
	"testing"
)

// fixture bytes below are taken verbatim from real `tmux -CC attach`
// sessions captured during this change's spike work (see spike-notes.md).

func TestParser_InitialAttachStripsDCSAndParsesSessionChanged(t *testing.T) {
	raw := []byte("\x1bP1000p%begin 1788519012 334 0\r\n%end 1788519012 334 0\r\n%session-changed $0 alpha\r\n")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}

	if events[0].Result == nil {
		t.Fatalf("expected first event to be a command result, got %+v", events[0])
	}
	if events[0].Result.CommandNumber != 334 || events[0].Result.IsError {
		t.Errorf("unexpected result: %+v", events[0].Result)
	}

	if events[1].Notification == nil {
		t.Fatalf("expected second event to be a notification, got %+v", events[1])
	}
	n := events[1].Notification
	if n.Type != NotifSessionChanged || n.Name != "alpha" {
		t.Errorf("unexpected notification: %+v", n)
	}
}

func TestParser_CommandResultWithLines(t *testing.T) {
	raw := []byte("\x1bP1000p%begin 1788519104 340 0\r\n%end 1788519104 340 0\r\n%session-changed $0 alpha\r\n%begin 1788519105 344 1\r\nalpha:@0.%0 1\r\nalpha:@1.%1 1\r\nbeta:@2.%2 1\r\n%end 1788519105 344 1\r\n")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	result := events[2].Result
	if result == nil {
		t.Fatalf("expected third event to be a command result")
	}
	wantLines := []string{"alpha:@0.%0 1", "alpha:@1.%1 1", "beta:@2.%2 1"}
	if len(result.Lines) != len(wantLines) {
		t.Fatalf("expected %d lines, got %d: %v", len(wantLines), len(result.Lines), result.Lines)
	}
	for i, want := range wantLines {
		if result.Lines[i] != want {
			t.Errorf("line %d: expected %q, got %q", i, want, result.Lines[i])
		}
	}
}

func TestParser_OutputNotificationDecodesOctalEscapes(t *testing.T) {
	// From spike capture: echo "hi-from-pane" in pane %0, including a
	// backspace (\010) and other control bytes.
	raw := []byte("%output %0 e\\010echo \r\n")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	n := events[0].Notification
	if n == nil || n.Type != NotifOutput {
		t.Fatalf("expected an output notification, got %+v", events[0])
	}
	if n.PaneID != "%0" {
		t.Errorf("expected pane ID %%0, got %q", n.PaneID)
	}
	want := []byte("e\x08echo ")
	if string(n.Value) != string(want) {
		t.Errorf("expected decoded value %q, got %q", want, n.Value)
	}
}

func TestParser_OutputNotificationDecodesEscapedBackslash(t *testing.T) {
	// From spike capture: a literal single backslash byte in pane output is
	// escaped by tmux as four consecutive \134 sequences per backslash
	// character typed at the shell, but the isolated single-backslash
	// output line itself is exactly one \134 escape.
	raw := []byte("%output %0 \\134\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifOutput {
		t.Fatalf("expected an output notification, got %+v", events[0])
	}
	if string(n.Value) != "\\" {
		t.Errorf("expected decoded value to be a single backslash, got %q", n.Value)
	}
}

func TestParser_OutputNotificationPassesThroughUTF8Unescaped(t *testing.T) {
	// From spike capture: printf 'B\\\\Sé\n' produced raw UTF-8 bytes for
	// 'é' (0xc3 0xa9) directly in the %output value, not octal-escaped —
	// tmux only escapes non-printable bytes and backslash, not valid UTF-8.
	raw := []byte("%output %0 B\\134\\134S\xc3\xa9\\015\\012\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifOutput {
		t.Fatalf("expected an output notification, got %+v", events[0])
	}
	want := []byte("B\\\\S\xc3\xa9\r\n")
	if string(n.Value) != string(want) {
		t.Errorf("expected %q, got %q", want, n.Value)
	}
}

func TestParser_LayoutChangeNotification(t *testing.T) {
	raw := []byte("%layout-change @0 c196,80x24,0,0[80x12,0,0,0,80x11,0,13,3] c196,80x24,0,0[80x12,0,0,0,80x11,0,13,3] -\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifLayoutChange {
		t.Fatalf("expected a layout-change notification, got %+v", events[0])
	}
	if n.WindowID != "@0" {
		t.Errorf("expected window ID @0, got %q", n.WindowID)
	}
	wantLayout := "c196,80x24,0,0[80x12,0,0,0,80x11,0,13,3]"
	if n.Layout != wantLayout {
		t.Errorf("expected layout %q, got %q", wantLayout, n.Layout)
	}
}

func TestParser_WindowRenamedNotification(t *testing.T) {
	raw := []byte("%window-renamed @0 tmux\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifWindowRenamed {
		t.Fatalf("expected a window-renamed notification, got %+v", events[0])
	}
	if n.WindowID != "@0" || n.Name != "tmux" {
		t.Errorf("unexpected notification: %+v", n)
	}
}

func TestParser_SessionRenamedNotification(t *testing.T) {
	// tmux's actual wire format carries a leading session-id field (unlike
	// the abbreviated synopsis in tmux(1)), e.g. as observed against tmux
	// 3.7b: "%session-renamed $0 renamed-externally".
	raw := []byte("%session-renamed $0 renamed-externally\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifSessionRenamed {
		t.Fatalf("expected a session-renamed notification, got %+v", events[0])
	}
	if n.Name != "renamed-externally" {
		t.Errorf("expected Name %q, got %+v", "renamed-externally", n)
	}
}

func TestParser_WindowPaneChangedNotification(t *testing.T) {
	raw := []byte("%window-pane-changed @0 %3\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifWindowPaneChanged {
		t.Fatalf("expected a window-pane-changed notification, got %+v", events[0])
	}
	if n.WindowID != "@0" || n.PaneID != "%3" {
		t.Errorf("unexpected notification: %+v", n)
	}
}

func TestParser_SessionsChangedAndUnlinkedWindowNotifications(t *testing.T) {
	raw := []byte("%unlinked-window-add @3\r\n%sessions-changed\r\n%unlinked-window-renamed @3 zsh\r\n%unlinked-window-close @3\r\n")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if events[0].Notification.Type != NotifUnlinkedWindowAdd || events[0].Notification.WindowID != "@3" {
		t.Errorf("unexpected event 0: %+v", events[0].Notification)
	}
	if events[1].Notification.Type != NotifSessionsChanged {
		t.Errorf("unexpected event 1: %+v", events[1].Notification)
	}
	if events[2].Notification.Type != NotifUnlinkedWindowRenamed || events[2].Notification.WindowID != "@3" || events[2].Notification.Name != "zsh" {
		t.Errorf("unexpected event 2: %+v", events[2].Notification)
	}
	if events[3].Notification.Type != NotifUnlinkedWindowClose || events[3].Notification.WindowID != "@3" {
		t.Errorf("unexpected event 3: %+v", events[3].Notification)
	}
}

func TestParser_ExitNotificationWithDCSTerminator(t *testing.T) {
	raw := []byte("%sessions-changed\r\n%exit\r\n\x1b\\")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Notification.Type != NotifExit {
		t.Errorf("expected an exit notification, got %+v", events[1].Notification)
	}
	if events[1].Notification.Reason != "" {
		t.Errorf("expected empty reason, got %q", events[1].Notification.Reason)
	}
}

func TestParser_FeedAcrossMultipleCallsHandlesSplitLines(t *testing.T) {
	p := NewParser()

	// Split the DCS prefix and first notification across two Feed calls,
	// mid-line, as a real pty read loop might deliver partial data.
	part1 := []byte("\x1bP1000p%begin 1 1 0\r\n%end 1 1 0\r\n%sess")
	part2 := []byte("ion-changed $0 alpha\r\n")

	events1 := p.Feed(part1)
	if len(events1) != 1 {
		t.Fatalf("expected 1 event after first partial feed, got %d: %+v", len(events1), events1)
	}

	events2 := p.Feed(part2)
	if len(events2) != 1 {
		t.Fatalf("expected 1 event after completing the split line, got %d: %+v", len(events2), events2)
	}
	if events2[0].Notification == nil || events2[0].Notification.Type != NotifSessionChanged {
		t.Errorf("expected a session-changed notification, got %+v", events2[0])
	}
}

func TestParser_UnknownNotificationTypeIsPreserved(t *testing.T) {
	raw := []byte("%some-future-notification foo bar\r\n")

	p := NewParser()
	events := p.Feed(raw)

	n := events[0].Notification
	if n == nil || n.Type != NotifUnknown {
		t.Fatalf("expected an unknown notification, got %+v", events[0])
	}
	if n.Raw != "%some-future-notification foo bar" {
		t.Errorf("expected Raw to preserve the original line, got %q", n.Raw)
	}
}

func TestParser_ErrorBlock(t *testing.T) {
	raw := []byte("%begin 1 2 0\r\n%error 1 2 0\r\n")

	p := NewParser()
	events := p.Feed(raw)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if !events[0].Result.IsError {
		t.Errorf("expected IsError to be true for an %%error block")
	}
}
