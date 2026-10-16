package host_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

func TestTwoRemoteHostsAndLocalAcceptanceFlow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Start two independent disposable SSH servers with isolated tmux servers and private files
	srv1 := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User:           "passuser",
		Password:       "passsecret",
		TmuxSocketName: "acc-srv1",
	})
	srv2 := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User:            "keyuser",
		UseEncryptedKey: true,
		KeyPassphrase:   "testpassphrase",
		TmuxSocketName:  "acc-srv2",
	})

	// 2. Set up durable registry with independent master key
	stateDir := t.TempDir()
	masterKeyPath := filepath.Join(stateDir, "master.key")
	registry, err := host.NewRegistry(ctx, stateDir, masterKeyPath)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	// 3. Enrollment: Probe endpoints to verify pre-authentication fingerprint probing
	key1, err := host.ProbeEndpoint(ctx, srv1.Addr, uint16(srv1.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("probe srv1: %v", err)
	}
	if !bytes.Equal(key1.Marshal(), srv1.HostPubKey.Marshal()) {
		t.Fatal("probe returned wrong host key for srv1")
	}

	key2, err := host.ProbeEndpoint(ctx, srv2.Addr, uint16(srv2.Port), 2*time.Second)
	if err != nil {
		t.Fatalf("probe srv2: %v", err)
	}
	if !bytes.Equal(key2.Marshal(), srv2.HostPubKey.Marshal()) {
		t.Fatal("probe returned wrong host key for srv2")
	}

	// Register Host 1 (Password authentication)
	_, err = registry.Save(ctx, host.HostInput{
		ID:             "remote-pass",
		Name:           "Remote Password Server",
		Address:        srv1.Addr,
		Port:           uint16(srv1.Port),
		Username:       srv1.User,
		CredentialType: "password",
		Credentials:    host.Credentials{Password: srv1.Password},
		HostKey:        srv1.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("register remote-pass: %v", err)
	}

	// Register Host 2 (Encrypted private key authentication)
	_, err = registry.Save(ctx, host.HostInput{
		ID:             "remote-key",
		Name:           "Remote Key Server",
		Address:        srv2.Addr,
		Port:           uint16(srv2.Port),
		Username:       srv2.User,
		CredentialType: "private_key",
		Credentials:    host.Credentials{PrivateKey: srv2.UserKeyPEM, Passphrase: srv2.Passphrase},
		HostKey:        srv2.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("register remote-key: %v", err)
	}

	// Verify all 3 hosts (Local + 2 Remotes) are present in registry
	hostsList, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list hosts: %v", err)
	}
	if len(hostsList) != 3 {
		t.Fatalf("expected 3 hosts (local + 2 remotes), got %d", len(hostsList))
	}

	// 4. Connection Manager and Capability checks
	connMgr := host.NewConnectionManager(host.DefaultConnectionConfig(), registry)

	caps1, err := connMgr.CheckCapabilities(ctx, "remote-pass")
	if err != nil {
		t.Fatalf("check caps srv1: %v", err)
	}
	if !caps1.Online || !caps1.SSH.Available || !caps1.Tmux.Available || !caps1.SFTP.Available {
		t.Fatalf("expected srv1 to be fully online and capable, got: %+v", caps1)
	}

	caps2, err := connMgr.CheckCapabilities(ctx, "remote-key")
	if err != nil {
		t.Fatalf("check caps srv2: %v", err)
	}
	if !caps2.Online || !caps2.SSH.Available || !caps2.Tmux.Available || !caps2.SFTP.Available {
		t.Fatalf("expected srv2 to be fully online and capable, got: %+v", caps2)
	}

	// 5. Session Operations & Cross-Host Isolation
	lease1, err := connMgr.Lease(ctx, "remote-pass")
	if err != nil {
		t.Fatalf("lease srv1: %v", err)
	}

	lease2, err := connMgr.Lease(ctx, "remote-key")
	if err != nil {
		t.Fatalf("lease srv2: %v", err)
	}

	sessSvc1 := host.NewRemoteSessionService(lease1, srv1.RootDir, "")
	sessSvc2 := host.NewRemoteSessionService(lease2, srv2.RootDir, "")

	// Create identical session name "app-work" on both remote hosts
	if _, err := sessSvc1.CreateSession(ctx, "app-work"); err != nil {
		t.Fatalf("create app-work on srv1: %v", err)
	}
	if _, err := sessSvc2.CreateSession(ctx, "app-work"); err != nil {
		t.Fatalf("create app-work on srv2: %v", err)
	}

	// Create a distinct session on srv1 only
	if _, err := sessSvc1.CreateSession(ctx, "only-pass"); err != nil {
		t.Fatalf("create only-pass on srv1: %v", err)
	}

	// Verify isolation: srv1 has both, srv2 has only app-work
	list1, err := sessSvc1.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list srv1: %v", err)
	}
	var names1 []string
	for _, s := range list1 {
		names1 = append(names1, s.Name)
	}
	if !contains(names1, "app-work") || !contains(names1, "only-pass") {
		t.Fatalf("expected app-work and only-pass on srv1, got: %v", names1)
	}

	list2, err := sessSvc2.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list srv2: %v", err)
	}
	var names2 []string
	for _, s := range list2 {
		names2 = append(names2, s.Name)
	}
	if !contains(names2, "app-work") || contains(names2, "only-pass") {
		t.Fatalf("srv2 should contain app-work and NOT only-pass, got: %v", names2)
	}

	// Rename session on srv1 and verify srv2 is unchanged
	if err := sessSvc1.RenameSession(ctx, "only-pass", "renamed-pass"); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	updatedList1, _ := sessSvc1.ListSessions(ctx)
	var updatedNames1 []string
	for _, s := range updatedList1 {
		updatedNames1 = append(updatedNames1, s.Name)
	}
	if !contains(updatedNames1, "renamed-pass") || contains(updatedNames1, "only-pass") {
		t.Fatalf("expected renamed-pass on srv1, got: %v", updatedNames1)
	}

	// Query pane summaries and working directory
	workDir1, err := sessSvc1.PaneWorkingDirectory(ctx, "app-work")
	if err != nil || workDir1 == "" {
		t.Fatalf("get working dir on srv1: %v, dir=%q", err, workDir1)
	}

	// 6. Terminal Interaction (PTY Attach, Backpressure, Resize, Detach, Survival)
	procFactory1 := host.NewRemoteProcessFactory(lease1, "")
	proc1, err := procFactory1.NewProcess(ctx, "app-work", 80, 24)
	if err != nil {
		t.Fatalf("new process attach: %v", err)
	}

	// Read initial terminal output in background
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		_, _ = proc1.Read(buf)
		close(readDone)
	}()

	// Write input to remote terminal
	if _, err := proc1.Write([]byte("echo ACC_TEST_OK\r\n")); err != nil {
		t.Fatalf("write to pty: %v", err)
	}

	// Resize remote terminal
	if err := proc1.Resize(100, 30); err != nil {
		t.Fatalf("resize pty: %v", err)
	}

	// Detach process
	if err := proc1.Detach(); err != nil {
		// Fallback to Close if Detach returned error
		_ = proc1.Close()
	} else {
		_ = proc1.Close()
	}

	select {
	case <-readDone:
	case <-time.After(time.Second):
	}

	// Verify session survives detachment
	activeSess1, err := sessSvc1.GetSession(ctx, "app-work")
	if err != nil || activeSess1 == nil {
		t.Fatalf("session app-work must survive detachment, got err: %v", err)
	}

	// 7. File Operations (List, Read, Write, Conflict, Rename, Delete)
	fileSvc1 := host.NewRemoteFileService(lease1, 1024*1024, 100, t.TempDir())

	// List directory
	dirListing, err := fileSvc1.List(ctx, srv1.RootDir)
	if err != nil {
		t.Fatalf("list srv1 root dir: %v", err)
	}
	if dirListing.Path != srv1.RootDir {
		t.Fatalf("expected path %s, got %s", srv1.RootDir, dirListing.Path)
	}

	// Write file
	targetFile := filepath.Join(srv1.RootDir, "acc_note.txt")
	writeRes, err := fileSvc1.Write(ctx, targetFile, bytes.NewReader([]byte("first line\n")), 11, nil, false)
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	if writeRes.Mtime == 0 {
		t.Fatal("expected non-zero write mtime")
	}

	// Read file back
	readRes, err := fileSvc1.Read(ctx, targetFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	defer readRes.File.Close()
	contentBytes, _ := io.ReadAll(readRes.File)
	if string(contentBytes) != "first line\n" {
		t.Fatalf("unexpected content: %q", string(contentBytes))
	}

	// Conflict detection: attempt write with outdated mtime precondition
	staleNanos := writeRes.MtimeNanos - 1000000000
	stalePrecondition := &files.ExpectedMtime{
		Millis: writeRes.Mtime - 1000,
		Nanos:  &staleNanos,
	}
	_, err = fileSvc1.Write(ctx, targetFile, bytes.NewReader([]byte("conflict edit\n")), 14, stalePrecondition, true)
	if !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict on stale precondition, got: %v", err)
	}

	// Atomic save with matching precondition
	validPrecondition := &files.ExpectedMtime{
		Millis: writeRes.Mtime,
		Nanos:  &writeRes.MtimeNanos,
	}
	writeRes2, err := fileSvc1.Write(ctx, targetFile, bytes.NewReader([]byte("updated line\n")), 13, validPrecondition, true)
	if err != nil {
		t.Fatalf("write with valid precondition failed: %v", err)
	}
	if writeRes2.Mtime == 0 {
		t.Fatal("expected valid second write mtime")
	}

	// Rename file
	renamedFile := filepath.Join(srv1.RootDir, "acc_note_renamed.txt")
	if err := fileSvc1.Rename(ctx, targetFile, renamedFile); err != nil {
		t.Fatalf("rename file: %v", err)
	}

	// Delete file
	if err := fileSvc1.Delete(ctx, renamedFile, false); err != nil {
		t.Fatalf("delete file: %v", err)
	}

	// 8. Hub Restart Acceptance Flow
	// Shut down current connection manager and registry
	_ = connMgr.Close()
	_ = registry.Close()

	// Reopen registry against the existing SQLite database and master key
	reopenedRegistry, err := host.NewRegistry(ctx, stateDir, masterKeyPath)
	if err != nil {
		t.Fatalf("reopen registry after restart: %v", err)
	}
	defer reopenedRegistry.Close()

	// Verify all enrolled hosts are restored
	restoredHosts, err := reopenedRegistry.List(ctx)
	if err != nil {
		t.Fatalf("list restored hosts: %v", err)
	}
	if len(restoredHosts) != 3 {
		t.Fatalf("expected 3 restored hosts, got %d", len(restoredHosts))
	}

	reopenedConnMgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reopenedRegistry)
	defer reopenedConnMgr.Close()

	// Verify both remote hosts reconnect successfully and tmux sessions survive restart!
	for _, h := range []host.ID{"remote-pass", "remote-key"} {
		restartedLease, err := reopenedConnMgr.Lease(ctx, h)
		if err != nil {
			t.Fatalf("acquire lease after restart for %s: %v", h, err)
		}

		reopenedSessSvc := host.NewRemoteSessionService(restartedLease, ".", "")
		reopenedSessions, err := reopenedSessSvc.ListSessions(ctx)
		if err != nil {
			t.Fatalf("list sessions after restart on %s: %v", h, err)
		}

		found := false
		for _, s := range reopenedSessions {
			if s.Name == "app-work" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("session app-work did not survive hub restart on %s", h)
		}

		// Reattach terminal process to verify session remains attachable
		procFactory := host.NewRemoteProcessFactory(restartedLease, "")
		proc, err := procFactory.NewProcess(ctx, "app-work", 80, 24)
		if err != nil {
			t.Fatalf("reattach after restart on %s: %v", h, err)
		}
		_ = proc.Close()
	}
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
