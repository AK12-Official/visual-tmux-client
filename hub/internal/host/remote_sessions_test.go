package host_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

// startMockTmuxSSHServer starts a disposable SSH server that simulates remote tmux command execution.
func startMockTmuxSSHServer(t *testing.T, initialSessions []string) (net.Listener, ssh.PublicKey, *mockTmuxState) {
	t.Helper()
	signer := generateTestHostKey(t)
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(signer)

	state := &mockTmuxState{
		sessions: make(map[string]string), // name -> working dir
	}
	for _, s := range initialSessions {
		state.sessions[s] = "/mock/home"
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			tcp, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					for newChannel := range chans {
						if newChannel.ChannelType() != "session" {
							_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
							continue
						}
						channel, requests, err := newChannel.Accept()
						if err != nil {
							continue
						}
						go func() {
							for req := range requests {
								if req.Type == "exec" {
									state.mu.Lock()
									discBefore := state.disconnectBeforeDispatch
									discAfter := state.disconnectAfterDispatch
									state.mu.Unlock()

									if discBefore {
										_ = req.Reply(false, nil)
										_ = channel.Close()
										_ = c.Close()
										return
									}

									_ = req.Reply(true, nil)
									cmd := string(req.Payload[4:])
									if discAfter {
										state.mu.Lock()
										state.executedCmds = append(state.executedCmds, cmd)
										state.mu.Unlock()
										_ = c.Close()
										_ = channel.Close()
										return
									}

									state.handleExec(cmd, channel)
									return
								}
								_ = req.Reply(false, nil)
							}
						}()
					}
				}()
				_ = sshConn.Wait()
			}(tcp)
		}
	}()

	return listener, signer.PublicKey(), state
}

type mockTmuxState struct {
	mu                       sync.Mutex
	sessions                 map[string]string
	executedCmds             []string
	createdHome              string
	disconnectBeforeDispatch bool
	disconnectAfterDispatch  bool
}

func (s *mockTmuxState) handleExec(cmd string, ch ssh.Channel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executedCmds = append(s.executedCmds, cmd)

	var stdout, stderr string
	var exitCode uint32

	switch {
	case strings.Contains(cmd, "list-sessions"):
		if len(s.sessions) == 0 {
			stderr = "no server running on /tmp/tmux-1000/default\n"
			exitCode = 1
		} else {
			for name := range s.sessions {
				// Format: session_name|session_windows|session_attached|session_created
				stdout += fmt.Sprintf("%s|1|0|1700000000\n", name)
			}
		}

	case strings.Contains(cmd, "has-session"):
		found := false
		for name := range s.sessions {
			if strings.Contains(cmd, "="+name) || strings.Contains(cmd, name) {
				found = true
				break
			}
		}
		if !found {
			stderr = "can't find session\n"
			exitCode = 1
		}

	case strings.Contains(cmd, "new-session"):
		// Extract session name and -c home
		parts := strings.Fields(cmd)
		sessName := ""
		for i, p := range parts {
			if strings.Contains(p, "-s") && i+1 < len(parts) {
				sessName = strings.Trim(parts[i+1], "'")
			}
			if strings.Contains(p, "-c") && i+1 < len(parts) {
				s.createdHome = strings.Trim(parts[i+1], "'")
			}
		}
		if sessName != "" {
			if _, exists := s.sessions[sessName]; exists {
				stderr = "duplicate session: " + sessName + "\n"
				exitCode = 1
			} else {
				s.sessions[sessName] = s.createdHome
			}
		}

	case strings.Contains(cmd, "rename-session"):
		// e.g. rename-session -t =old -- new
		parts := strings.Fields(cmd)
		if len(parts) >= 2 {
			oldName := ""
			newName := strings.Trim(parts[len(parts)-1], "'")
			for i, p := range parts {
				if strings.Contains(p, "-t") && i+1 < len(parts) {
					oldName = strings.TrimPrefix(strings.Trim(parts[i+1], "'"), "=")
				}
			}
			if oldName != "" && newName != "" {
				if _, exists := s.sessions[newName]; exists {
					stderr = "duplicate session: " + newName + "\n"
					exitCode = 1
				} else if dir, ok := s.sessions[oldName]; ok {
					delete(s.sessions, oldName)
					s.sessions[newName] = dir
				} else {
					stderr = "can't find session\n"
					exitCode = 1
				}
			}
		}

	case strings.Contains(cmd, "kill-session"):
		found := false
		for name := range s.sessions {
			if strings.Contains(cmd, "="+name) || strings.Contains(cmd, name) {
				delete(s.sessions, name)
				found = true
				break
			}
		}
		if !found {
			stderr = "can't find session\n"
			exitCode = 1
		}

	case strings.Contains(cmd, "list-panes"):
		if len(s.sessions) == 0 {
			stderr = "no server running\n"
			exitCode = 1
		} else {
			for name := range s.sessions {
				// Record: length-prefixed fields matching tmux/panes.go format
				fields := []string{name, "0", "0", "1", "1", "0", "bash", "title", "bash"}
				var line strings.Builder
				for _, f := range fields {
					fmt.Fprintf(&line, "%d:%s", len(f), f)
				}
				line.WriteString("\n")
				stdout += line.String()
			}
		}

	case strings.Contains(cmd, "display-message"):
		// #{pane_current_path}
		for name, dir := range s.sessions {
			if strings.Contains(cmd, name) {
				stdout = dir + "\n"
				break
			}
		}
	}

	if stdout != "" {
		_, _ = io.WriteString(ch, stdout)
	}
	if stderr != "" {
		_, _ = io.WriteString(ch.Stderr(), stderr)
	}
	statusMsg := struct{ Status uint32 }{Status: exitCode}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(&statusMsg))
	_ = ch.Close()
}

