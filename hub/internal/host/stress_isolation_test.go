package host_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

func TestSimultaneousTransferSlowTerminalAndOfflineHost(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Start two online disposable SSH servers
	srv1 := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User:           "user1",
		Password:       "pass1",
		TmuxSocketName: "stress-iso-1",
	})
	srv2 := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User:           "user2",
		Password:       "pass2",
		TmuxSocketName: "stress-iso-2",
	})

	stateDir := t.TempDir()
	registry, err := host.NewRegistry(ctx, stateDir, filepath.Join(stateDir, "master.key"))
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	// Register online host 1
	_, err = registry.Save(ctx, host.HostInput{
		ID:             "online-1",
		Name:           "Online 1",
		Address:        srv1.Addr,
		Port:           uint16(srv1.Port),
		Username:       srv1.User,
		CredentialType: "password",
		Credentials:    host.Credentials{Password: srv1.Password},
		HostKey:        srv1.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("save online-1: %v", err)
	}

	// Register online host 2
	_, err = registry.Save(ctx, host.HostInput{
		ID:             "online-2",
		Name:           "Online 2",
		Address:        srv2.Addr,
		Port:           uint16(srv2.Port),
		Username:       srv2.User,
		CredentialType: "password",
		Credentials:    host.Credentials{Password: srv2.Password},
		HostKey:        srv2.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("save online-2: %v", err)
	}

	// Register offline host pointing to closed loopback port
	_, err = registry.Save(ctx, host.HostInput{
		ID:             "offline-host",
		Name:           "Offline Host",
		Address:        "127.0.0.1",
		Port:           1, // Unreachable port
		Username:       "fake",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "fake"},
		HostKey:        srv1.HostPubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatalf("save offline-host: %v", err)
	}

	// Connection manager with short dial timeout for offline host detection
	cfg := host.DefaultConnectionConfig()
	cfg.DialTimeout = 500 * time.Millisecond
	cfg.HandshakeTimeout = 500 * time.Millisecond
	connMgr := host.NewConnectionManager(cfg, registry)
	defer connMgr.Close()

	lease1, err := connMgr.Lease(ctx, "online-1")
	if err != nil {
		t.Fatalf("lease online-1: %v", err)
	}
	lease2, err := connMgr.Lease(ctx, "online-2")
	if err != nil {
		t.Fatalf("lease online-2: %v", err)
	}

	// Create sessions on both hosts
	sessSvc1 := host.NewRemoteSessionService(lease1, srv1.RootDir, "")
	if _, err := sessSvc1.CreateSession(ctx, "sess-1"); err != nil {
		t.Fatalf("create sess-1: %v", err)
	}
	sessSvc2 := host.NewRemoteSessionService(lease2, srv2.RootDir, "")
	if _, err := sessSvc2.CreateSession(ctx, "sess-2"); err != nil {
		t.Fatalf("create sess-2: %v", err)
	}

	// 2. Start large file transfer on online-1 (3 MiB) in background
	fileSvc1 := host.NewRemoteFileService(lease1, 10*1024*1024, 100, t.TempDir())
	largeFileSize := int64(3 * 1024 * 1024)
	largeData := bytes.Repeat([]byte("ABCDEFGH01234567"), int(largeFileSize/16))

	transferDone := make(chan error, 1)
	go func() {
		largePath := filepath.Join(srv1.RootDir, "large_file.bin")
		_, wErr := fileSvc1.Write(ctx, largePath, bytes.NewReader(largeData), largeFileSize, nil, false)
		transferDone <- wErr
	}()

	// 3. Start slow terminal consumer on online-1
	termFactory1 := host.NewRemoteProcessFactory(lease1, "")
	proc1, err := termFactory1.NewProcess(ctx, "sess-1", 80, 24)
	if err != nil {
		t.Fatalf("open terminal proc1: %v", err)
	}

	slowConsumerStop := make(chan struct{})
	slowConsumerDone := make(chan struct{})
	go func() {
		defer close(slowConsumerDone)
		buf := make([]byte, 64)
		for {
			select {
			case <-slowConsumerStop:
				return
			default:
				_, rErr := proc1.Read(buf)
				if rErr != nil {
					return
				}
				time.Sleep(10 * time.Millisecond) // Throttled reading
			}
		}
	}()

	// Blast some output to proc1
	go func() {
		for i := 0; i < 50; i++ {
			select {
			case <-slowConsumerStop:
				return
			default:
				_, _ = fmt.Fprintf(proc1, "echo chunk-%d\r\n", i)
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// 4. Verify independent responsive terminal on online-2
	// Despite online-1 executing large transfer and having slow terminal, online-2 must be fully responsive
	termFactory2 := host.NewRemoteProcessFactory(lease2, "")
	proc2, err := termFactory2.NewProcess(ctx, "sess-2", 80, 24)
	if err != nil {
		t.Fatalf("open terminal proc2: %v", err)
	}

	// Write prompt command and verify immediate echo
	uniqueToken := fmt.Sprintf("TOKEN_%d", time.Now().UnixNano())
	if _, err := fmt.Fprintf(proc2, "echo %s\r\n", uniqueToken); err != nil {
		t.Fatalf("write to proc2: %v", err)
	}

	readRespCh := make(chan bool, 1)
	go func() {
		buf := make([]byte, 2048)
		accumulated := ""
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			n, err := proc2.Read(buf)
			if n > 0 {
				accumulated += string(buf[:n])
				if strings.Contains(accumulated, uniqueToken) {
					readRespCh <- true
					return
				}
			}
			if err != nil {
				break
			}
		}
		readRespCh <- false
	}()

	select {
	case found := <-readRespCh:
		if !found {
			t.Fatalf("expected responsive echo of %s on online-2", uniqueToken)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("timeout waiting for responsive terminal echo on online-2")
	}

	// 5. Offline host failure isolation
	// Concurrently query offline host; verify fast failure without degrading online hosts
	offlineErrCh := make(chan error, 1)
	go func() {
		shortCtx, shortCancel := context.WithTimeout(ctx, 2*time.Second)
		defer shortCancel()
		offlineLease, err := connMgr.Lease(shortCtx, "offline-host")
		if err != nil {
			offlineErrCh <- err
			return
		}
		_, err = offlineLease.GetControl(shortCtx)
		offlineErrCh <- err
	}()

	select {
	case oErr := <-offlineErrCh:
		if oErr == nil {
			t.Fatal("expected error when leasing offline host")
		}
		var hErr *host.Error
		if errors.As(oErr, &hErr) && hErr.Code != host.ErrUnavailable {
			t.Fatalf("expected ErrUnavailable for offline host, got: %v", oErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("offline host query stalled or exceeded timeout budget")
	}

	// Wait for large file transfer to finish
	select {
	case err := <-transferDone:
		if err != nil {
			t.Fatalf("large file transfer failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for large file transfer to complete")
	}

	// 6. Bounded shutdown and process cleanup
	close(slowConsumerStop)
	_ = proc1.Close()
	_ = proc2.Close()
	<-slowConsumerDone

	// Shutdown terminal manager coordination
	termMgr := terminal.NewManager(1 * time.Second)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := termMgr.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("terminal manager shutdown error: %v", err)
	}

	// Verify online sessions remain alive in isolated tmux servers
	s1, err := sessSvc1.GetSession(ctx, "sess-1")
	if err != nil || s1 == nil {
		t.Fatalf("sess-1 must survive shutdown: %v", err)
	}
	s2, err := sessSvc2.GetSession(ctx, "sess-2")
	if err != nil || s2 == nil {
		t.Fatalf("sess-2 must survive shutdown: %v", err)
	}
}
