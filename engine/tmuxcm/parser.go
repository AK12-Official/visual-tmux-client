// Package tmuxcm implements a parser for tmux's control-mode (`-CC`) text
// protocol: the notification stream and command-response blocks
// (%begin/%end/%error) documented in tmux(1)'s CONTROL MODE section.
//
// The parser is pure and has no dependency on any transport (pty, ssh,
// etc.) so it can be tested against recorded byte sequences without a live
// tmux server.
package tmuxcm

import (
	"bytes"
	"strconv"
	"strings"
)

// NotificationType identifies which control-mode notification a
// Notification carries. Names follow tmux(1)'s notification names, without
// the leading '%'.
type NotificationType string

const (
	NotifOutput                NotificationType = "output"
	NotifExtendedOutput         NotificationType = "extended-output"
	NotifSessionChanged         NotificationType = "session-changed"
	NotifSessionRenamed         NotificationType = "session-renamed"
	NotifSessionWindowChanged   NotificationType = "session-window-changed"
	NotifSessionsChanged        NotificationType = "sessions-changed"
	NotifLayoutChange           NotificationType = "layout-change"
	NotifWindowAdd              NotificationType = "window-add"
	NotifWindowClose            NotificationType = "window-close"
	NotifWindowRenamed          NotificationType = "window-renamed"
	NotifWindowPaneChanged      NotificationType = "window-pane-changed"
	NotifUnlinkedWindowAdd      NotificationType = "unlinked-window-add"
	NotifUnlinkedWindowClose    NotificationType = "unlinked-window-close"
	NotifUnlinkedWindowRenamed  NotificationType = "unlinked-window-renamed"
	NotifClientDetached         NotificationType = "client-detached"
	NotifPause                  NotificationType = "pause"
	NotifContinue               NotificationType = "continue"
	NotifPaneModeChanged        NotificationType = "pane-mode-changed"
	NotifExit                   NotificationType = "exit"
	NotifUnknown                NotificationType = "unknown"
)

// Notification is a single parsed control-mode notification line. Only the
// fields relevant to Type are populated; see each NotificationType's
// tmux(1) documentation for its argument shape.
type Notification struct {
	Type NotificationType

	PaneID   string // %output, %extended-output, %pane-mode-changed, %pause, %continue
	WindowID string // %layout-change, %window-*, %unlinked-window-*
	Name     string // %window-renamed, %unlinked-window-renamed (new name); %session-changed (session name)
	Value    []byte // %output, %extended-output: decoded raw bytes (octal escapes resolved)

	Layout        string // %layout-change: the window-layout field
	VisibleLayout string // %layout-change: the window-visible-layout field

	Reason string // %exit: optional reason, empty if absent

	// Raw is the original notification line, without its leading '%' or
	// trailing newline, for diagnostics and for NotifUnknown.
	Raw string
}

// CommandResult is the parsed output of a %begin/%end or %begin/%error
// block: the response to a single command sent to the control-mode client.
type CommandResult struct {
	Timestamp     int64
	CommandNumber int
	Flags         int
	Lines         []string
	IsError       bool
}

// Event is emitted by Parser.Feed: exactly one of Notification or Result is
// non-nil.
type Event struct {
	Notification *Notification
	Result       *CommandResult
}

// dcsPrefix is the DCS (Device Control String) introducer that wraps a
// `-CC` control-mode stream: ESC P 1 0 0 0 p. See spike-notes.md's "1.3 —
// -C vs -CC" for background.
var dcsPrefix = []byte("\x1bP1000p")

// dcsTerminator is the DCS string terminator (ST): ESC \. It is observed at
// the very end of the stream when the control-mode client exits (following
// a final %exit notification).
var dcsTerminator = []byte("\x1b\\")

// Parser incrementally parses a control-mode byte stream fed to it via
// Feed. It is not safe for concurrent use.
type Parser struct {
	buf           []byte
	strippedDCS   bool
	inBlock       bool
	block         CommandResult
}

