package host_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	ws "github.com/AK12-Official/visual-tmux-client/hub/internal/transport/ws"
	"github.com/coder/websocket"
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
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // test cleanup

	go func() {
		for {
			tcp, err := listener.Accept()
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
						state.mu.Lock()
						hangChan := state.hangChannelOpen
						state.mu.Unlock()
						if hangChan {
							continue
						}
						channel, requests, err := newChannel.Accept()
						if err != nil {
							continue
						}
						go state.handleSession(channel, requests)
					}
				}()
				_ = sshConn.Wait() //nolint:errcheck // wait
			}(tcp)
		}
	}()

	return listener, signer.PublicKey(), state
}

type mockPTYServerState struct {
	mu              sync.Mutex
	ptyRequested    bool
	termType        string
	rows, cols      uint32
	windowResizes   []struct{ cols, rows uint32 }
	receivedBytes   bytes.Buffer
	hangChannelOpen bool
	hangPTY         bool
	hangExec        bool
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
			hang := s.hangPTY
			s.mu.Unlock()
			if hang {
				continue
			}
			_ = req.Reply(true, nil) //nolint:errcheck // reply

		case "window-change":
			if len(req.Payload) >= 8 {
				cols := binary.BigEndian.Uint32(req.Payload[:4])
				rows := binary.BigEndian.Uint32(req.Payload[4:8])
				s.mu.Lock()
				s.windowResizes = append(s.windowResizes, struct{ cols, rows uint32 }{cols, rows})
				s.mu.Unlock()
			}
			_ = req.Reply(true, nil) //nolint:errcheck // reply

		case "exec":
			s.mu.Lock()
			hang := s.hangExec
			s.mu.Unlock()
			if hang {
				continue
			}
			_ = req.Reply(true, nil) //nolint:errcheck // reply
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
						_, _ = ch.Write(buf[:n]) //nolint:errcheck // echo
					}
					if err != nil {
						break
					}
				}
				statusMsg := struct{ Status uint32 }{Status: 0}
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(&statusMsg)) //nolint:errcheck // status
				_ = ch.Close()                                                       //nolint:errcheck // close
			}()

		default:
			_ = req.Reply(false, nil) //nolint:errcheck // reject
		}
	}
}

func TestRemotePTYProcess(t *testing.T) {
	listener, hostKey, state := startMockPTYSSHServer(t)
	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	reg, _ := setupTestRegistryWithHost(t, "pty-host", uint16(port), hostKey)
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(context.Background(), "pty-host")
	if err != nil {
		t.Fatal(err)
	}

	factory := host.NewRemoteProcessFactory(lease, "tmux")
	proc, err := factory.NewProcess(context.Background(), "test-session", 100, 30)
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}
	defer proc.Close() //nolint:errcheck // test cleanup

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

type mockReleaseResolver struct {
	rt host.Runtime
}

func (r mockReleaseResolver) Resolve(context.Context, host.ID) (host.Runtime, error) {
	return r.rt, nil
}

type mockReleaseRedeemer struct{}

func (mockReleaseRedeemer) Redeem(string, string) error { return nil }

func TestRemoteTerminalSurvivesServeHTTPReturn(t *testing.T) {
	listener, key, _ := startMockPTYSSHServer(t)
	_, ps, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(ps, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := setupTestRegistryWithHost(t, "remote-term", uint16(port), key)
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(context.Background(), "remote-term")
	if err != nil {
		t.Fatal(err)
	}
	tm := terminal.NewManager(time.Second)
	defer tm.CloseHost("remote-term")

	h := ws.NewHandlerWithResolver(
		mockReleaseRedeemer{},
		mockReleaseResolver{host.Runtime{
			Target:    lease.Target,
			Processes: host.NewRemoteProcessFactory(lease, ""),
		}},
		tm,
		ws.DefaultOptions(),
		nil,
		nil,
	)

	returned := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("hostId", "remote-term")
		r.SetPathValue("session", "demo")
		h.ServeHTTP(w, r)
		close(returned)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow() //nolint:errcheck // test cleanup

	// Wait for ServeHTTP to return (cancelling r.Context() in net/http!)
	<-returned

	// Read initial frames: first is JSON ready frame
	msgType, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed reading ready frame after ServeHTTP returned: %v", err)
	}
	if msgType != websocket.MessageText || !strings.Contains(string(data), `"type":"ready"`) {
		t.Fatalf("unexpected frame: %v, data: %s", msgType, string(data))
	}

	// Send input over websocket
	testInput := []byte("echo hello\r")
	if err := conn.Write(ctx, websocket.MessageBinary, testInput); err != nil {
		t.Fatalf("failed writing to terminal after ServeHTTP returned: %v", err)
	}

	// Verify terminal remains alive and readable
	readCtx, readCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer readCancel()
	_, echoData, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("failed reading echo from terminal after ServeHTTP returned: %v", err)
	}
	if !bytes.Contains(echoData, []byte("echo hello")) {
		t.Logf("received output: %q", string(echoData))
	}

	// Normal closure when client disconnects
	_ = conn.Close(websocket.StatusNormalClosure, "done") //nolint:errcheck // test cleanup
}

