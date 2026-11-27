package host

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// startEchoListener accepts connections and echoes bytes back.
func startEchoListener(t *testing.T, network, address string) string {
	t.Helper()
	listener, err := net.Listen(network, address)
	if err != nil {
		t.Fatalf("listen %s %s: %v", network, address, err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // test cleanup
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }() //nolint:errcheck // test cleanup
				_, _ = io.Copy(c, c)             //nolint:errcheck // echo
			}(conn)
		}
	}()
	return listener.Addr().String()
}

func portOf(t *testing.T, endpoint string) uint16 {
	t.Helper()
	_, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		t.Fatalf("split %s: %v", endpoint, err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port of %s: %v", endpoint, err)
	}
	return uint16(value)
}

func exchange(t *testing.T, conn net.Conn, payload string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if string(buf) != payload {
		t.Fatalf("echo mismatch: %q", buf)
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close() //nolint:errcheck // release the reserved address
	return address
}

func proxyWith(t *testing.T, configure func(p *testutil.FakeSOCKS5Proxy)) *testutil.FakeSOCKS5Proxy {
	t.Helper()
	return testutil.NewFakeSOCKS5Proxy(t, configure)
}

func TestSOCKS5DialSendsDomainsAndAddresses(t *testing.T) {
	v4Target := startEchoListener(t, "tcp", "127.0.0.1:0")

	relayTo := func(target string) func(testutil.SOCKS5Request) (net.Conn, error) {
		return func(testutil.SOCKS5Request) (net.Conn, error) {
			return net.DialTimeout("tcp", target, 2*time.Second)
		}
	}

	cases := []struct {
		name     string
		target   string
		relay    func(testutil.SOCKS5Request) (net.Conn, error)
		wantHost string
		wantPort uint16
		domain   bool
	}{
		{
			name:     "IPv4 literal is sent as an address",
			target:   v4Target,
			wantHost: "127.0.0.1",
			wantPort: portOf(t, v4Target),
		},
		{
			name:     "host name is sent as a domain name",
			target:   net.JoinHostPort("localhost", strconv.Itoa(int(portOf(t, v4Target)))),
			wantHost: "localhost",
			wantPort: portOf(t, v4Target),
			domain:   true,
		},
		{
			name:     "host name the local machine cannot resolve is left to the proxy",
			target:   "db.internal.example:2222",
			relay:    relayTo(v4Target),
			wantHost: "db.internal.example",
			wantPort: 2222,
			domain:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.DialTarget = tc.relay })

			conn, err := dialSOCKS5(context.Background(), proxy.Addr, tc.target, 2*time.Second)
			if err != nil {
				t.Fatalf("dial through proxy: %v", err)
			}
			defer func() { _ = conn.Close() }() //nolint:errcheck // test cleanup
			exchange(t, conn, "through the proxy")

			requests := proxy.Requests()
			if len(requests) != 1 {
				t.Fatalf("expected one CONNECT request, got %d", len(requests))
			}
			got := requests[0]
			if got.Host != tc.wantHost || got.Port != tc.wantPort || got.Domain != tc.domain {
				t.Fatalf("proxy received %+v, want host %q port %d domain=%v",
					got, tc.wantHost, tc.wantPort, tc.domain)
			}
		})
	}
}

