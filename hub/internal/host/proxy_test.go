package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"golang.org/x/crypto/ssh"
)

func registryWithProxy(t *testing.T, proxyAddress string) *host.Registry {
	t.Helper()
	r, err := host.NewRegistry(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() }) //nolint:errcheck // test cleanup
	setProxy(t, r, proxyAddress)
	return r
}

func setProxy(t *testing.T, r *host.Registry, proxyAddress string) host.Settings {
	t.Helper()
	_, err := r.UpdateSettings(context.Background(), host.SettingsUpdate{ProxyAddress: &proxyAddress}, 0)
	if err == nil {
		t.Fatal("a missing revision was accepted")
	}
	current, err := r.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := r.UpdateSettings(
		context.Background(), host.SettingsUpdate{ProxyAddress: &proxyAddress}, current.Revision,
	)
	if err != nil {
		t.Fatalf("set proxy %q: %v", proxyAddress, err)
	}
	return settings
}

func registerHost(
	t *testing.T, r *host.Registry, id host.ID, address string, port uint16, hostKey ssh.PublicKey,
) {
	t.Helper()
	input := host.HostInput{
		ID:             id,
		Name:           "Host " + string(id),
		Address:        address,
		Port:           port,
		Username:       "testuser",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "correct-password"},
	}
	if hostKey != nil {
		input.HostKey = hostKey.Marshal()
	}
	if _, err := r.Save(context.Background(), input, 0); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

func proxyTestConfig() host.ConnectionConfig {
	return host.ConnectionConfig{
		DialTimeout:      2 * time.Second,
		HandshakeTimeout: 2 * time.Second,
		OperationTimeout: 5 * time.Second,
		MaxBackoff:       time.Second,
	}
}

func assertAllRequests(t *testing.T, proxy *testutil.FakeSOCKS5Proxy, want int, hostAddress string, port uint16) {
	t.Helper()
	requests := proxy.Requests()
	if len(requests) != want {
		t.Fatalf("proxy received %d CONNECT requests, want %d: %+v", len(requests), want, requests)
	}
	for _, req := range requests {
		if req.Host != hostAddress || req.Port != port {
			t.Fatalf("proxy received %+v, want %s:%d", req, hostAddress, port)
		}
	}
}

// countingRelay forwards connections to target and counts how many it accepted.
func countingRelay(t *testing.T, target string) (string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // test cleanup
	var accepted atomic.Int32
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			upstream, err := net.Dial("tcp", target)
			if err != nil {
				_ = client.Close() //nolint:errcheck // relay cleanup
				continue
			}
			go pipe(client, upstream)
		}
	}()
	return listener.Addr().String(), &accepted
}

func pipe(a, b net.Conn) {
	defer func() {
		_ = a.Close() //nolint:errcheck // relay cleanup
		_ = b.Close() //nolint:errcheck // relay cleanup
	}()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(b, a) //nolint:errcheck // test relay
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(a, b) //nolint:errcheck // test relay
		done <- struct{}{}
	}()
	<-done
}

func unusedPort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close() //nolint:errcheck // release the reserved port
	value, err := strconv.ParseUint(portOnly(t, address), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return uint16(value)
}

