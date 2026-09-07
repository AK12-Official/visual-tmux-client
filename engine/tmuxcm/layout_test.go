package tmuxcm

import (
	"reflect"
	"testing"
)

func TestParseLayoutPaneIDs_SinglePane(t *testing.T) {
	got := ParseLayoutPaneIDs("b25d,80x24,0,0,0")
	want := []string{"%0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestParseLayoutPaneIDs_VerticalSplit(t *testing.T) {
	// From the parser_test.go fixture: two panes, %0 and %3.
	got := ParseLayoutPaneIDs("c196,80x24,0,0[80x12,0,0,0,80x11,0,13,3]")
	want := []string{"%0", "%3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestParseLayoutPaneIDs_NestedSplit(t *testing.T) {
	// A horizontal split inside a vertical split: three panes, %0, %1, %2.
	got := ParseLayoutPaneIDs("a1b2,102x55,0,0{51x55,0,0,0,50x55,52,0[50x27,52,0,1,50x27,52,28,2]}")
	want := []string{"%0", "%1", "%2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestParseLayoutPaneIDs_EmptyString(t *testing.T) {
	got := ParseLayoutPaneIDs("")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseLayout_SinglePane(t *testing.T) {
	got := ParseLayout("b25d,80x24,0,0,0")
	want := &LayoutNode{X: 0, Y: 0, Width: 80, Height: 24, PaneID: "%0"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}

func TestParseLayout_VerticalSplit(t *testing.T) {
	// "[...]" is a vertical split: children stacked top-to-bottom.
	got := ParseLayout("c196,80x24,0,0[80x12,0,0,0,80x11,0,13,3]")
	want := &LayoutNode{
		X: 0, Y: 0, Width: 80, Height: 24,
		Orientation: OrientationVertical,
		Children: []*LayoutNode{
			{X: 0, Y: 0, Width: 80, Height: 12, PaneID: "%0"},
			{X: 0, Y: 13, Width: 80, Height: 11, PaneID: "%3"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}

func TestParseLayout_NestedSplit(t *testing.T) {
	// A horizontal split ("{...}") inside a vertical split ("[...]").
	got := ParseLayout("a1b2,102x55,0,0{51x55,0,0,0,50x55,52,0[50x27,52,0,1,50x27,52,28,2]}")
	want := &LayoutNode{
		X: 0, Y: 0, Width: 102, Height: 55,
		Orientation: OrientationHorizontal,
		Children: []*LayoutNode{
			{X: 0, Y: 0, Width: 51, Height: 55, PaneID: "%0"},
			{
				X: 52, Y: 0, Width: 50, Height: 55,
				Orientation: OrientationVertical,
				Children: []*LayoutNode{
					{X: 52, Y: 0, Width: 50, Height: 27, PaneID: "%1"},
					{X: 52, Y: 28, Width: 50, Height: 27, PaneID: "%2"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %+v, got %+v", want, got)
	}
}

func TestParseLayout_EmptyString(t *testing.T) {
	got := ParseLayout("")
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