// NewParser returns a new, empty Parser.
func NewParser() *Parser {
	return &Parser{}
}

// Feed appends data to the parser's internal buffer, extracts and returns
// every complete line's resulting Event, and retains any trailing partial
// line for the next call.
func (p *Parser) Feed(data []byte) []Event {
	p.buf = append(p.buf, data...)

	if !p.strippedDCS {
		if idx := bytes.Index(p.buf, dcsPrefix); idx == 0 {
			p.buf = p.buf[len(dcsPrefix):]
			p.strippedDCS = true
		} else if len(p.buf) < len(dcsPrefix) && bytes.HasPrefix(dcsPrefix, p.buf) {
			// Not enough bytes yet to know if the prefix matches; wait for more.
			return nil
		} else {
			p.strippedDCS = true
		}
	}

	var events []Event
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}
		line := p.buf[:idx]
		p.buf = p.buf[idx+1:]

		line = bytes.TrimSuffix(line, []byte("\r"))
		// A trailing DCS terminator (ESC \) can be appended directly after
		// the last notification with no newline in between in some cases;
		// strip it defensively if present at the end of a line.
		line = bytes.TrimSuffix(line, dcsTerminator)

		if evt, ok := p.processLine(line); ok {
			events = append(events, evt)
		}
	}
	return events
}

func (p *Parser) processLine(line []byte) (Event, bool) {
	s := string(line)

	if p.inBlock {
		if strings.HasPrefix(s, "%end ") || s == "%end" {
			p.finishBlock(s, false)
			evt := Event{Result: &p.block}
			p.inBlock = false
			return evt, true
		}
		if strings.HasPrefix(s, "%error ") || s == "%error" {
			p.finishBlock(s, true)
			evt := Event{Result: &p.block}
			p.inBlock = false
			return evt, true
		}
		p.block.Lines = append(p.block.Lines, s)
		return Event{}, false
	}

	if strings.HasPrefix(s, "%begin ") || s == "%begin" {
		p.block = CommandResult{}
		fields := strings.Fields(s)
		if len(fields) >= 4 {
			p.block.Timestamp, _ = strconv.ParseInt(fields[1], 10, 64)
			p.block.CommandNumber, _ = strconv.Atoi(fields[2])
			p.block.Flags, _ = strconv.Atoi(fields[3])
		}
		p.inBlock = true
		return Event{}, false
	}

	if !strings.HasPrefix(s, "%") {
		// Not a notification or block marker; ignore (e.g. stray blank
		// lines, or command echo if a caller mistakenly used -C not -CC).
		return Event{}, false
	}

	notif := parseNotification(s)
	return Event{Notification: &notif}, true
}

func (p *Parser) finishBlock(line string, isError bool) {
	fields := strings.Fields(line)
	if len(fields) >= 4 {
		p.block.Timestamp, _ = strconv.ParseInt(fields[1], 10, 64)
		p.block.CommandNumber, _ = strconv.Atoi(fields[2])
		p.block.Flags, _ = strconv.Atoi(fields[3])
	}
	p.block.IsError = isError
}