func TestRemoteTerminalSetupCancellation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(s *mockPTYServerState)
	}{
		{
			name: "hang_channel_open",
			configure: func(s *mockPTYServerState) {
				s.mu.Lock()
				s.hangChannelOpen = true
				s.mu.Unlock()
			},
		},
		{
			name: "hang_pty",
			configure: func(s *mockPTYServerState) {
				s.mu.Lock()
				s.hangPTY = true
				s.mu.Unlock()
			},
		},
		{
			name: "hang_exec",
			configure: func(s *mockPTYServerState) {
				s.mu.Lock()
				s.hangExec = true
				s.mu.Unlock()
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, hostKey, state := startMockPTYSSHServer(t)
			_, portStr, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				t.Fatal(err)
			}

			reg, _ := setupTestRegistryWithHost(t, "cancel-term", uint16(port), hostKey)
			mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
			defer mgr.Close() //nolint:errcheck // test cleanup

			lease, err := mgr.Lease(context.Background(), "cancel-term")
			if err != nil {
				t.Fatal(err)
			}

			tc.configure(state)

			factory := host.NewRemoteProcessFactory(lease, "tmux")
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			start := time.Now()
			proc, err := factory.NewProcess(ctx, "test-sess", 80, 24)
			elapsed := time.Since(start)

			if err == nil {
				if proc != nil {
					_ = proc.Close() //nolint:errcheck // cleanup
				}
				t.Fatal("expected NewProcess to fail on hung setup")
			}

			// Must unblock promptly (< 400ms) once context deadline expires (50ms)
			if elapsed > 400*time.Millisecond {
				t.Fatalf("setup cancellation hung for %v (expected < 400ms)", elapsed)
			}

			if !errors.Is(err, context.DeadlineExceeded) &&
				!strings.Contains(err.Error(), "timed out or cancelled") &&
				!strings.Contains(err.Error(), "context deadline exceeded") {
				t.Fatalf("unexpected error message: %v", err)
			}
		})
	}
}

func TestRemoteTerminalHandoverImmunity(t *testing.T) {
	listener, hostKey, _ := startMockPTYSSHServer(t)
	_, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	reg, _ := setupTestRegistryWithHost(t, "handover-term", uint16(port), hostKey)
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(context.Background(), "handover-term")
	if err != nil {
		t.Fatal(err)
	}

	factory := host.NewRemoteProcessFactory(lease, "tmux")
	setupCtx, cancelSetup := context.WithCancel(context.Background())

	proc, err := factory.NewProcess(setupCtx, "handover-sess", 80, 24)
	if err != nil {
		t.Fatalf("NewProcess: %v", err)
	}
	defer proc.Close() //nolint:errcheck // test cleanup

	// Cancel the setup context immediately after NewProcess returns.
	// Since ownership was handed over to the attachment, this MUST NOT terminate the process.
	cancelSetup()

	// Verify terminal remains readable and writable
	payload := []byte("handover echo\n")
	if _, err := proc.Write(payload); err != nil {
		t.Fatalf("proc.Write failed after setupCtx cancelled: %v", err)
	}

	readBuf := make([]byte, len(payload))
	if _, err := io.ReadFull(proc, readBuf); err != nil {
		t.Fatalf("proc.Read failed after setupCtx cancelled: %v", err)
	}
	if !bytes.Equal(readBuf, payload) {
		t.Fatalf("got %q, want %q", readBuf, payload)
	}
}