func portOnly(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestProxySettingRoutesControlTerminalAndFileConnections(t *testing.T) {
	ctx := context.Background()
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	port := uint16(0)
	if value, err := strconv.ParseUint(portOnly(t, listener.Addr().String()), 10, 16); err == nil {
		port = uint16(value)
	} else {
		t.Fatal(err)
	}

	proxy := testutil.NewFakeSOCKS5Proxy(t)
	reg := registryWithProxy(t, proxy.Addr)
	registerHost(t, reg, "via-proxy", "127.0.0.1", port, serverKey)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(ctx, "via-proxy")
	if err != nil {
		t.Fatal(err)
	}
	control, err := lease.GetControl(ctx)
	if err != nil {
		t.Fatalf("control connection through proxy: %v", err)
	}
	if _, err := lease.OpenDedicated(ctx, "terminal"); err != nil {
		t.Fatalf("terminal connection through proxy: %v", err)
	}
	if _, err := lease.OpenDedicated(ctx, "sftp"); err != nil {
		t.Fatalf("file connection through proxy: %v", err)
	}
	assertAllRequests(t, proxy, 3, "127.0.0.1", port)

	// A lost terminal is re-established on a new connection, also through the proxy.
	term, err := lease.OpenDedicated(ctx, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.OpenDedicated(ctx, "terminal"); err != nil {
		t.Fatalf("reconnect through proxy: %v", err)
	}
	assertAllRequests(t, proxy, 5, "127.0.0.1", port)

	// The shared control connection stays usable.
	if session, err := control.NewSession(); err != nil {
		t.Fatalf("control connection: %v", err)
	} else {
		_ = session.Close() //nolint:errcheck // test cleanup
	}
}

func TestProxyResolvesHostNamesForTheApplication(t *testing.T) {
	ctx := context.Background()
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	port, err := strconv.ParseUint(portOnly(t, listener.Addr().String()), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	// Only the proxy can reach the target under the name the host is registered with.
	proxy := testutil.NewFakeSOCKS5Proxy(t, func(p *testutil.FakeSOCKS5Proxy) {
		p.DialTarget = func(testutil.SOCKS5Request) (net.Conn, error) {
			return net.Dial("tcp", listener.Addr().String())
		}
	})
	reg := registryWithProxy(t, proxy.Addr)
	registerHost(t, reg, "by-name", "db.internal.example", uint16(port), serverKey)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(ctx, "by-name")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.GetControl(ctx); err != nil {
		t.Fatalf("connect by unresolvable name through proxy: %v", err)
	}

	requests := proxy.Requests()
	if len(requests) != 1 || requests[0].Host != "db.internal.example" || !requests[0].Domain {
		t.Fatalf("the host name was not passed to the proxy for resolution: %+v", requests)
	}
}

func TestProbeThroughProxyPresentsTheServersKey(t *testing.T) {
	ctx := context.Background()
	server := startAuthCountingSSHServer(t)
	proxy := testutil.NewFakeSOCKS5Proxy(t)

	directKey, err := host.ProbeEndpoint(ctx, "127.0.0.1", server.port, 2*time.Second)
	if err != nil {
		t.Fatalf("direct probe: %v", err)
	}
	proxyKey, err := host.ProbeEndpointViaProxy(ctx, "127.0.0.1", server.port, 2*time.Second, proxy.Addr)
	if err != nil {
		t.Fatalf("probe through proxy: %v", err)
	}
	if !bytes.Equal(proxyKey.Marshal(), directKey.Marshal()) ||
		!bytes.Equal(proxyKey.Marshal(), server.key.Marshal()) {
		t.Fatal("probe through proxy presented a different key than a direct probe")
	}
	if server.attempts.Load() != 0 {
		t.Fatalf("probe offered credentials; attempts=%d", server.attempts.Load())
	}
	assertAllRequests(t, proxy, 1, "127.0.0.1", server.port)
}

func TestConfiguredProxyIsNeverBypassed(t *testing.T) {
	ctx := context.Background()
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	targetAddress, targetConnections := countingRelay(t, listener.Addr().String())
	targetPort, err := strconv.ParseUint(portOnly(t, targetAddress), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing listens at the proxy address, while the target itself is reachable.
	deadProxy := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(unusedPort(t))))
	reg := registryWithProxy(t, deadProxy)
	registerHost(t, reg, "guarded", "127.0.0.1", uint16(targetPort), serverKey)

	cfg := proxyTestConfig()
	cfg.DialTimeout = 300 * time.Millisecond
	mgr := host.NewConnectionManager(cfg, reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	// The capability check is what "test connection" uses.
	start := time.Now()
	caps, err := mgr.CheckCapabilities(ctx, "guarded")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("failure took %s, beyond the dial budget", elapsed)
	}
	if caps.Online || caps.Error != "proxy_unreachable" || caps.SSH.Error != "proxy_unreachable" {
		t.Fatalf("expected the proxy to be reported as unreachable, got %+v", caps)
	}
	if caps.ConnectionPath == nil || caps.ConnectionPath.Mode != "proxy" || caps.ConnectionPath.Address != deadProxy {
		t.Fatalf("status does not name the proxy as the connection path: %+v", caps.ConnectionPath)
	}

	// The backoff of the failed check passes, then a terminal connection fails the same way.
	time.Sleep(600 * time.Millisecond)
	lease, err := mgr.Lease(ctx, "guarded")
	if err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	_, err = lease.OpenDedicated(ctx, "terminal")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("failure took %s, beyond the dial budget", elapsed)
	}
	if !errors.Is(err, host.ErrProxyUnreachable) {
		t.Fatalf("expected the proxy to be reported as unreachable, got %v", err)
	}
	if code, proxy, ok := host.ProxyFailure(err); !ok || code != host.ErrProxyUnreachable || proxy != deadProxy {
		t.Fatalf("proxy failure = %s, %q, %v", code, proxy, ok)
	}

	// A probe of the same endpoint is limited to the proxy too.
	if _, err := host.ProbeEndpointViaProxy(
		ctx, "127.0.0.1", uint16(targetPort), 300*time.Millisecond, deadProxy,
	); !errors.Is(err, host.ErrProxyUnreachable) {
		t.Fatalf("probe: expected the proxy to be reported as unreachable, got %v", err)
	}

	if targetConnections.Load() != 0 {
		t.Fatalf("the target received %d direct connections although a proxy is configured",
			targetConnections.Load())
	}
}

func TestReconnectAfterProxyStopsTargetsTheProxyOnly(t *testing.T) {
	ctx := context.Background()
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	targetAddress, targetConnections := countingRelay(t, listener.Addr().String())
	targetPort, err := strconv.ParseUint(portOnly(t, targetAddress), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	proxy := testutil.NewFakeSOCKS5Proxy(t)
	reg := registryWithProxy(t, proxy.Addr)
	registerHost(t, reg, "reconnecting", "127.0.0.1", uint16(targetPort), serverKey)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(ctx, "reconnecting")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.GetControl(ctx); err != nil {
		t.Fatalf("connect through running proxy: %v", err)
	}
	assertAllRequests(t, proxy, 1, "127.0.0.1", uint16(targetPort))
	if targetConnections.Load() != 1 {
		t.Fatalf("expected one proxied connection to reach the target, got %d", targetConnections.Load())
	}

	// The proxy goes away; the connection is lost and the application reconnects.
	proxy.Close()
	mgr.Invalidate("reconnecting")

	lease, err = mgr.Lease(ctx, "reconnecting")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = lease.GetControl(ctx)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("reconnect attempt took %s, beyond the dial budget", elapsed)
	}
	if !errors.Is(err, host.ErrProxyUnreachable) {
		t.Fatalf("expected the proxy to be reported as unreachable, got %v", err)
	}
	code, proxyAddress, ok := host.ProxyFailure(err)
	if !ok || code != host.ErrProxyUnreachable || proxyAddress != proxy.Addr {
		t.Fatalf("proxy failure = %s, %q, %v", code, proxyAddress, ok)
	}
	if targetConnections.Load() != 1 {
		t.Fatalf("reconnect reached the target directly %d times", targetConnections.Load()-1)
	}
}

func TestProxyChangeKeepsActiveConnectionsAndAppliesToNewOnes(t *testing.T) {
	ctx := context.Background()
	listener, serverKey := startDisposableSSHServer(t, "testuser", "correct-password", nil)
	port, err := strconv.ParseUint(portOnly(t, listener.Addr().String()), 10, 16)
	if err != nil {
		t.Fatal(err)
	}

	first := testutil.NewFakeSOCKS5Proxy(t)
	second := testutil.NewFakeSOCKS5Proxy(t)
	reg := registryWithProxy(t, first.Addr)
	registerHost(t, reg, "switching", "127.0.0.1", uint16(port), serverKey)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(ctx, "switching")
	if err != nil {
		t.Fatal(err)
	}
	control, err := lease.GetControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dedicated, err := lease.OpenDedicated(ctx, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	assertAllRequests(t, first, 2, "127.0.0.1", uint16(port))

	// Change the proxy: existing connections stay attached.
	setProxy(t, reg, second.Addr)

	// The terminal and the control connection keep working.
	if session, err := dedicated.Client.NewSession(); err != nil {
		t.Fatalf("open terminal was interrupted by the proxy change: %v", err)
	} else {
		_ = session.Close() //nolint:errcheck // test cleanup
	}
	if session, err := control.NewSession(); err != nil {
		t.Fatalf("control connection was interrupted by the proxy change: %v", err)
	} else {
		_ = session.Close() //nolint:errcheck // test cleanup
	}

	// A new connection takes the new route.
	if _, err := lease.OpenDedicated(ctx, "terminal"); err != nil {
		t.Fatalf("new connection after proxy change: %v", err)
	}
	assertAllRequests(t, second, 1, "127.0.0.1", uint16(port))
	assertAllRequests(t, first, 2, "127.0.0.1", uint16(port))

	// Clearing the proxy returns new connections to the direct route.
	setProxy(t, reg, "")
	if _, err := lease.OpenDedicated(ctx, "terminal"); err != nil {
		t.Fatalf("new connection after clearing the proxy: %v", err)
	}
	assertAllRequests(t, second, 1, "127.0.0.1", uint16(port))
	assertAllRequests(t, first, 2, "127.0.0.1", uint16(port))
}

func TestTrustChecksAreUnchangedThroughProxy(t *testing.T) {
	ctx := context.Background()
	server := startAuthCountingSSHServer(t)
	other := startAuthCountingSSHServer(t)
	proxy := testutil.NewFakeSOCKS5Proxy(t)
	reg := registryWithProxy(t, proxy.Addr)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	// A key that was never confirmed is not trusted because a proxy is in use.
	registerHost(t, reg, "unconfirmed", "127.0.0.1", server.port, nil)
	lease, err := mgr.Lease(ctx, "unconfirmed")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lease.GetControl(ctx)
	if !errors.Is(err, host.ErrTrustRequired) {
		t.Fatalf("expected trust to be required through a proxy, got %v", err)
	}
	if code, via, isProxy := host.ProxyFailure(err); isProxy {
		t.Fatalf("trust failure was reported as a proxy failure: %v (%v via %s)", err, code, via)
	}

	// A key that changed is refused before any credential is offered.
	registerHost(t, reg, "changed-key", "127.0.0.1", server.port, other.key)
	lease, err = mgr.Lease(ctx, "changed-key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = lease.GetControl(ctx)
	if !errors.Is(err, host.ErrHostKeyMismatch) {
		t.Fatalf("expected a server identity mismatch through a proxy, got %v", err)
	}
	if code, via, isProxy := host.ProxyFailure(err); isProxy {
		t.Fatalf("identity mismatch was reported as a proxy failure: %v (%v via %s)", err, code, via)
	}

	// The mismatched connection was really made through the proxy.
	assertAllRequests(t, proxy, 1, "127.0.0.1", server.port)
	if server.attempts.Load() != 0 {
		t.Fatalf("credentials were offered before the server key was accepted; attempts=%d", server.attempts.Load())
	}
}

// authCountingServer is an SSH server that counts every authentication attempt.
type authCountingServer struct {
	key      ssh.PublicKey
	port     uint16
	attempts *atomic.Int64
}

func startAuthCountingSSHServer(t *testing.T) *authCountingServer {
	t.Helper()
	signer := generateTestHostKey(t)
	attempts := &atomic.Int64{}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			attempts.Add(1)
			return nil, errors.New("auth disabled during probe test")
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			attempts.Add(1)
			return nil, errors.New("auth disabled during probe test")
		},
	}
	serverConfig.AddHostKey(signer)

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
				sshConn, _, _, _ := ssh.NewServerConn(c, serverConfig) //nolint:errcheck // handshake probe
				if sshConn != nil {
					_ = sshConn.Close() //nolint:errcheck // close conn
				}
			}(tcpConn)
		}
	}()

	port, err := strconv.ParseUint(portOnly(t, listener.Addr().String()), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return &authCountingServer{key: signer.PublicKey(), port: uint16(port), attempts: attempts}
}

