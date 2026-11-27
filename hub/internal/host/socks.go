package host

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Proxy failure codes. Each names who failed: the proxy itself
// (unreachable, authentication required, refused) or the target behind it.
const (
	ErrProxyUnreachable       ErrorCode = "proxy_unreachable"
	ErrProxyAuthRequired      ErrorCode = "proxy_auth_required"
	ErrProxyRefused           ErrorCode = "proxy_refused"
	ErrProxyTargetUnreachable ErrorCode = "proxy_target_unreachable"
)

// ProxyError is a failure of the configured proxy route. Proxy is the proxy
// address the connection was sent to; it never carries a credential because
// the setting cannot contain one.
type ProxyError struct {
	Code  ErrorCode
	Proxy string
	Err   error
}

func (e *ProxyError) Error() string {
	return fmt.Sprintf("%s: proxy %s: %v", e.Code, e.Proxy, e.Err)
}

func (e *ProxyError) Unwrap() error        { return e.Err }
func (e *ProxyError) Is(target error) bool { return target == e.Code }

// ProxyFailure reports the proxy failure in err's chain, if any.
func ProxyFailure(err error) (code ErrorCode, proxy string, ok bool) {
	var pe *ProxyError
	if errors.As(err, &pe) {
		return pe.Code, pe.Proxy, true
	}
	return "", "", false
}

func proxyFailure(code ErrorCode, proxy string, format string, args ...any) error {
	return &ProxyError{Code: code, Proxy: proxy, Err: fmt.Errorf(format, args...)}
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// dialRemote opens the transport to a remote endpoint. An empty proxyAddress
// dials directly; otherwise the connection goes through that SOCKS5 proxy and
// no direct connection to the endpoint is ever attempted.
func dialRemote(ctx context.Context, endpoint, proxyAddress string, timeout time.Duration) (net.Conn, error) {
	if proxyAddress == "" {
		return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", endpoint)
	}
	return dialSOCKS5(ctx, proxyAddress, endpoint, timeout)
}

// dialSOCKS5 performs SOCKS5 CONNECT without authentication (RFC 1928). Host
// names are sent as domain names so the proxy resolves them; IP literals are
// sent as addresses. timeout is one budget for reaching the proxy, negotiating
// and receiving the connect reply.
func dialSOCKS5(ctx context.Context, proxyAddress, endpoint string, timeout time.Duration) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, proxyFailure(ErrProxyTargetUnreachable, proxyAddress, "invalid target %q", endpoint)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		return nil, proxyFailure(ErrProxyTargetUnreachable, proxyAddress, "invalid target port %q", portText)
	}

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, proxyFailure(ErrProxyUnreachable, proxyAddress, "connect: %w", err)
	}

	if err := socksConnect(ctx, conn, host, uint16(port), proxyAddress, timeout); err != nil {
		_ = conn.Close() //nolint:errcheck // failed proxy connection cleanup
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = conn.Close() //nolint:errcheck // unusable proxy connection cleanup
		return nil, proxyFailure(ErrProxyRefused, proxyAddress, "clear deadline: %w", err)
	}
	return conn, nil
}

func socksConnect(
	ctx context.Context, conn net.Conn, host string, port uint16, proxyAddress string, timeout time.Duration,
) error {
	// The connection deadline enforces only the dial budget. An earlier
	// context deadline is enforced by closing the connection below: a read
	// then ends with the closed connection, never with a deadline of its own
	// that would race the context and be taken for a proxy that does not answer.
	deadline := time.Now().Add(timeout)
	if err := conn.SetDeadline(deadline); err != nil {
		return proxyFailure(ErrProxyRefused, proxyAddress, "set deadline: %w", err)
	}
	stopCancel := make(chan struct{})
	defer close(stopCancel)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close() //nolint:errcheck // cancel proxy handshake
		case <-stopCancel:
		}
	}()

	if err := socksGreet(conn, proxyAddress); err != nil {
		return err
	}
	return socksRequest(conn, host, port, proxyAddress, timeout)
}

func socksGreet(conn net.Conn, proxyAddress string) error {
	// Method negotiation: offer only "no authentication".
	if _, err := conn.Write([]byte{socks5Version, 1, socks5MethodNoAuth}); err != nil {
		return greetingFailure(err, proxyAddress)
	}
	var reply [2]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		return greetingFailure(err, proxyAddress)
	}
	if reply[0] != socks5Version {
		return proxyFailure(ErrProxyRefused, proxyAddress, "not a SOCKS5 proxy (version %d)", reply[0])
	}
	if reply[1] == socks5MethodNoneAcceptable || reply[1] != socks5MethodNoAuth {
		return proxyFailure(
			ErrProxyAuthRequired, proxyAddress, "proxy requires an authentication method (0x%02x)", reply[1],
		)
	}
	return nil
}

