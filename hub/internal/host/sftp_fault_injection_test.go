package host_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func TestSFTPOpenSSHReplacementAndFaultInjection(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	server := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User:           "sftpuser",
		Password:       "sftppass",
		TmuxSocketName: "fault-iso",
	})

	stateDir := t.TempDir()
	registry, err := host.NewRegistry(ctx, stateDir, filepath.Join(stateDir, "master.key"))
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	hostID := host.ID("sftp-test-host")
	_, err = registry.Save(ctx, host.HostInput{
		ID:             hostID,
		Name:           "SFTP Test Host",
		Address:        server.Addr,
		Port:           uint16(server.Port),
		Username:       server.User,
		CredentialType: "password",
		Credentials:    host.Credentials{Password: server.Password},
		HostKey:        server.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("save host: %v", err)
	}

	connMgr := host.NewConnectionManager(host.DefaultConnectionConfig(), registry)
	defer connMgr.Close()

	lease, err := connMgr.Lease(ctx, hostID)
	if err != nil {
		t.Fatalf("lease host: %v", err)
	}

	// 1. Environment & Shell Compatibility Probe
	controlClient, err := lease.GetControl(ctx)
	if err != nil {
		t.Fatalf("get control client: %v", err)
	}
	env, err := host.ProbeRemoteEnvironment(ctx, controlClient)
	if err != nil {
		t.Fatalf("probe remote environment: %v", err)
	}
	if env.OS == "" || env.Home == "" || env.TmuxVersion == "" {
		t.Fatalf("incomplete remote environment: %+v", env)
	}
	t.Logf("Supported server environment recorded: OS=%s, Shell=POSIX (sh), Tmux=%s", env.OS, env.TmuxVersion)

	// 2. Real OpenSSH SFTP atomic replacement test
	fileSvc := host.NewRemoteFileService(lease, 1024*1024, 100, t.TempDir())
	targetPath := filepath.Join(server.RootDir, "atomic_replace.txt")
	_ = os.WriteFile(targetPath, []byte("initial version"), 0644)

	readRes, err := fileSvc.Read(ctx, targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	_ = readRes.File.Close()

	precondition := &files.ExpectedMtime{
		Millis: readRes.Mtime,
		Nanos:  &readRes.MtimeNanos,
	}

	writeRes, err := fileSvc.Write(ctx, targetPath, bytes.NewReader([]byte("replaced version")), 16, precondition, true)
	if err != nil {
		t.Fatalf("atomic replacement failed: %v", err)
	}
	if writeRes.Mtime == 0 {
		t.Fatal("expected non-zero write mtime after atomic replace")
	}

	replacedData, _ := os.ReadFile(targetPath)
	if string(replacedData) != "replaced version" {
		t.Fatalf("expected 'replaced version', got %q", string(replacedData))
	}

	// 3. Fault injection: Unsupported atomic replacement
	// Override SFTP adapter to simulate missing posix-rename@openssh.com extension
	fileSvc.SetAdapterFactoryForTesting(func(ctx context.Context, sshClient *ssh.Client) (*host.SFTPAdapter, error) {
		a, err := host.NewSFTPAdapter(ctx, sshClient)
		if err != nil {
			return nil, err
		}
		a.SetExtensionForTesting(host.PosixRenameExtension, "", false)
		return a, nil
	})

	unsupportedTarget := filepath.Join(server.RootDir, "preserve_me.txt")
	_ = os.WriteFile(unsupportedTarget, []byte("vital pristine content"), 0644)

	_, err = fileSvc.Write(ctx, unsupportedTarget, bytes.NewReader([]byte("should fail")), 11, nil, true)
	if err == nil {
		t.Fatal("expected error on unsupported server atomic overwrite")
	}
	if !errors.Is(err, host.ErrUnsupported) || !errors.Is(err, files.ErrWriteFailed) {
		t.Fatalf("expected ErrWriteFailed wrapping ErrUnsupported, got: %v", err)
	}

	// Verify original file was preserved intact (never truncated or deleted)
	vitalData, _ := os.ReadFile(unsupportedTarget)
	if string(vitalData) != "vital pristine content" {
		t.Fatalf("vital file modified on unsupported server: %q", string(vitalData))
	}

	// Verify creating a new file on unsupported server still works using non-overwriting rename
	newFileOnUnsupported := filepath.Join(server.RootDir, "new_file_unsupported.txt")
	_, err = fileSvc.Write(ctx, newFileOnUnsupported, bytes.NewReader([]byte("brand new")), 9, nil, true)
	if err != nil {
		t.Fatalf("creating brand new file on unsupported server should succeed: %v", err)
	}
	brandNewData, _ := os.ReadFile(newFileOnUnsupported)
	if string(brandNewData) != "brand new" {
		t.Fatalf("unexpected new file content: %q", string(brandNewData))
	}

	// Reset adapter factory
	fileSvc.SetAdapterFactoryForTesting(nil)

	// 4. Fault injection around commit: Connection drop before replacement commit -> ErrOutcomeUnknown
	var capturedSSH *ssh.Client
	fileSvc.SetAdapterFactoryForTesting(func(ctx context.Context, sshClient *ssh.Client) (*host.SFTPAdapter, error) {
		capturedSSH = sshClient
		return host.NewSFTPAdapter(ctx, sshClient)
	})
	fileSvc.SetBeforePosixRenameHookForTesting(func() {
		if capturedSSH != nil {
			_ = capturedSSH.Close()
		}
	})

	commitDropTarget := filepath.Join(server.RootDir, "drop_at_commit.txt")
	_ = os.WriteFile(commitDropTarget, []byte("base data"), 0644)

	_, err = fileSvc.Write(ctx, commitDropTarget, bytes.NewReader([]byte("commit drop data")), 16, nil, true)
	if err == nil {
		t.Fatal("expected error on disconnect before commit")
	}
	if !errors.Is(err, host.ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown on disconnect before commit, got: %v", err)
	}

	// Reset hooks
	fileSvc.SetAdapterFactoryForTesting(nil)
	fileSvc.SetBeforePosixRenameHookForTesting(nil)

	// 5. Coarse Metadata and same-time content conflict detection
	// Simulate an observation where subsecond nanoseconds are not available (Nanos: nil, Millis only)
	coarseTarget := filepath.Join(server.RootDir, "coarse_file.txt")
	_ = os.WriteFile(coarseTarget, []byte("coarse origin text"), 0644)

	coarseStat, _ := os.Stat(coarseTarget)
	coarsePrecondition := &files.ExpectedMtime{
		Millis: coarseStat.ModTime().UnixMilli(),
		Nanos:  nil, // Coarse timestamp
	}

	// Content modified on disk concurrently while timestamp is artificially kept at the same millis
	_ = os.WriteFile(coarseTarget, []byte("concurrent change!"), 0644)
	_ = os.Chtimes(coarseTarget, coarseStat.ModTime(), coarseStat.ModTime())

	// Write should detect same-mtime content modification via digest comparison and reject with ErrConflict
	_, err = fileSvc.Write(ctx, coarseTarget, bytes.NewReader([]byte("my new text")), 11, coarsePrecondition, true)
	if !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict when content changed under coarse metadata, got: %v", err)
	}

	// 6. Capabilities check matches actual server behavior
	caps, err := connMgr.CheckCapabilities(ctx, hostID)
	if err != nil {
		t.Fatalf("CheckCapabilities failed: %v", err)
	}
	if !caps.Files.AtomicReplacement {
		t.Fatalf("expected real OpenSSH server to support atomic replacement, got: %+v", caps.Files)
	}
}
