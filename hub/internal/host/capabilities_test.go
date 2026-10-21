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
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
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
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // test cleanup

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close() //nolint:errcheck // mock conn cleanup
					return
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					for newChannel := range chans {
						if newChannel.ChannelType() != "session" {
							_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported") //nolint:errcheck // reject
							continue
						}
						ch, channelReqs, err := newChannel.Accept()
						if err != nil {
							continue
						}
						go func(in <-chan *ssh.Request, channel ssh.Channel) {
							defer channel.Close() //nolint:errcheck // test cleanup
							for req := range in {
								switch req.Type {
								case "exec":
									var payload struct {
										Command string
									}
									_ = ssh.Unmarshal(req.Payload, &payload) //nolint:errcheck // unmarshal
									if strings.Contains(payload.Command, "tmux") && opts.tmuxEnabled {
										_ = req.Reply(true, nil)                    //nolint:errcheck // reply
										_, _ = channel.Write([]byte("tmux 3.3a\n")) //nolint:errcheck // write
										// Send exit status 0
										exitStatus := make([]byte, 4)
										binary.BigEndian.PutUint32(exitStatus, 0)
										//nolint:errcheck // status
										_, _ = channel.SendRequest("exit-status", false, exitStatus)
									} else {
										_ = req.Reply(true, nil) //nolint:errcheck // reply
										exitStatus := make([]byte, 4)
										binary.BigEndian.PutUint32(exitStatus, 127)
										//nolint:errcheck // status
										_, _ = channel.SendRequest("exit-status", false, exitStatus)
									}
									return
								case "subsystem":
									var payload struct {
										Subsystem string
									}
									_ = ssh.Unmarshal(req.Payload, &payload) //nolint:errcheck // unmarshal
									if payload.Subsystem == "sftp" && opts.sftpEnabled {
										_ = req.Reply(true, nil) //nolint:errcheck // reply
										server := sftp.NewRequestServer(channel, sftp.InMemHandler())
										go func() {
											_ = server.Serve()  //nolint:errcheck // serve
											_ = channel.Close() //nolint:errcheck // close
										}()
										return
									} else {
										_ = req.Reply(false, nil) //nolint:errcheck // reject
									}
								default:
									if req.WantReply {
										_ = req.Reply(false, nil) //nolint:errcheck // reject
									}
								}
							}
						}(channelReqs, ch)
					}
				}()
				_ = sshConn.Wait() //nolint:errcheck // wait
			}(tcpConn)
		}
	}()

	return listener, hostSigner.PublicKey()
}

func TestLocalCapabilities(t *testing.T) {
	ctx := context.Background()
	regDir := t.TempDir()
	r, err := host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
	defer mgr.Close() //nolint:errcheck // test cleanup

	// 1. Default local capabilities
	caps, err := mgr.CheckCapabilities(ctx, host.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Online || !caps.Tmux.Available {
		t.Fatalf("unexpected local capabilities: %+v", caps)
	}
	if caps.SSH.Available || caps.SSH.State != host.CapabilityStateNotApplicable {
		t.Fatalf("local SSH should be not_applicable: %+v", caps.SSH)
	}
	if caps.SFTP.Available || caps.SFTP.State != host.CapabilityStateNotApplicable {
		t.Fatalf("local SFTP should be not_applicable: %+v", caps.SFTP)
	}
	if !caps.Files.Enabled || caps.Files.Boundary != "account_bound" ||
		caps.Files.RootHandleConfinement || !caps.Files.AtomicReplacement {
		t.Fatalf("local default files mismatch: %+v", caps.Files)
	}

	// 2. Local capabilities with files disabled
	rootSet, err := files.NewRootSet(nil)
	if err != nil {
		t.Fatal(err)
	}
	disabledFiles := files.NewService(files.Options{Roots: rootSet, Enabled: false})
	mgr.SetLocalRuntime(host.Runtime{
		Files: disabledFiles,
	})
	caps, err = mgr.CheckCapabilities(ctx, host.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if caps.Files.Enabled || caps.Files.State != host.CapabilityStateUnavailable ||
		caps.Files.Error != "files_disabled" {
		t.Fatalf("disabled files not reflected: %+v", caps.Files)
	}

	// 3. Local capabilities with root confinement
	rootDir := t.TempDir()
	confinedRootSet, err := files.NewRootSet([]string{rootDir})
	if err != nil {
		t.Fatal(err)
	}
	confinedFiles := files.NewService(files.Options{Roots: confinedRootSet, Enabled: true})
	mgr.SetLocalRuntime(host.Runtime{
		Files: confinedFiles,
	})
	caps, err = mgr.CheckCapabilities(ctx, host.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if !caps.Files.Enabled || caps.Files.Boundary != "root_confined" || !caps.Files.RootHandleConfinement {
		t.Fatalf("confined files not reflected: %+v", caps.Files)
	}

	// 4. Deleted local host returns ErrNotFound
	if err := r.Delete(ctx, host.LocalID, 1); err != nil {
		t.Fatal(err)
	}
	_, err = mgr.CheckCapabilities(ctx, host.LocalID)
	if !errors.Is(err, host.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for deleted local host, got: %v", err)
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
			_, portStr, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				t.Fatal(err)
			}

			regDir := t.TempDir()
			r, err := host.NewRegistry(ctx, regDir, "")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close() //nolint:errcheck // test cleanup

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
			defer mgr.Close() //nolint:errcheck // test cleanup

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
	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	regDir := t.TempDir()
	r, err := host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close() //nolint:errcheck // test cleanup

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
	defer mgr.Close() //nolint:errcheck // test cleanup

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

func TestCapabilitiesChannelOpenCancellation(t *testing.T) {
	signer := generateTestHostKey(t)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close() //nolint:errcheck // test cleanup

	opened := make(chan struct{})
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		sc, ch, req, e := ssh.NewServerConn(c, cfg)
		if e != nil {
			return
		}
		defer sc.Close() //nolint:errcheck // test cleanup
		go ssh.DiscardRequests(req)
		for range ch {
			select {
			case <-opened:
			default:
				close(opened)
			}
		}
	}()

	_, ps, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ps, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := setupTestRegistryWithHost(t, "stall", uint16(port), signer.PublicKey())
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_, _ = mgr.CheckCapabilities(ctx, "stall") //nolint:errcheck // cancellation test
		close(done)
	}()

	<-opened
	select {
	case <-done:
		// Returned promptly when context deadline expired
	case <-time.After(250 * time.Millisecond):
		t.Fatal("CheckCapabilities hung ignoring context cancellation during channel open")
	}
}
