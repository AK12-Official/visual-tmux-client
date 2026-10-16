package testutil_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestDisposableSSHFixturePasswordAuthAndTmuxIsolation(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	cfg := testutil.DisposableSSHConfig{
		User:           "operator",
		Password:       "op-secret",
		TmuxSocketName: "fix-iso-1",
	}
	server := testutil.NewDisposableSSHServer(t, cfg)

	// Check host fingerprint and key type
	if !strings.HasPrefix(server.Fingerprint, "SHA256:") {
		t.Fatalf("unexpected fingerprint format: %s", server.Fingerprint)
	}
	if server.KeyType != "ssh-ed25519" {
		t.Fatalf("unexpected key type: %s", server.KeyType)
	}

	// Verify root directory permissions (0700)
	info, err := os.Stat(server.RootDir)
	if err != nil {
		t.Fatalf("stat root dir: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("expected 0700 permissions, got: %v", info.Mode().Perm())
	}

	// Connect via SSH with password
	sshConfig := &ssh.ClientConfig{
		User:            "operator",
		Auth:            []ssh.AuthMethod{ssh.Password("op-secret")},
		HostKeyCallback: ssh.FixedHostKey(server.HostPubKey),
		Timeout:         3 * time.Second,
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(server.Addr, fmt.Sprintf("%d", server.Port)), sshConfig)
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer client.Close()

	// 1. Run a tmux session command via SSH exec and verify environment isolation
	sess, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer sess.Close()

	var out bytes.Buffer
	sess.Stdout = &out
	// Create a new session in isolated tmux
	if err := sess.Run("tmux new-session -d -s isolated-alpha 'sleep 60'"); err != nil {
		t.Fatalf("tmux new-session: %v", err)
	}

	// List sessions via a second command
	sess2, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session 2: %v", err)
	}
	defer sess2.Close()
	var listOut bytes.Buffer
	sess2.Stdout = &listOut
	if err := sess2.Run("tmux list-sessions -F '#{session_name}'"); err != nil {
		t.Fatalf("tmux list-sessions: %v", err)
	}
	if !strings.Contains(listOut.String(), "isolated-alpha") {
		t.Fatalf("expected isolated-alpha in list, got: %q", listOut.String())
	}

	// Verify that the command environment on the server was scrubbed:
	// TMUX and TMUX_PANE must not be in the remote process environment
	sess3, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session 3: %v", err)
	}
	defer sess3.Close()
	var envOut bytes.Buffer
	sess3.Stdout = &envOut
	if err := sess3.Run("env"); err != nil {
		t.Fatalf("env run: %v", err)
	}
	envText := envOut.String()
	for _, line := range strings.Split(envText, "\n") {
		if strings.HasPrefix(line, "TMUX=") || strings.HasPrefix(line, "TMUX_PANE=") {
			t.Fatalf("scrubbed env violation: found %s", line)
		}
	}

	// 2. Test SFTP subsystem on the disposable server
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		t.Fatalf("sftp client: %v", err)
	}
	defer sftpClient.Close()

	f, err := sftpClient.Create("hello.txt")
	if err != nil {
		t.Fatalf("sftp create: %v", err)
	}
	if _, err := f.Write([]byte("disposable test content")); err != nil {
		t.Fatalf("sftp write: %v", err)
	}
	_ = f.Close()

	// Verify file landed in server.RootDir
	diskPath := filepath.Join(server.RootDir, "hello.txt")
	data, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("read disk file: %v", err)
	}
	if string(data) != "disposable test content" {
		t.Fatalf("file content mismatch: %q", string(data))
	}
}

func TestDisposableSSHFixturePrivateKeyAndPTY(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	cfg := testutil.DisposableSSHConfig{
		User:           "keyuser",
		UsePrivateKey:  true,
		TmuxSocketName: "fix-iso-2",
	}
	server := testutil.NewDisposableSSHServer(t, cfg)

	signer, err := ssh.ParsePrivateKey([]byte(server.UserKeyPEM))
	if err != nil {
		t.Fatalf("parse generated key: %v", err)
	}

	sshConfig := &ssh.ClientConfig{
		User:            "keyuser",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(server.HostPubKey),
		Timeout:         3 * time.Second,
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(server.Addr, fmt.Sprintf("%d", server.Port)), sshConfig)
	if err != nil {
		t.Fatalf("ssh dial with private key: %v", err)
	}
	defer client.Close()

	// Create a tmux session first
	setupSess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := setupSess.Run("tmux new-session -d -s pty-sess 'echo READY; sleep 60'"); err != nil {
		t.Fatalf("new session: %v", err)
	}
	_ = setupSess.Close()

	// Attach with PTY
	ptySess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer ptySess.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := ptySess.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		t.Fatalf("request pty: %v", err)
	}

	stdoutPipe, err := ptySess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdinPipe, err := ptySess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := ptySess.Start("tmux attach-session -t pty-sess"); err != nil {
		t.Fatalf("start attach: %v", err)
	}

	buf := make([]byte, 1024)
	n, err := stdoutPipe.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read pty stdout: %v", err)
	}
	if n == 0 {
		t.Fatal("expected output from tmux attach")
	}

	// Detach cleanly
	// In tmux, Ctrl+b d detaches: \x02 then d
	_, _ = stdinPipe.Write([]byte{2, 'd'})

	// Window resize test
	if err := ptySess.WindowChange(30, 100); err != nil {
		t.Logf("window change notice: %v", err)
	}

	_ = ptySess.Close()
}