func TestRemoteSessionOperationsAndIsolation(t *testing.T) {
	// Host A has session "dev" and home "/home/userA"
	listenerA, keyA, stateA := startMockTmuxSSHServer(t, []string{"dev"})
	// Host B has session "dev" and home "/home/userB"
	listenerB, keyB, stateB := startMockTmuxSSHServer(t, []string{"dev"})

	_, portAStr, _ := net.SplitHostPort(listenerA.Addr().String())
	portA, _ := strconv.ParseUint(portAStr, 10, 16)

	_, portBStr, _ := net.SplitHostPort(listenerB.Addr().String())
	portB, _ := strconv.ParseUint(portBStr, 10, 16)

	regA, _ := setupTestRegistryWithHost(t, "host-A", uint16(portA), keyA)
	mgrA := host.NewConnectionManager(host.DefaultConnectionConfig(), regA)
	defer mgrA.Close()

	regB, _ := setupTestRegistryWithHost(t, "host-B", uint16(portB), keyB)
	mgrB := host.NewConnectionManager(host.DefaultConnectionConfig(), regB)
	defer mgrB.Close()

	leaseA, err := mgrA.Lease(context.Background(), "host-A")
	if err != nil {
		t.Fatal(err)
	}
	serviceA := host.NewRemoteSessionService(leaseA, "/home/userA", "tmux")

	leaseB, err := mgrB.Lease(context.Background(), "host-B")
	if err != nil {
		t.Fatal(err)
	}
	serviceB := host.NewRemoteSessionService(leaseB, "/home/userB", "tmux")

	ctx := context.Background()

	// 1. List sessions on Host A and Host B
	listA, err := serviceA.ListSessions(ctx)
	if err != nil {
		var hErr *host.Error
		if errors.As(err, &hErr) {
			t.Fatalf("list A: %v, cause: %v", err, hErr.Cause)
		}
		t.Fatalf("list A: %v", err)
	}
	if len(listA) != 1 || listA[0].Name != "dev" {
		t.Fatalf("unexpected list A: %+v", listA)
	}
	// Verify pane summary was populated
	if listA[0].Pane == nil || listA[0].Pane.CurrentCommand != "bash" {
		t.Fatalf("expected pane summary on host A, got %+v", listA[0].Pane)
	}

	listB, err := serviceB.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 1 || listB[0].Name != "dev" {
		t.Fatalf("unexpected list B: %+v", listB)
	}

	// 2. Terminate session "dev" on Host A
	if err := serviceA.KillSession(ctx, "dev"); err != nil {
		t.Fatalf("kill A: %v", err)
	}

	// Host A no longer has "dev", but Host B STILL HAS "dev"! (Isolation)
	stateA.mu.Lock()
	_, devInA := stateA.sessions["dev"]
	stateA.mu.Unlock()
	if devInA {
		t.Fatal("session dev still present in host A")
	}

	stateB.mu.Lock()
	_, devInB := stateB.sessions["dev"]
	stateB.mu.Unlock()
	if !devInB {
		t.Fatal("session dev was deleted from host B! Cross-host isolation failure!")
	}

	// 3. Create a session on Host A and verify it uses Host A's remote home
	created, err := serviceA.CreateSession(ctx, "new-project")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.Name != "new-project" {
		t.Fatalf("expected name new-project, got %s", created.Name)
	}
	stateA.mu.Lock()
	createdHome := stateA.createdHome
	stateA.mu.Unlock()
	if createdHome != "/home/userA" {
		t.Fatalf("expected created home /home/userA, got %q", createdHome)
	}

	// 4. Rename session on Host A
	if err := serviceA.RenameSession(ctx, "new-project", "renamed-project"); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	stateA.mu.Lock()
	_, hasRenamed := stateA.sessions["renamed-project"]
	_, hasOld := stateA.sessions["new-project"]
	stateA.mu.Unlock()
	if !hasRenamed || hasOld {
		t.Fatalf("rename failed in state: sessions=%+v", stateA.sessions)
	}

	// 5. Working directory query
	workDir, err := serviceA.PaneWorkingDirectory(ctx, "renamed-project")
	if err != nil {
		t.Fatalf("pane working directory: %v", err)
	}
	if workDir != "/home/userA" {
		t.Fatalf("expected /home/userA, got %q", workDir)
	}
}

