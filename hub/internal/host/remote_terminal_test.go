package host_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

// startMockPTYSSHServer starts a disposable SSH server that handles PTY allocation, window changes, and duplex I/O.
func startMockPTYSSHServer(t *testing.T) (net.Listener, ssh.PublicKey, *mockPTYServerState) {
	t.Helper()
	signer := generateTestHostKey(t)
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			return nil, nil
		},
	}
	serverConfig.AddHostKey(signer)

	state := &mockPTYServerState{}

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
						go state.handleSession(channel, requests)
					}
				}()
				_ = sshConn.Wait()
			}(tcp)
		}
	}()

	return listener, signer.PublicKey(), state
}

type mockPTYServerState struct {
	mu            sync.Mutex
	ptyRequested  bool
	termType      string
	rows, cols    uint32
	windowResizes []struct{ cols, rows uint32 }
	receivedBytes bytes.Buffer
}

func (s *mockPTYServerState) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			s.mu.Lock()
			s.ptyRequested = true
			if len(req.Payload) >= 4 {
				termLen := binary.BigEndian.Uint32(req.Payload[:4])
				if uint32(len(req.Payload)) >= 4+termLen+8 {
					s.termType = string(req.Payload[4 : 4+termLen])
					s.cols = binary.BigEndian.Uint32(req.Payload[4+termLen : 4+termLen+4])
					s.rows = binary.BigEndian.Uint32(req.Payload[4+termLen+4 : 4+termLen+8])
				}
			}
			s.mu.Unlock()
			_ = req.Reply(true, nil)

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				s.mu.Lock()
				s.windowResizes = append(s.windowResizes, struct{ cols, rows uint32 }{cols, rows})
				s.mu.Unlock()
			}
			_ = req.Reply(true, nil)

		case "exec":
			_ = req.Reply(true, nil)
			// Start echoing input to output
			go func() {
				buf := make([]byte, 1024)
				for {
					n, err := ch.Read(buf)
					if n > 0 {
						s.mu.Lock()
						s.receivedBytes.Write(buf[:n])
						s.mu.Unlock()
						// Echo back to channel
						_, _ = ch.Write(buf[:n])
					}
					if err != nil {
						break
					}
				}
				statusMsg := struct{ Status uint32 }{Status: 0}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(&statusMsg))
				_ = ch.Close()
			}()

		default:
			_ = req.Reply(false, nil)
		}
	}
}

func TestRemotePTYProcess(t *testing.T) {
	listener, hostKey, state := startMockPTYSSHServer(t)
	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	reg, _ := setupTestRegistryWithHost(t, "pty-host", uint16(port), hostKey)
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close()

	lease, err := mgr.Lease(context.Background(), "pty-host")
	if err != nil {
		t.Fatal(err)
	}

	factory := host.NewRemoteProcessFactory(lease, "tmux")
	proc, err := factory.NewProcess(context.Background(), "test-session", 100, 30)
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}
	defer proc.Close()

	// 1. Verify PTY was requested with correct initial dimensions
	state.mu.Lock()
	ptyOk := state.ptyRequested
	termType := state.termType
	cols := state.cols
	rows := state.rows
	state.mu.Unlock()

	if !ptyOk {
		t.Fatal("PTY was not requested")
	}
	if termType != "xterm-256color" {
		t.Fatalf("unexpected term type: %s", termType)
	}
	if cols != 100 || rows != 30 {
		t.Fatalf("unexpected initial dimensions: cols=%d, rows=%d", cols, rows)
	}

	// 2. Test byte fidelity and control characters (read/write duplex echo)
	payload := []byte("Hello, world! \x03 \x1b[31mRed\x1b[0m 🚀 Unicode 测试\n")
	if _, err := proc.Write(payload); err != nil {
		t.Fatalf("proc.Write: %v", err)
	}

	readBuf := make([]byte, len(payload))
	if _, err := io.ReadFull(proc, readBuf); err != nil {
		t.Fatalf("io.ReadFull: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("byte fidelity failure:\ngot:  %q\nwant: %q", readBuf, payload)
	}

	// 3. Test Resize
	if err := proc.Resize(120, 40); err != nil {
		t.Fatalf("proc.Resize: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	state.mu.Lock()
	resizes := state.windowResizes
	state.mu.Unlock()

	if len(resizes) == 0 || resizes[len(resizes)-1].cols != 120 || resizes[len(resizes)-1].rows != 40 {
		t.Fatalf("resize not received: %+v", resizes)
	}

	// 4. Test Idempotent Close and Detach
	if err := proc.Close(); err != nil {
		t.Fatalf("proc.Close: %v", err)
	}
	// Calling Close again must not error
	if err := proc.Close(); err != nil {
		t.Fatalf("idempotent proc.Close failed: %v", err)
	}
	if err := proc.Detach(); err != nil {
		t.Fatalf("proc.Detach failed: %v", err)
	}

	// Wait exit code
	code, err := proc.Wait()
	if err != nil {
		t.Logf("wait error (acceptable on close): %v", err)
	}
	_ = code
}
