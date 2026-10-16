package host_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

func TestPOSIXEncoderLiteralFidelity(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
	}{
		{"Empty", ""},
		{"Simple", "hello"},
		{"Spaces", "hello world foo bar"},
		{"SingleQuotes", "it's a test's quote"},
		{"DoubleQuotes", `say "hello" to 'everyone'`},
		{"DollarSigns", "$HOME $PATH ${USER} $? $$"},
		{"BackticksAndCommandSub", "`reboot` $(echo injected) `whoami`"},
		{"ShellMetachars", "; & | && || < > >> ( ) * ? [ ] ! \\ ~ ^"},
		{"Unicode", "你好世界 🚀 ñoño €100 ₽500 日本語"},
		{"MixedHostile", `'"; rm -rf /; echo $HOME; ` + "`reboot`" + ` --flag="val'ue"`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			encoded := host.EncodePOSIXArg(tc.raw)

			// Execute with real /bin/sh to verify exact byte fidelity
			cmd := exec.Command("sh", "-c", fmt.Sprintf("printf '%%s' %s", encoded))
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			if err := cmd.Run(); err != nil {
				t.Fatalf("sh execution failed: %v, stderr: %s", err, stderr.String())
			}

			if stdout.String() != tc.raw {
				t.Fatalf("literal mismatch: got %q, expected %q (encoded was: %s)", stdout.String(), tc.raw, encoded)
			}
		})
	}
}

func TestPOSIXCommandEncoding(t *testing.T) {
	verifyScript := `for a in "$@"; do printf '%s\n' "$a"; done`
	fullCmd := host.EncodePOSIXCommand("sh", "-c", verifyScript, "--", "tmux", "new-session", "-d", "-s", "session $1 'test'", "-c", "/path/with spaces")

	runCmd := exec.Command("sh", "-c", fullCmd)
	out, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command execution failed: %v, output: %s", err, string(out))
	}

	expectedLines := []string{
		"tmux",
		"new-session",
		"-d",
		"-s",
		"session $1 'test'",
		"-c",
		"/path/with spaces",
	}

	actualLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(actualLines) != len(expectedLines) {
		t.Fatalf("arg count mismatch: got %d, expected %d. output:\n%s", len(actualLines), len(expectedLines), string(out))
	}
	for i := range expectedLines {
		if actualLines[i] != expectedLines[i] {
			t.Errorf("arg %d: got %q, expected %q", i, actualLines[i], expectedLines[i])
		}
	}
}

// startMockEnvSSHServer starts a test SSH server that responds to the environment probe command with specific output.
func startMockEnvSSHServer(t *testing.T, stdoutResp string, exitStatus uint32) (net.Listener, ssh.PublicKey) {
	t.Helper()
	signer := generateTestHostKey(t)
	serverConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	serverConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func(tcp net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(tcp, serverConfig)
				if err != nil {
					_ = tcp.Close()
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
									_ = req.Reply(true, nil)
									_, _ = io.WriteString(channel, stdoutResp)
									if exitStatus != 0 {
										// send exit status
										statusMsg := struct{ Status uint32 }{Status: exitStatus}
										_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(&statusMsg))
									} else {
										statusMsg := struct{ Status uint32 }{Status: 0}
										_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(&statusMsg))
									}
									_ = channel.Close()
									return
								}
								_ = req.Reply(false, nil)
							}
						}()
					}
				}()
				_ = sshConn.Wait()
			}(c)
		}
	}()

	return listener, signer.PublicKey()
}

func TestProbeRemoteEnvironment(t *testing.T) {
	t.Run("ValidLinuxEnvironment", func(t *testing.T) {
		listener, hostKey := startMockEnvSSHServer(t, "Linux\n---ENV-SEP---\n/home/ubuntu\n---ENV-SEP---\ntmux 3.3a\n", 0)
		client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			User:            "ubuntu",
			HostKeyCallback: host.PinnedHostKeyCallback(hostKey.Marshal()),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		env, err := host.ProbeRemoteEnvironment(context.Background(), client)
		if err != nil {
			t.Fatalf("expected successful probe, got: %v", err)
		}
		if env.OS != "Linux" || env.Home != "/home/ubuntu" || env.TmuxVersion != "tmux 3.3a" {
			t.Fatalf("unexpected probe result: %+v", env)
		}
	})

	t.Run("UnsupportedWindowsOS", func(t *testing.T) {
		listener, hostKey := startMockEnvSSHServer(t, "Windows_NT\n---ENV-SEP---\nC:\\Users\\admin\n---ENV-SEP---\ntmux 3.0\n", 0)
		client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			User:            "admin",
			HostKeyCallback: host.PinnedHostKeyCallback(hostKey.Marshal()),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		_, err = host.ProbeRemoteEnvironment(context.Background(), client)
		if !errors.Is(err, host.ErrUnsupportedEnvironment) {
			t.Fatalf("expected ErrUnsupportedEnvironment for Windows, got: %v", err)
		}
	})

	t.Run("TmuxUnavailable", func(t *testing.T) {
		listener, hostKey := startMockEnvSSHServer(t, "Linux\n---ENV-SEP---\n/home/user\n---ENV-SEP---\nsh: tmux: command not found\n", 127)
		client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
			User:            "user",
			HostKeyCallback: host.PinnedHostKeyCallback(hostKey.Marshal()),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()

		_, err = host.ProbeRemoteEnvironment(context.Background(), client)
		if !errors.Is(err, host.ErrTmuxUnavailable) {
			t.Fatalf("expected ErrTmuxUnavailable when tmux is missing, got: %v", err)
		}
	})
}