func TestSOCKS5DialIPv6LiteralIsSentAsAnAddress(t *testing.T) {
	v6Listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	v6Target := v6Listener.Addr().String()
	_ = v6Listener.Close() //nolint:errcheck // re-bound by the echo listener
	echo := startEchoListener(t, "tcp6", v6Target)

	proxy := proxyWith(t, func(*testutil.FakeSOCKS5Proxy) {})
	conn, err := dialSOCKS5(context.Background(), proxy.Addr, echo, 2*time.Second)
	if err != nil {
		t.Fatalf("dial IPv6 target through proxy: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck // test cleanup
	exchange(t, conn, "ipv6")

	requests := proxy.Requests()
	if len(requests) != 1 || requests[0].Host != "::1" || requests[0].Domain || requests[0].Port != portOf(t, echo) {
		t.Fatalf("unexpected proxy request: %+v", requests)
	}
}

func TestSOCKS5FailureClassification(t *testing.T) {
	targetDown := func(testutil.SOCKS5Request) (net.Conn, error) {
		return nil, errors.New("target down")
	}

	type failure struct {
		name     string
		proxy    func(t *testing.T) string
		timeout  time.Duration
		want     ErrorCode
		wantText string
	}
	cases := []failure{
		{
			name:    "proxy not accepting connections",
			proxy:   func(t *testing.T) string { return unusedAddress(t) },
			timeout: time.Second,
			want:    ErrProxyUnreachable,
		},
		{
			name: "no reply to the negotiation",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Stall = true }).Addr
			},
			timeout: 300 * time.Millisecond,
			want:    ErrProxyUnreachable,
		},
		{
			name: "authentication method required",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.RequireAuth = true }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyAuthRequired,
		},
		{
			name: "closed without any reply",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.CloseAfterAccept = true }).Addr
			},
			timeout:  time.Second,
			want:     ErrProxyRefused,
			wantText: "closed during negotiation",
		},
		{
			name: "reply: general failure",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x01 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyRefused,
		},
		{
			name: "reply: not allowed by ruleset",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x02 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyRefused,
		},
		{
			name: "reply: TTL expired",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x06 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyRefused,
		},
		{
			name: "reply: command not supported",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x07 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyRefused,
		},
		{
			name: "reply: network unreachable",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x03 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyTargetUnreachable,
		},
		{
			name: "reply: host unreachable",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x04 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyTargetUnreachable,
		},
		{
			name: "reply: target connection refused",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Reply = 0x05 }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyTargetUnreachable,
		},
		{
			name: "target cannot be reached by the proxy",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.DialTarget = targetDown }).Addr
			},
			timeout: time.Second,
			want:    ErrProxyTargetUnreachable,
		},
		{
			name: "connect reply times out",
			proxy: func(t *testing.T) string {
				return proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.StallAfterRequest = true }).Addr
			},
			timeout: 300 * time.Millisecond,
			want:    ErrProxyTargetUnreachable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proxyAddress := tc.proxy(t)
			start := time.Now()
			conn, err := dialSOCKS5(context.Background(), proxyAddress, "10.9.8.7:22", tc.timeout)
			if conn != nil {
				_ = conn.Close() //nolint:errcheck // unexpected connection cleanup
				t.Fatal("expected no connection")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("failure took %s, beyond the dial budget %s", elapsed, tc.timeout)
			}

			code, proxy, ok := ProxyFailure(err)
			if !ok || code != tc.want || proxy != proxyAddress {
				t.Fatalf("ProxyFailure = %s, %q, %v; want %s, %q", code, proxy, ok, tc.want, proxyAddress)
			}
			if tc.wantText != "" && !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("diagnostic %q does not say %q", err.Error(), tc.wantText)
			}
			// The code survives being wrapped as a host error.
			wrapped := &Error{Code: code, Cause: err}
			if !errors.Is(wrapped, tc.want) {
				t.Fatalf("code is lost when wrapped in Error: %v", wrapped)
			}
		})
	}
}

func TestSOCKS5DialCancellation(t *testing.T) {
	proxy := proxyWith(t, func(p *testutil.FakeSOCKS5Proxy) { p.Stall = true })

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := dialSOCKS5(ctx, proxy.Addr, "10.9.8.7:22", 30*time.Second)
	if conn != nil {
		_ = conn.Close() //nolint:errcheck // unexpected connection cleanup
		t.Fatal("expected no connection")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the context deadline, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestNormalizeProxyAddress(t *testing.T) {
	accepted := map[string]string{
		"":                         "",
		"   ":                      "",
		"127.0.0.1:7890":           "127.0.0.1:7890",
		" 127.0.0.1:7890 ":         "127.0.0.1:7890",
		"socks5://127.0.0.1:7890":  "127.0.0.1:7890",
		"SOCKS5://127.0.0.1:7890":  "127.0.0.1:7890",
		"localhost:1080":           "localhost:1080",
		"proxy.lan:1080":           "proxy.lan:1080",
		"[::1]:7890":               "[::1]:7890",
		"socks5://[fe80::1]:7890":  "[fe80::1]:7890",
		"192.168.1.2:65535":        "192.168.1.2:65535",
		"socks5h://127.0.0.1:7890": "",
		"http://127.0.0.1:7890":    "",
		"127.0.0.1":                "",
		"127.0.0.1:0":              "",
		"127.0.0.1:65536":          "",
		"127.0.0.1:http":           "",
		"user:pass@127.0.0.1:7890": "",
		"127.0.0.1:7890/path":      "",
		"fe80::1:7890":             "",
		"my proxy:7890":            "",
		"-bad:7890":                "",
	}
	for raw, want := range accepted {
		got, err := NormalizeProxyAddress(raw)
		rejected := strings.TrimSpace(raw) != "" && want == ""
		if rejected {
			if !errors.Is(err, ErrInvalidProxyAddress) {
				t.Errorf("%q: expected ErrInvalidProxyAddress, got %q, %v", raw, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", raw, got, err, want)
		}
	}
}
