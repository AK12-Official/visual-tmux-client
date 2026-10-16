package host_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type mockServerOptions struct {
	expectedUser     string
	expectedPassword string
	tmuxEnabled      bool
	sftpEnabled      bool
}

func startCapabilitySSHServer(t *testing.T, opts mockServerOptions) (net.Listener, ssh.PublicKey) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == opts.expectedUser && string(password) == opts.expectedPassword {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			tcpConn, err := listener.Accept()
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
						ch, channelReqs, err := newChannel.Accept()
						if err != nil {
							continue
						}
						go func(in <-chan *ssh.Request, channel ssh.Channel) {
							defer channel.Close()
							for req := range in {
								switch req.Type {
								case "exec":
									var payload struct {
										Command string
									}
									_ = ssh.Unmarshal(req.Payload, &payload)
									if strings.Contains(payload.Command, "tmux") && opts.tmuxEnabled {
										_ = req.Reply(true, nil)
										_, _ = channel.Write([]byte("tmux 3.3a\n"))
										// Send exit status 0
										exitStatus := make([]byte, 4)
										binary.BigEndian.PutUint32(exitStatus, 0)
										_, _ = channel.SendRequest("exit-status", false, exitStatus)
									} else {
										_ = req.Reply(true, nil)
										exitStatus := make([]byte, 4)
										binary.BigEndian.PutUint32(exitStatus, 127)
										_, _ = channel.SendRequest("exit-status", false, exitStatus)
									}
									return
								case "subsystem":
									var payload struct {
										Subsystem string
									}
									_ = ssh.Unmarshal(req.Payload, &payload)
									if payload.Subsystem == "sftp" && opts.sftpEnabled {
										_ = req.Reply(true, nil)
										server := sftp.NewRequestServer(channel, sftp.InMemHandler())
										go func() {
											_ = server.Serve()
											_ = channel.Close()
										}()
										return
									} else {
										_ = req.Reply(false, nil)
									}
								default:
									if req.WantReply {
										_ = req.Reply(false, nil)
									}
								}
							}
						}(channelReqs, ch)
					}
				}()
				_ = sshConn.Wait()
			}(tcpConn)
		}
	}()

	return listener, hostSigner.PublicKey()
}

func TestLocalCapabilities(t *testing.T) {
	regDir := t.TempDir()
	r, err := host.NewRegistry(context.Background(), regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
	defer mgr.Close()

	caps, err := mgr.CheckCapabilities(context.Background(), host.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Online || !caps.SSH.Available || !caps.Tmux.Available {
		t.Fatalf("unexpected local capabilities: %+v", caps)
	}
	if caps.SFTP.Available {
		t.Fatalf("local should not claim sftp available: %+v", caps)
	}
}

func TestCapabilitiesIndependentStatuses(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name         string
		tmuxEnabled  bool
		sftpEnabled  bool
		expectedTmux bool
		expectedSFTP bool
	}{
		{"both supported", true, true, true, true},
		{"missing sftp does not disable tmux", true, false, true, false},
		{"missing tmux does not hide sftp", false, true, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			listener, pubKey := startCapabilitySSHServer(t, mockServerOptions{
				expectedUser:     "alice",
				expectedPassword: "password123",
				tmuxEnabled:      tc.tmuxEnabled,
				sftpEnabled:      tc.sftpEnabled,
			})
			_, portStr, _ := net.SplitHostPort(listener.Addr().String())
			port, _ := strconv.ParseUint(portStr, 10, 16)

			regDir := t.TempDir()
			r, err := host.NewRegistry(ctx, regDir, "")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()

			hostID := host.ID("test-remote")
			_, err = r.Save(ctx, host.HostInput{
				ID:             hostID,
				Name:           "Test Remote",
				Address:        "127.0.0.1",
				Port:           uint16(port),
				Username:       "alice",
				CredentialType: "password",
				Credentials:    host.Credentials{Password: "password123"},
				HostKey:        pubKey.Marshal(),
			}, 0)
			if err != nil {
				t.Fatal(err)
			}

			mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
			defer mgr.Close()

			caps, err := mgr.CheckCapabilities(ctx, hostID)
			if err != nil {
				t.Fatal(err)
			}
			if !caps.Online || !caps.SSH.Available {
				t.Fatalf("expected SSH available: %+v", caps)
			}
			if caps.Tmux.Available != tc.expectedTmux {
				t.Fatalf("expected tmux=%v, got: %+v", tc.expectedTmux, caps)
			}
			if caps.SFTP.Available != tc.expectedSFTP {
				t.Fatalf("expected sftp=%v, got: %+v", tc.expectedSFTP, caps)
			}
		})
	}
}

func TestCapabilitiesAuthenticationFailureAndOffline(t *testing.T) {
	ctx := context.Background()

	listener, pubKey := startCapabilitySSHServer(t, mockServerOptions{
		expectedUser:     "alice",
		expectedPassword: "password123",
		tmuxEnabled:      true,
		sftpEnabled:      true,
	})
	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	regDir := t.TempDir()
	r, err := host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// 1. Wrong password
	_, err = r.Save(ctx, host.HostInput{
		ID:             "bad-auth",
		Name:           "Bad Auth Host",
		Address:        "127.0.0.1",
		Port:           uint16(port),
		Username:       "alice",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "wrong-password"},
		HostKey:        pubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
	defer mgr.Close()

	caps, err := mgr.CheckCapabilities(ctx, "bad-auth")
	if err != nil {
		t.Fatalf("CheckCapabilities should not return Go error for remote auth failure: %v", err)
	}
	if caps.Online || caps.SSH.Available || caps.SSH.Error != "authentication_failed" {
		t.Fatalf("expected authentication_failed: %+v", caps)
	}

	// 2. Offline host
	_, err = r.Save(ctx, host.HostInput{
		ID:             "offline-host",
		Name:           "Offline Host",
		Address:        "127.0.0.1",
		Port:           1, // closed port
		Username:       "alice",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "password123"},
		HostKey:        pubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	caps, err = mgr.CheckCapabilities(ctx, "offline-host")
	if err != nil {
		t.Fatalf("CheckCapabilities should not return Go error for offline host: %v", err)
	}
	if caps.Online || caps.SSH.Available || caps.SSH.Error != "host_unavailable" {
		t.Fatalf("expected host_unavailable: %+v", caps)
	}

	// 3. Verify offline host does NOT block registry listing!
	hosts, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 3 { // local, bad-auth, offline-host
		t.Fatalf("expected 3 hosts listed, got %d", len(hosts))
	}
}
