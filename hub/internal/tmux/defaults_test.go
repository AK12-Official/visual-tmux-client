package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSessionPreservationDefaults(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	if _, err := c.Create(ctx, "preserved"); err != nil {
		t.Fatal(err)
	}
	for _, option := range []struct{ scope, name, want string }{
		{"-sv", "exit-unattached", "off"},
		{"-gv", "destroy-unattached", "off"},
		{"-gwv", "remain-on-exit", "failed"},
		{"-sv", "exit-empty", "on"},
	} {
		out, stderr, code, err := c.Exec(ctx, "show-options", option.scope, option.name)
		if err != nil || code != 0 || strings.TrimSpace(out) != option.want {
			t.Fatalf("%s=%q, want %q: code=%d err=%v stderr=%s", option.name, out, option.want, code, err, stderr)
		}
	}
	// Failed programs retain their pane and exit status for inspection.
	_, stderr, code, err := c.Exec(ctx, "respawn-pane", "-k", "-t", "=preserved:", "exit 7")
	if err != nil || code != 0 {
		t.Fatalf("respawn: code=%d err=%v stderr=%s", code, err, stderr)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, stderr, code, err := c.Exec(ctx, "list-panes", "-t", "=preserved:",
			"-F", "#{pane_dead}:#{pane_dead_status}")
		if err != nil || code != 0 {
			t.Fatalf("failed pane disappeared: code=%d err=%v stderr=%s", code, err, stderr)
		}
		if strings.TrimSpace(out) == "1:7" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed pane was not preserved with exit status: %q", out)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A successful exit still closes the pane and its last session normally.
	_, stderr, code, err = c.Exec(ctx, "respawn-pane", "-t", "=preserved:", "exit 0")
	if err != nil || code != 0 {
		t.Fatalf("respawn: code=%d err=%v stderr=%s", code, err, stderr)
	}
	deadline = time.Now().Add(3 * time.Second)
	for c.HasSession(ctx, "preserved") {
		if time.Now().After(deadline) {
			t.Fatal("successful exit should close the last pane")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
