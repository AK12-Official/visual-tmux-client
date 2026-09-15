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
	ranks := make(map[string]int)
	for _, p := range panes {
		rank := paneRank(p)
		if rank == paneRankExcluded {
			continue
		}
		if current, ok := ranks[p.Session]; ok && current >= rank {
			continue
		}
		ranks[p.Session] = rank
		best[p.Session] = PaneSummary{
			WindowName:     p.WindowName,
			Title:          p.Title,
			CurrentCommand: p.CurrentCommand,
			WindowActive:   p.WindowActive,
		}
	}
	return best
}
