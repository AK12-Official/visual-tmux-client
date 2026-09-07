package tmuxcm

import (
	"strconv"
	"strings"
)

// Orientation identifies how a LayoutNode's Children are arranged.
type Orientation int

const (
	// OrientationNone marks a leaf node (a single pane, no children).
	OrientationNone Orientation = iota
	// OrientationVertical marks a node whose children are stacked
	// top-to-bottom ("[...]" in tmux's layout grammar).
	OrientationVertical
	// OrientationHorizontal marks a node whose children sit side-by-side
	// ("{...}" in tmux's layout grammar).
	OrientationHorizontal
)

// LayoutNode is one node of a parsed tmux window-layout tree: either a leaf
// (PaneID set, no Children) or a split (Orientation and Children set, no
// PaneID). X/Y/Width/Height are in terminal cells, matching tmux's own
// layout coordinates.
type LayoutNode struct {
	X, Y, Width, Height int

	// PaneID is set (e.g. "%3") for a leaf node, empty for a split node.
	PaneID string

	// Orientation and Children are set for a split node, unset for a leaf.
	Orientation Orientation
	Children    []*LayoutNode
}

// ParseLayout parses a tmux window-layout string (e.g. the Layout field of a
// %layout-change notification or the #{window_layout} format variable) into
// its full geometry tree. Returns nil if layout is empty or malformed enough
// that no checksum separator is found.
//
// Grammar (from tmux's layout_dump/layout_parse): a layout string is
// "checksum,node", where a node is "WxH,x,y" followed by either ",pane_id"
// (a leaf pane) or a bracketed, comma-separated list of child nodes
// ("[...]" for a vertical split, "{...}" for horizontal split).
func ParseLayout(layout string) *LayoutNode {
	_, rest, found := strings.Cut(layout, ",")
	if !found {
		return nil
	}
	node, _ := parseLayoutTreeNode(rest, 0)
	return node
}

// ParseLayoutPaneIDs extracts the set of pane IDs (as "%N" strings) present
// in a tmux window-layout string, in the order they appear (left-to-right,
// depth-first). There is no direct control-mode notification for pane death
// (see spike-notes.md); the engine diffs this pane-ID set across successive
// %layout-change events to infer it.
func ParseLayoutPaneIDs(layout string) []string {
	root := ParseLayout(layout)
	if root == nil {
		return nil
	}
	var ids []string
	collectPaneIDs(root, &ids)
	return ids
}

func collectPaneIDs(n *LayoutNode, ids *[]string) {
	if n.PaneID != "" {
		*ids = append(*ids, n.PaneID)
		return
	}
	for _, c := range n.Children {
		collectPaneIDs(c, ids)
	}
}

// parseLayoutTreeNode parses a single node starting at pos: its "WxH,x,y"
// geometry, then either a leaf pane ID or a bracketed list of children. It
// returns the built node and the index just past it (before a following
// ',', ']', or '}', or at len(s) if the node runs to the end of the string).
func parseLayoutTreeNode(s string, pos int) (*LayoutNode, int) {
	wStart := pos
	pos = skipField(s, pos)
	w, h := parseWxH(s[wStart:pos])
	if pos < len(s) && s[pos] == ',' {
		pos++
	}

	xStart := pos
	pos = skipField(s, pos)
	x, _ := strconv.Atoi(s[xStart:pos])
	if pos < len(s) && s[pos] == ',' {
		pos++
	}

	yStart := pos
	pos = skipField(s, pos)
	y, _ := strconv.Atoi(s[yStart:pos])

	node := &LayoutNode{X: x, Y: y, Width: w, Height: h}

	if pos >= len(s) {
		return node, pos
	}

	switch s[pos] {
	case ',':
		// Leaf: ",pane_id"
		pos++
		start := pos
		for pos < len(s) && isDigit(s[pos]) {
			pos++
		}
		if id, err := strconv.Atoi(s[start:pos]); err == nil {
			node.PaneID = "%" + strconv.Itoa(id)
		}
		return node, pos
	case '[', '{':
		closing := byte(']')
		node.Orientation = OrientationVertical
		if s[pos] == '{' {
			closing = '}'
			node.Orientation = OrientationHorizontal
		}
		pos++
		for pos < len(s) && s[pos] != closing {
			var child *LayoutNode
			child, pos = parseLayoutTreeNode(s, pos)
			node.Children = append(node.Children, child)
			if pos < len(s) && s[pos] == ',' {
				pos++
			}
		}
		if pos < len(s) && s[pos] == closing {
			pos++
		}
		return node, pos
	default:
		return node, pos
	}
}

// parseWxH parses a "WxH" field (e.g. "80x24") into its width and height.
func parseWxH(s string) (int, int) {
	wStr, hStr, found := strings.Cut(s, "x")
	if !found {
		return 0, 0
	}
	w, _ := strconv.Atoi(wStr)
	h, _ := strconv.Atoi(hStr)
	return w, h
}

// skipField advances pos past a single comma-delimited field (e.g. "80x24"),
// stopping at the next ',', ']', '}', or end of string.
func skipField(s string, pos int) int {
	for pos < len(s) && s[pos] != ',' && s[pos] != ']' && s[pos] != '}' && s[pos] != '[' && s[pos] != '{' {
		pos++
	}
	return pos
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
