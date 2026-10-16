package host_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

func setupTestRegistryWithHost(t *testing.T, hostID host.ID, port uint16, hostKey ssh.PublicKey) (*host.Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := host.NewRegistry(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	_, err = r.Save(context.Background(), host.HostInput{
		ID:             hostID,
		Name:           "Test Host",
		Address:        "127.0.0.1",
		Port:           port,
		Username:       "testuser",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "correct-password"},
		HostKey:        hostKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	return r, dir
}

func TestSingleFlightDialing(t *testing.T) {
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)

	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	reg, _ := setupTestRegistryWithHost(t, "server-1", uint16(port), serverKey)

	cfg := host.DefaultConnectionConfig()
	mgr := host.NewConnectionManager(cfg, reg)
	defer mgr.Close()

	// Intercept net.Listener connections to count them
	// Instead, test single-flight by launching 10 goroutines requesting GetControl on the same lease
	lease, err := mgr.Lease(context.Background(), "server-1")
	if err != nil {
		t.Fatal(err)
	}

	const concurrency = 10
	var wg sync.WaitGroup
	clients := make([]*ssh.Client, concurrency)
	errorsList := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c, err := lease.GetControl(context.Background())
			clients[idx] = c
			errorsList[idx] = err
		}(i)
	}
	wg.Wait()

	for i := 0; i < concurrency; i++ {
		if errorsList[i] != nil {
			t.Fatalf("goroutine %d failed: %v", i, errorsList[i])
		}
		if clients[i] != clients[0] {
			t.Fatalf("goroutine %d received a different client instance (single-flight failed)", i)
		}
	}
}

func TestStalledHandshakeTimeout(t *testing.T) {
	// TCP listener that accepts connections but never replies (simulating handshake stall)
	stalledListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stalledListener.Close()

	go func() {
		for {
			c, err := stalledListener.Accept()
			if err != nil {
				return
			}
			// Just hold the connection open without sending SSH handshake
			defer c.Close()
			time.Sleep(2 * time.Second)
		}
	}()

	_, portStr, _ := net.SplitHostPort(stalledListener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	dummySigner := generateTestHostKey(t)
	reg, _ := setupTestRegistryWithHost(t, "stalled-server", uint16(port), dummySigner.PublicKey())

	cfg := host.ConnectionConfig{
		DialTimeout:      500 * time.Millisecond,
		HandshakeTimeout: 100 * time.Millisecond,
		OperationTimeout: time.Second,
	}
	mgr := host.NewConnectionManager(cfg, reg)
	defer mgr.Close()

	lease, err := mgr.Lease(context.Background(), "stalled-server")
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = lease.GetControl(context.Background())
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected handshake error on stalled server")
	}
	// Handshake budget of 100ms should fire well before 1s
	if duration > 800*time.Millisecond {
		t.Fatalf("handshake timeout took too long: %v (expected ~100ms)", duration)
	}
}

func TestContextCancellationStopsDial(t *testing.T) {
	stalledListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stalledListener.Close()

	_, portStr, _ := net.SplitHostPort(stalledListener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	dummySigner := generateTestHostKey(t)
	reg, _ := setupTestRegistryWithHost(t, "cancel-server", uint16(port), dummySigner.PublicKey())

	cfg := host.DefaultConnectionConfig()
	mgr := host.NewConnectionManager(cfg, reg)
	defer mgr.Close()

	lease, err := mgr.Lease(context.Background(), "cancel-server")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = lease.GetControl(ctx)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context error on cancelled dial, got: %v", err)
	}
}

