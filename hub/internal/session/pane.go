package session

// Pane is a single terminal multiplexer pane, used to summarise what a session
// is currently doing.
type Pane struct {
	Session        string
	WindowIndex    int
	PaneIndex      int
	WindowActive   bool
	PaneActive     bool
	Dead           bool
	WindowName     string
	Title          string
	CurrentCommand string
}

// PaneSummary describes the most representative pane of a session.
type PaneSummary struct {
	WindowName     string `json:"window_name"`
	Title          string `json:"title"`
	CurrentCommand string `json:"current_command"`
	WindowActive   bool   `json:"window_active"`
}

// Ranks used to pick a session's representative pane. A pane with a lower rank
// never displaces one with a higher rank, and an excluded pane is never chosen.
const (
	paneRankExcluded         = -1
	paneRankOther            = 0
	paneRankActiveWindow     = 1
	paneRankActivePane       = 2
	paneRankActiveWindowPane = 3
)

// maxPaneFieldRunes caps each free-form pane field. A pane title is set by
// whatever program runs in the pane, so it is unbounded input from outside the
// hub: tmux will happily store and report a title of hundreds of kilobytes.
// Without a cap, one pane could make every session-list poll carry that data,
// in every response, for every client. The sidebar renders one ellipsised line,
// so nothing visible is lost.
const maxPaneFieldRunes = 200

// paneChoice is the ordering key for selecting a session's representative pane.
type paneChoice struct {
	rank        int
	windowIndex int
	paneIndex   int
}

// better reports whether candidate should displace incumbent.
//
// Rank decides first. Equal ranks are reachable -- every pane of the active
// window except the active one ranks the same, as does the active pane of every
// other window -- and resolving them by index keeps the result a function of
// server state rather than of the order in which tmux happens to emit panes.
// The top rank is not one of these: each session has exactly one active pane
// in its active window.
func better(candidate, incumbent paneChoice) bool {
	if candidate.rank != incumbent.rank {
		return candidate.rank > incumbent.rank
	}
	if candidate.windowIndex != incumbent.windowIndex {
		return candidate.windowIndex < incumbent.windowIndex
	}
	return candidate.paneIndex < incumbent.paneIndex
}

// clampField bounds a free-form pane field to maxPaneFieldRunes.
func clampField(value string) string {
	runes := []rune(value)
	if len(runes) <= maxPaneFieldRunes {
		return value
	}
	return string(runes[:maxPaneFieldRunes])
}

// paneRank orders panes by how well each represents its session. A pane that has
// exited ranks as excluded, because it no longer describes anything.
func paneRank(p Pane) int {
	if p.Dead {
		return paneRankExcluded
	}
	switch {
	case p.WindowActive && p.PaneActive:
		return paneRankActiveWindowPane
	case p.PaneActive:
		return paneRankActivePane
	case p.WindowActive:
		return paneRankActiveWindow
	default:
		return paneRankOther
	}
}

// SelectPaneSummaries picks the most representative pane for each session name.
// Sessions with no live pane are omitted; callers treat absence as "nothing to show"
// rather than as an error.
func SelectPaneSummaries(panes []Pane) map[string]PaneSummary {
	best := make(map[string]PaneSummary)
	chosen := make(map[string]paneChoice)
	for _, p := range panes {
		rank := paneRank(p)
		if rank == paneRankExcluded {
			continue
		}
		candidate := paneChoice{rank: rank, windowIndex: p.WindowIndex, paneIndex: p.PaneIndex}
		if incumbent, ok := chosen[p.Session]; ok && !better(candidate, incumbent) {
			continue
		}
		chosen[p.Session] = candidate
		best[p.Session] = PaneSummary{
			WindowName:     clampField(p.WindowName),
			Title:          clampField(p.Title),
			CurrentCommand: clampField(p.CurrentCommand),
			WindowActive:   p.WindowActive,
		}
	}
	return best
}