func TestProxySettingDoesNotAffectLocalHost(t *testing.T) {
	ctx := context.Background()
	deadProxy := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(unusedPort(t))))
	reg := registryWithProxy(t, deadProxy)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	lease, err := mgr.Lease(ctx, host.LocalID)
	if err != nil {
		t.Fatalf("local lease with a proxy configured: %v", err)
	}
	if err := lease.CheckValid(); err != nil {
		t.Fatalf("local lease is not valid: %v", err)
	}

	caps, err := mgr.CheckCapabilities(ctx, host.LocalID)
	if err != nil {
		t.Fatalf("local status with a proxy configured: %v", err)
	}
	if !caps.Online || caps.Error != "" {
		t.Fatalf("local host was affected by the proxy setting: %+v", caps)
	}
	if caps.ConnectionPath != nil {
		t.Fatalf("local host must not report a connection path: %+v", caps.ConnectionPath)
	}
}

func TestCapabilityReportNamesADirectConnection(t *testing.T) {
	ctx := context.Background()
	reg, err := host.NewRegistry(ctx, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close() //nolint:errcheck // test cleanup
	registerHost(t, reg, "direct-host", "192.0.2.10", 22, nil)

	mgr := host.NewConnectionManager(proxyTestConfig(), reg)
	defer mgr.Close() //nolint:errcheck // test cleanup

	caps, err := mgr.CheckCapabilities(ctx, "direct-host")
	if err != nil {
		t.Fatal(err)
	}
	if caps.ConnectionPath == nil || caps.ConnectionPath.Mode != "direct" || caps.ConnectionPath.Address != "" {
		t.Fatalf("expected a direct connection path, got %+v", caps.ConnectionPath)
	}
	encoded, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"connectionPath":{"mode":"direct"}`) {
		t.Fatalf("unexpected report: %s", encoded)
	}
}