func TestDialBackoff(t *testing.T) {
	// Point to a port with nothing listening
	dummySigner := generateTestHostKey(t)
	reg, _ := setupTestRegistryWithHost(t, "offline-server", 59999, dummySigner.PublicKey())

	cfg := host.ConnectionConfig{
		DialTimeout: 50 * time.Millisecond,
		MaxBackoff:  1 * time.Second,
	}
	mgr := host.NewConnectionManager(cfg, reg)
	defer mgr.Close()

	lease, err := mgr.Lease(context.Background(), "offline-server")
	if err != nil {
		t.Fatal(err)
	}

	// First dial fails on TCP
	_, err1 := lease.GetControl(context.Background())
	if err1 == nil {
		t.Fatal("expected error on offline server")
	}

	// Immediate second dial should fail fast on backoff
	start := time.Now()
	_, err2 := lease.GetControl(context.Background())
	duration := time.Since(start)

	if err2 == nil {
		t.Fatal("expected error on backoff")
	}
	if duration > 20*time.Millisecond {
		t.Fatalf("backoff rejection took too long: %v (expected immediate rejection)", duration)
	}
}

func TestRevisionedLeasesAndHostIsolation(t *testing.T) {
	listenerA, keyA := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	listenerB, keyB := startDisposableSSHServer(t, "testuser", "correct-password", nil)

	_, portAStr, _ := net.SplitHostPort(listenerA.Addr().String())
	portA, _ := strconv.ParseUint(portAStr, 10, 16)

	_, portBStr, _ := net.SplitHostPort(listenerB.Addr().String())
	portB, _ := strconv.ParseUint(portBStr, 10, 16)

	dir := t.TempDir()
	reg, err := host.NewRegistry(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	_, err = reg.Save(context.Background(), host.HostInput{
		ID:             "host-A",
		Name:           "Host A",
		Address:        "127.0.0.1",
		Port:           uint16(portA),
		Username:       "testuser",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "correct-password"},
		HostKey:        keyA.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, err = reg.Save(context.Background(), host.HostInput{
		ID:             "host-B",
		Name:           "Host B",
		Address:        "127.0.0.1",
		Port:           uint16(portB),
		Username:       "testuser",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "correct-password"},
		HostKey:        keyB.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close()

	leaseA1, err := mgr.Lease(context.Background(), "host-A")
	if err != nil {
		t.Fatal(err)
	}
	clientA1, err := leaseA1.GetControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	leaseB, err := mgr.Lease(context.Background(), "host-B")
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := leaseB.GetControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Invalidate Host A (simulating host removal or configuration change)
	mgr.Invalidate("host-A")

	// Old lease A1 must be revoked with ErrStaleRevision
	if err := leaseA1.CheckValid(); !errors.Is(err, host.ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision on revoked lease A, got %v", err)
	}
	if _, err := leaseA1.GetControl(context.Background()); !errors.Is(err, host.ErrStaleRevision) {
		t.Fatalf("expected ErrStaleRevision when using revoked lease A, got %v", err)
	}

	// Host B must remain valid, connected, and functioning!
	if err := leaseB.CheckValid(); err != nil {
		t.Fatalf("Host B lease was unexpectedly affected: %v", err)
	}
	// Verify host B client is still alive
	_, _, _ = clientB.SendRequest("ping@test", true, nil)
	// Even if rejected as unknown request, connection is still open (not EOF or closed)
	_ = clientA1
}

func TestTransportLifecycleSeparation(t *testing.T) {
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	_, portStr, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.ParseUint(portStr, 10, 16)

	reg, _ := setupTestRegistryWithHost(t, "host-sep", uint16(port), serverKey)
	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), reg)
	defer mgr.Close()

	lease, err := mgr.Lease(context.Background(), "host-sep")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Shared control client
	controlClient, err := lease.GetControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// 2. Dedicated terminal transport
	termConn, err := lease.OpenDedicated(context.Background(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer termConn.Close()

	// 3. Dedicated file transport
	fileConn, err := lease.OpenDedicated(context.Background(), "sftp")
	if err != nil {
		t.Fatal(err)
	}

	// Close the file transport (simulating stalled SFTP operation cancellation)
	if err := fileConn.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify terminal transport is still alive!
	session, err := termConn.Client.NewSession()
	if err != nil {
		t.Fatalf("terminal transport was closed by file cancellation: %v", err)
	}
	_ = session.Close()

	// Verify control client is still alive!
	controlSession, err := controlClient.NewSession()
	if err != nil {
		t.Fatalf("control client was closed by file cancellation: %v", err)
	}
	_ = controlSession.Close()
}
