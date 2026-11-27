package testutil

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The fake proxy speaks RFC 1928; these are the fields of that protocol.
const (
	socks5Version        = 0x05
	socks5NoAuth         = 0x00
	socks5NoAuthOK       = 0x00
	socks5NoneOK         = 0xFF
	socks5ReplySucceed   = 0x00
	socks5ReplyRefused   = 0x04
	socks5AtypIPv4       = 0x01
	socks5AtypIPv6       = 0x04
	socks5AtypDomain     = 0x03
	socks5CommandConnect = 0x01
	socks5Reserved       = 0x00
	socks5PortLength     = 2
	socks5BoundLoopback  = 127

	socks5IPv4ReplyLength = 10 // VER REP RSV ATYP BND.ADDR BND.PORT
	relayCopiers          = 2
	fixtureConnTimeout    = 30 * time.Second
	fixtureDialTimeout    = 5 * time.Second
)

// SOCKS5Request is one CONNECT request a FakeSOCKS5Proxy received.
type SOCKS5Request struct {
	Host   string
	Port   uint16
	Domain bool // true when the client sent the host as a domain name
}

// FakeSOCKS5Proxy is a disposable in-process SOCKS5 proxy. By default it
// relays to the requested target; the knobs make it fail the ways a real proxy
// does. All state is private to the test that creates it.
type FakeSOCKS5Proxy struct {
	Listener net.Listener
	Addr     string // host:port to use as a proxy setting

	// DialTarget opens the connection to the requested target. Nil dials the
	// requested host and port directly.
	DialTarget func(req SOCKS5Request) (net.Conn, error)
	// Reply, when non-zero, is sent as the reply code instead of connecting.
	Reply byte
	// RequireAuth makes the proxy reject the no-authentication method.
	RequireAuth bool
	// Stall accepts connections and never answers the negotiation.
	Stall bool
	// CloseAfterAccept closes accepted connections without any reply.
	CloseAfterAccept bool
	// StallAfterRequest answers the negotiation and then never answers the
	// CONNECT request.
	StallAfterRequest bool

	mu       sync.Mutex
	requests []SOCKS5Request
	conns    []net.Conn
	closed   bool
}

// NewFakeSOCKS5Proxy starts a proxy on a loopback port and closes it at test
// end. The behavior knobs must be set in configure, before any connection can
// be accepted; they are read by the serving goroutines.
func NewFakeSOCKS5Proxy(t *testing.T, configure ...func(*FakeSOCKS5Proxy)) *FakeSOCKS5Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake socks5 listen: %v", err)
	}
	p := &FakeSOCKS5Proxy{Listener: listener}
	for _, apply := range configure {
		apply(p)
	}
	p.Addr = listener.Addr().String()
	t.Cleanup(p.Close)
	go p.serve()
	return p
}

// Requests returns the CONNECT requests received so far.
func (p *FakeSOCKS5Proxy) Requests() []SOCKS5Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]SOCKS5Request(nil), p.requests...)
}

// Close stops the proxy and drops every connection it holds.
func (p *FakeSOCKS5Proxy) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	_ = p.Listener.Close() //nolint:errcheck // test proxy cleanup
	for _, c := range p.conns {
		_ = c.Close() //nolint:errcheck // test proxy cleanup
	}
	p.conns = nil
	p.mu.Unlock()
}

func (p *FakeSOCKS5Proxy) serve() {
	for {
		conn, err := p.Listener.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		closed := p.closed
		if !closed {
			p.conns = append(p.conns, conn)
		}
		p.mu.Unlock()
		if closed {
			_ = conn.Close() //nolint:errcheck // closing on stopped proxy
			return
		}
		go p.handle(conn)
	}
}