func TestMutationOutcomeClassificationAndNoReplay(t *testing.T) {
	// Start mock SSH server with session "target-session"
	listener, key, state := startMockTmuxSSHServer(t, []string{"target-session"})

	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	reg, _ := setupTestRegistryWithHost(t, "host-fault", uint16(port), key)
	connMgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer connMgr.Close()

	ctx := context.Background()

	// 1. Injected disconnect BEFORE dispatch for Rename
	state.mu.Lock()
	state.disconnectBeforeDispatch = true
	state.disconnectAfterDispatch = false
	state.executedCmds = nil
	state.mu.Unlock()

	lease, err := connMgr.Lease(ctx, "host-fault")
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	service := host.NewRemoteSessionService(lease, "/home/testuser", "tmux")

	err = service.RenameSession(ctx, "target-session", "new-name")
	if err == nil {
		t.Fatal("expected error on disconnect before dispatch, got nil")
	}
	if !errors.Is(err, host.ErrUnavailable) {
		t.Errorf("expected ErrUnavailable for disconnect before dispatch, got %v", err)
	}
	if errors.Is(err, host.ErrOutcomeUnknown) {
		t.Errorf("expected NOT ErrOutcomeUnknown for disconnect before dispatch, got %v", err)
	}
	state.mu.Lock()
	cmdsBefore := len(state.executedCmds)
	state.mu.Unlock()
	if cmdsBefore != 0 {
		t.Errorf("expected 0 executed commands when disconnected before dispatch, got %d", cmdsBefore)
	}

	// 2. Injected disconnect AFTER dispatch for Rename (mutation)
	state.mu.Lock()
	state.disconnectBeforeDispatch = false
	state.disconnectAfterDispatch = true
	state.executedCmds = nil
	state.mu.Unlock()

	lease2, err := connMgr.Lease(ctx, "host-fault")
	if err != nil {
		t.Fatalf("acquire lease 2: %v", err)
	}
	service2 := host.NewRemoteSessionService(lease2, "/home/testuser", "tmux")

	err = service2.RenameSession(ctx, "target-session", "new-name")
	if err == nil {
		t.Fatal("expected error on disconnect after dispatch, got nil")
	}
	if !errors.Is(err, host.ErrOutcomeUnknown) {
		t.Errorf("expected ErrOutcomeUnknown for disconnect after dispatch on Rename, got %v", err)
	}
	if errors.Is(err, host.ErrUnavailable) {
		t.Errorf("expected NOT ErrUnavailable for disconnect after dispatch on Rename, got %v", err)
	}
	state.mu.Lock()
	cmdsAfterRename := len(state.executedCmds)
	state.mu.Unlock()
	if cmdsAfterRename != 1 {
		t.Errorf("expected exactly 1 dispatched command without automatic replay, got %d", cmdsAfterRename)
	}

	// 3. Injected disconnect AFTER dispatch for Kill (mutation)
	state.mu.Lock()
	state.disconnectBeforeDispatch = false
	state.disconnectAfterDispatch = true
	state.executedCmds = nil
	state.mu.Unlock()

	lease3, err := connMgr.Lease(ctx, "host-fault")
	if err != nil {
		t.Fatalf("acquire lease 3: %v", err)
	}
	service3 := host.NewRemoteSessionService(lease3, "/home/testuser", "tmux")

	err = service3.KillSession(ctx, "target-session")
	if err == nil {
		t.Fatal("expected error on disconnect after dispatch on Kill, got nil")
	}
	if !errors.Is(err, host.ErrOutcomeUnknown) {
		t.Errorf("expected ErrOutcomeUnknown for disconnect after dispatch on Kill, got %v", err)
	}
	state.mu.Lock()
	cmdsAfterKill := len(state.executedCmds)
	state.mu.Unlock()
	if cmdsAfterKill != 1 {
		t.Errorf("expected exactly 1 dispatched command without automatic replay on Kill, got %d", cmdsAfterKill)
	}

	// 4. Injected disconnect AFTER dispatch for Create (mutation)
	state.mu.Lock()
	state.disconnectBeforeDispatch = false
	state.disconnectAfterDispatch = true
	state.executedCmds = nil
	state.mu.Unlock()

	lease4, err := connMgr.Lease(ctx, "host-fault")
	if err != nil {
		t.Fatalf("acquire lease 4: %v", err)
	}
	service4 := host.NewRemoteSessionService(lease4, "/home/testuser", "tmux")

	_, err = service4.CreateSession(ctx, "fresh-session")
	if err == nil {
		t.Fatal("expected error on disconnect after dispatch on Create, got nil")
	}
	if !errors.Is(err, host.ErrOutcomeUnknown) {
		t.Errorf("expected ErrOutcomeUnknown for disconnect after dispatch on Create, got %v", err)
	}
	state.mu.Lock()
	cmdsAfterCreate := len(state.executedCmds)
	state.mu.Unlock()
	if cmdsAfterCreate != 1 {
		t.Errorf("expected exactly 1 dispatched command without automatic replay on Create, got %d", cmdsAfterCreate)
	}

	// 5. Injected disconnect AFTER dispatch for read-only operation (ListSessions)
	state.mu.Lock()
	state.disconnectBeforeDispatch = false
	state.disconnectAfterDispatch = true
	state.executedCmds = nil
	state.mu.Unlock()

	lease5, err := connMgr.Lease(ctx, "host-fault")
	if err != nil {
		t.Fatalf("acquire lease 5: %v", err)
	}
	service5 := host.NewRemoteSessionService(lease5, "/home/testuser", "tmux")

	_, err = service5.ListSessions(ctx)
	if err == nil {
		t.Fatal("expected error on disconnect after dispatch on ListSessions, got nil")
	}
	if errors.Is(err, host.ErrOutcomeUnknown) {
		t.Errorf("read-only operation must NOT be ErrOutcomeUnknown, got %v", err)
	}
	if !errors.Is(err, host.ErrUnavailable) {
		t.Errorf("read-only operation expected ErrUnavailable, got %v", err)
	}
}