func socksRequest(conn net.Conn, host string, port uint16, proxyAddress string, timeout time.Duration) error {
	request, err := socksConnectRequest(host, port)
	if err != nil {
		return proxyFailure(ErrProxyTargetUnreachable, proxyAddress, "%v", err)
	}
	if _, err := conn.Write(request); err != nil {
		return proxyFailure(ErrProxyRefused, proxyAddress, "send request: %w", err)
	}

	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		if isTimeout(err) {
			return proxyFailure(ErrProxyTargetUnreachable, proxyAddress, "no reply within %s", timeout)
		}
		return proxyFailure(ErrProxyRefused, proxyAddress, "closed without a reply: %w", err)
	}
	if err := skipBoundAddress(conn, head[3]); err != nil {
		return proxyFailure(ErrProxyRefused, proxyAddress, "malformed reply: %w", err)
	}
	if head[1] != socks5ReplySucceeded {
		return socksReplyFailure(head[1], proxyAddress)
	}
	return nil
}

func greetingFailure(err error, proxyAddress string) error {
	if isTimeout(err) {
		return proxyFailure(ErrProxyUnreachable, proxyAddress, "no reply to negotiation: %w", err)
	}
	return proxyFailure(ErrProxyRefused, proxyAddress, "closed during negotiation: %w", err)
}

func socksReplyFailure(rep byte, proxyAddress string) error {
	switch rep {
	case socks5ReplyNetworkUnreachable, socks5ReplyHostUnreachable, socks5ReplyConnectionRefused:
		return proxyFailure(ErrProxyTargetUnreachable, proxyAddress, "target not answering (reply 0x%02x)", rep)
	default:
		return proxyFailure(ErrProxyRefused, proxyAddress, "refused the connection (reply 0x%02x)", rep)
	}
}

const (
	socks5Version              = 0x05
	socks5MethodNoAuth         = 0x00
	socks5MethodNoneAcceptable = 0xFF
	socks5CommandConnect       = 0x01
	socks5AtypIPv4             = 0x01
	socks5AtypDomain           = 0x03
	socks5AtypIPv6             = 0x04

	socks5ReplySucceeded          = 0x00
	socks5ReplyNetworkUnreachable = 0x03
	socks5ReplyHostUnreachable    = 0x04
	socks5ReplyConnectionRefused  = 0x05

	socks5PortLength = 2 // the port field of a request or reply
)

func socksConnectRequest(host string, port uint16) ([]byte, error) {
	// VER, CMD, RSV
	request := []byte{socks5Version, socks5CommandConnect, 0x00}
	var address []byte
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			address = append([]byte{socks5AtypIPv4}, v4...)
		} else {
			address = append([]byte{socks5AtypIPv6}, ip.To16()...)
		}
	} else {
		name := []byte(host)
		if len(name) == 0 || len(name) > 255 {
			return nil, fmt.Errorf("target host name is not usable: %q", host)
		}
		address = append([]byte{socks5AtypDomain, byte(len(name))}, name...)
	}
	request = append(request, address...)
	return binary.BigEndian.AppendUint16(request, port), nil
}

func skipBoundAddress(conn net.Conn, atyp byte) error {
	var skip int
	switch atyp {
	case socks5AtypIPv4:
		skip = net.IPv4len
	case socks5AtypIPv6:
		skip = net.IPv6len
	case socks5AtypDomain:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return err
		}
		skip = int(length[0])
	default:
		return fmt.Errorf("unknown address type 0x%02x", atyp)
	}
	if _, err := io.CopyN(io.Discard, conn, int64(skip+socks5PortLength)); err != nil {
		return err
	}
	return nil
}

var proxyHostnamePattern = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// ErrInvalidProxyAddress is returned for a proxy address that is not
// host:port with an IPv4 literal, a bracketed IPv6 literal or a hostname, and
// a port from 1 to 65535. An optional leading socks5:// is accepted and
// removed. The result never contains a credential.
var ErrInvalidProxyAddress = errors.New("invalid proxy address")

// NormalizeProxyAddress validates a user-entered proxy address and returns the
// stored form, host:port. An empty value means no proxy.
func NormalizeProxyAddress(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) >= len(proxySchemePrefix) && strings.EqualFold(value[:len(proxySchemePrefix)], proxySchemePrefix) {
		value = value[len(proxySchemePrefix):]
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, "@/?# \t") {
		return "", ErrInvalidProxyAddress
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", ErrInvalidProxyAddress
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", ErrInvalidProxyAddress
	}
	if net.ParseIP(host) == nil && !proxyHostnamePattern.MatchString(host) {
		return "", ErrInvalidProxyAddress
	}
	return net.JoinHostPort(host, strconv.FormatUint(port, 10)), nil
}

const proxySchemePrefix = "socks5://"