// parseNotification parses a single notification line (starting with '%',
// no trailing newline).
func parseNotification(line string) Notification {
	rest := strings.TrimPrefix(line, "%")
	typ, args, found := strings.Cut(rest, " ")
	if !found {
		args = ""
	}

	n := Notification{Raw: line}

	switch NotificationType(typ) {
	case NotifOutput:
		n.Type = NotifOutput
		paneID, value := splitFirstField(args)
		n.PaneID = paneID
		n.Value = decodeOctalEscapes(value)
	case NotifExtendedOutput:
		n.Type = NotifExtendedOutput
		paneID, remainder := splitFirstField(args)
		n.PaneID = paneID
		// remainder is "age ... : value"; value is everything after the
		// last " : " marker.
		if idx := strings.LastIndex(remainder, ": "); idx >= 0 {
			n.Value = decodeOctalEscapes(remainder[idx+2:])
		}
	case NotifSessionChanged:
		n.Type = NotifSessionChanged
		fields := strings.Fields(args)
		if len(fields) >= 2 {
			n.Name = fields[1]
		}
	case NotifSessionRenamed:
		// Despite tmux(1)'s abbreviated synopsis ("%session-renamed name"),
		// the actual notification carries a leading session-id field, e.g.
		// "%session-renamed $0 renamed-externally" (confirmed against tmux
		// 3.7b), matching the shape of %session-changed.
		n.Type = NotifSessionRenamed
		_, name := splitFirstField(args)
		n.Name = name
	case NotifSessionWindowChanged:
		n.Type = NotifSessionWindowChanged
		fields := strings.Fields(args)
		if len(fields) >= 2 {
			n.WindowID = fields[1]
		}
	case NotifSessionsChanged:
		n.Type = NotifSessionsChanged
	case NotifLayoutChange:
		n.Type = NotifLayoutChange
		fields := strings.Fields(args)
		if len(fields) >= 1 {
			n.WindowID = fields[0]
		}
		if len(fields) >= 2 {
			n.Layout = fields[1]
		}
		if len(fields) >= 3 {
			n.VisibleLayout = fields[2]
		}
	case NotifWindowAdd:
		n.Type = NotifWindowAdd
		n.WindowID = args
	case NotifWindowClose:
		n.Type = NotifWindowClose
		n.WindowID = args
	case NotifWindowRenamed:
		n.Type = NotifWindowRenamed
		id, name := splitFirstField(args)
		n.WindowID = id
		n.Name = name
	case NotifWindowPaneChanged:
		n.Type = NotifWindowPaneChanged
		fields := strings.Fields(args)
		if len(fields) >= 1 {
			n.WindowID = fields[0]
		}
		if len(fields) >= 2 {
			n.PaneID = fields[1]
		}
	case NotifUnlinkedWindowAdd:
		n.Type = NotifUnlinkedWindowAdd
		n.WindowID = args
	case NotifUnlinkedWindowClose:
		n.Type = NotifUnlinkedWindowClose
		n.WindowID = args
	case NotifUnlinkedWindowRenamed:
		n.Type = NotifUnlinkedWindowRenamed
		id, name := splitFirstField(args)
		n.WindowID = id
		n.Name = name
	case NotifClientDetached:
		n.Type = NotifClientDetached
	case NotifPause:
		n.Type = NotifPause
		n.PaneID = args
	case NotifContinue:
		n.Type = NotifContinue
		n.PaneID = args
	case NotifPaneModeChanged:
		n.Type = NotifPaneModeChanged
		n.PaneID = args
	case NotifExit:
		n.Type = NotifExit
		n.Reason = args
	default:
		n.Type = NotifUnknown
	}

	return n
}

// splitFirstField splits s on its first space into (field, remainder). If
// there is no space, remainder is empty.
func splitFirstField(s string) (string, string) {
	field, remainder, _ := strings.Cut(s, " ")
	return field, remainder
}

// decodeOctalEscapes decodes tmux's control-mode escaping of %output
// values: a literal backslash followed by exactly three octal digits
// represents that single byte value (this is how tmux escapes both
// non-printable bytes and literal backslashes). Any other byte, including
// raw multi-byte UTF-8 sequences that tmux considers printable, passes
// through unchanged.
func decodeOctalEscapes(s string) []byte {
	out := make([]byte, 0, len(s))
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+3 < len(b) && isOctalDigit(b[i+1]) && isOctalDigit(b[i+2]) && isOctalDigit(b[i+3]) {
			v := (b[i+1]-'0')*64 + (b[i+2]-'0')*8 + (b[i+3] - '0')
			out = append(out, v)
			i += 3
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func isOctalDigit(c byte) bool {
	return c >= '0' && c <= '7'
}
