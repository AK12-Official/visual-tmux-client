package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
)

func TestLocalResolutionNeverFallsBack(t *testing.T) {
	r := host.NewLocalResolver(nil, nil, nil)
	for _, id := range []host.ID{"", "unknown", "LOCAL"} {
		if _, err := r.Resolve(context.Background(), id); !errors.Is(err, host.ErrNotFound) {
			t.Fatalf("unknown identity %q: %v", id, err)
		}
	}
	first, err := r.Resolve(context.Background(), host.LocalID)
	if err != nil || first.Target.HostID != host.LocalID || first.Target.Revision != host.LocalRevision {
		t.Fatalf("local runtime: %+v, %v", first, err)
	}
	first.Target.HostID = "changed"
	second, err := r.Resolve(context.Background(), host.LocalID)
	if err != nil || second.Target.HostID != host.LocalID {
		t.Fatal("caller changed resolver identity")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Resolve(ctx, host.LocalID); !errors.Is(err, context.Canceled) {
		t.Fatal("resolution ignored cancellation")
	}
}
