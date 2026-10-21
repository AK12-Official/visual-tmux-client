package host_test

import (
	"context"
	"errors"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
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

type stubSessions struct {
	host.Sessions
	name string
}

type stubFiles struct {
	host.Files
	name string
}

type stubProcesses struct {
	terminal.ProcessFactory
	name string
}

func TestMultiHostResolverTargetResolution(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	reg, err := host.NewRegistry(ctx, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reg.Close() }() //nolint:errcheck // cleanup

	connMgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer func() { _ = connMgr.Close() }() //nolint:errcheck // cleanup

	localSess := &stubSessions{name: "local-sessions"}
	localFiles := &stubFiles{name: "local-files"}
	localProcs := &stubProcesses{name: "local-procs"}

	localRuntime := host.Runtime{
		Target: host.Target{
			HostID:             host.LocalID,
			Revision:           host.LocalRevision,
			ConnectionRevision: host.LocalRevision,
		},
		Sessions:  localSess,
		Files:     localFiles,
		Processes: localProcs,
	}

	resolver := host.NewMultiHostResolver(localRuntime, reg, connMgr)

	// 1. Empty ID returns ErrNotFound
	if _, err := resolver.Resolve(ctx, ""); !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty ID, got: %v", err)
	}

	// 2. Unknown ID returns ErrNotFound
	if _, err := resolver.Resolve(ctx, "non-existent"); !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown ID, got: %v", err)
	}

	// 3. Registered local host resolves to local adapter
	resLocal, err := resolver.Resolve(ctx, host.LocalID)
	if err != nil {
		t.Fatalf("resolve local: %v", err)
	}
	if resLocal.Target.HostID != host.LocalID ||
		resLocal.Target.Revision != 1 ||
		resLocal.Target.ConnectionRevision != 1 {
		t.Fatalf("unexpected local target: %+v", resLocal.Target)
	}
	if resLocal.Sessions != localSess || resLocal.Files != localFiles || resLocal.Processes != localProcs {
		t.Fatal("local runtime did not use local adapters")
	}

	// 4. Delete local host: resolution of LocalID now fails
	if err := reg.Delete(ctx, host.LocalID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(ctx, host.LocalID); !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("deleted local host should not resolve: %v", err)
	}

	// 5. Register an arbitrary local-type ID (e.g. "my-workstation")
	customLocal, err := reg.Save(ctx, host.HostInput{
		ID:             "my-workstation",
		Name:           "My Workstation",
		ConnectionType: host.ConnectionTypeLocal,
	}, 0)
	if err != nil {
		t.Fatalf("save custom local: %v", err)
	}

	resCustom, err := resolver.Resolve(ctx, "my-workstation")
	if err != nil {
		t.Fatalf("resolve custom local: %v", err)
	}
	if resCustom.Target.HostID != "my-workstation" ||
		resCustom.Target.Revision != customLocal.Revision ||
		resCustom.Target.ConnectionRevision != customLocal.ConnectionRevision {
		t.Fatalf("unexpected custom local target: %+v", resCustom.Target)
	}
	if resCustom.Sessions != localSess || resCustom.Files != localFiles || resCustom.Processes != localProcs {
		t.Fatal("custom local did not use local adapters")
	}

	// 6. Register SSH host
	sshHost, err := reg.Save(ctx, host.HostInput{
		ID:             "remote-ssh",
		Name:           "Remote Box",
		ConnectionType: host.ConnectionTypeSSH,
		Address:        "127.0.0.1",
		Port:           22,
		Username:       "user",
	}, 0)
	if err != nil {
		t.Fatalf("save ssh host: %v", err)
	}

	resSSH, err := resolver.Resolve(ctx, "remote-ssh")
	if err != nil {
		t.Fatalf("resolve ssh host: %v", err)
	}
	if resSSH.Target.HostID != "remote-ssh" ||
		resSSH.Target.Revision != sshHost.Revision ||
		resSSH.Target.ConnectionRevision != sshHost.ConnectionRevision {
		t.Fatalf("unexpected ssh target: %+v", resSSH.Target)
	}
	// Verify it does NOT use local adapters
	if resSSH.Sessions == localSess || resSSH.Files == localFiles || resSSH.Processes == localProcs {
		t.Fatal("ssh runtime unexpectedly used local adapters")
	}

	// 7. Context cancellation aborts resolution
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := resolver.Resolve(cancelCtx, "my-workstation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