func (p *FakeSOCKS5Proxy) dropConn(conn net.Conn) {
	p.mu.Lock()
	for i, c := range p.conns {
		if c == conn {
			p.conns = append(p.conns[:i], p.conns[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
}

func (p *FakeSOCKS5Proxy) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }() //nolint:errcheck // proxy connection cleanup
	defer p.dropConn(conn)

	if p.CloseAfterAccept {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(fixtureConnTimeout)) //nolint:errcheck // test guard

	version, err := readByte(conn)
	if err != nil || version != socks5Version {
		return
	}
	methodCount, err := readByte(conn)
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, conn, int64(methodCount)); err != nil {
		return
	}
	if p.Stall {
		// Hold the connection silently until the client gives up on it.
		_, _ = io.Copy(io.Discard, conn) //nolint:errcheck // stalled negotiation
		return
	}
	if p.RequireAuth {
		_, _ = conn.Write([]byte{socks5Version, socks5NoneOK}) //nolint:errcheck // scripted reply
		return
	}
	if _, err := conn.Write([]byte{socks5Version, socks5NoAuthOK}); err != nil {
		return
	}

	req, err := readConnectRequest(conn)
	if err != nil {
		return
	}
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	if p.StallAfterRequest {
		_, _ = io.Copy(io.Discard, conn) //nolint:errcheck // stalled connect reply
		return
	}
	if p.Reply != 0 {
		_, _ = conn.Write(socks5Reply(p.Reply)) //nolint:errcheck // scripted reply
		return
	}

	dial := p.DialTarget
	if dial == nil {
		dial = func(r SOCKS5Request) (net.Conn, error) {
			return net.DialTimeout("tcp", net.JoinHostPort(r.Host, strconv.Itoa(int(r.Port))), fixtureDialTimeout)
		}
	}
	target, err := dial(req)
	if err != nil {
		_, _ = conn.Write(socks5Reply(socks5ReplyRefused)) //nolint:errcheck // host unreachable
		return
	}
	defer func() { _ = target.Close() }() //nolint:errcheck // relay cleanup
	if _, err := conn.Write(socks5Reply(socks5ReplySucceed)); err != nil {
		return
	}
	relay(conn, target)
}

// socks5Reply builds a reply whose bound address is the loopback address.
func socks5Reply(rep byte) []byte {
	head := []byte{socks5Version, rep, socks5Reserved, socks5AtypIPv4}
	bound := append(head, net.IPv4(socks5BoundLoopback, 0, 0, 1).To4()...)
	return binary.BigEndian.AppendUint16(bound, 0)
}

func relay(a, b net.Conn) {
	done := make(chan struct{}, relayCopiers)
	copy := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src) //nolint:errcheck // test relay
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite() //nolint:errcheck // propagate EOF to the peer
		}
		done <- struct{}{}
	}
	go copy(a, b)
	go copy(b, a)
	<-done
}

func readByte(conn net.Conn) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(conn, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func readConnectRequest(conn net.Conn) (SOCKS5Request, error) {
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return SOCKS5Request{}, err
	}
	if head[0] != socks5Version || head[1] != socks5CommandConnect || head[2] != socks5Reserved {
		return SOCKS5Request{}, errors.New("not a SOCKS5 CONNECT request")
	}
	req := SOCKS5Request{}
	switch head[3] {
	case socks5AtypIPv4:
		var addr [net.IPv4len]byte
		if _, err := io.ReadFull(conn, addr[:]); err != nil {
			return req, err
		}
		req.Host = net.IP(addr[:]).String()
	case socks5AtypIPv6:
		var addr [net.IPv6len]byte
		if _, err := io.ReadFull(conn, addr[:]); err != nil {
			return req, err
		}
		req.Host = net.IP(addr[:]).String()
	case socks5AtypDomain:
		length, err := readByte(conn)
		if err != nil {
			return req, err
		}
		name := make([]byte, length)
		if _, err := io.ReadFull(conn, name); err != nil {
			return req, err
		}
		req.Host = string(name)
		req.Domain = true
	default:
		return req, errors.New("unknown address type")
	}
	var port [socks5PortLength]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return req, err
	}
	req.Port = binary.BigEndian.Uint16(port[:])
	return req, nil
}
