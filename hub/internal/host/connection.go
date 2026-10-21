package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

const (
	DefaultDialTimeout       = 10 * time.Second
	DefaultHandshakeTimeout  = 10 * time.Second
	DefaultOperationTimeout  = 30 * time.Second
	DefaultKeepaliveInterval = 15 * time.Second
	DefaultMaxBackoff        = 30 * time.Second
	DefaultMaxConnections    = 16

	maxBackoffExponent  = 6
	baseBackoffDuration = 250 * time.Millisecond
	cleanupTimeout      = 10 * time.Second
	keepaliveTimeout    = 5 * time.Second
)

type ConnectionConfig struct {
	DialTimeout       time.Duration
	HandshakeTimeout  time.Duration
	OperationTimeout  time.Duration
	KeepaliveInterval time.Duration
	MaxBackoff        time.Duration
	MaxConnections    int
}

func DefaultConnectionConfig() ConnectionConfig {
	return ConnectionConfig{
		DialTimeout:       DefaultDialTimeout,
		HandshakeTimeout:  DefaultHandshakeTimeout,
		OperationTimeout:  DefaultOperationTimeout,
		KeepaliveInterval: DefaultKeepaliveInterval,
		MaxBackoff:        DefaultMaxBackoff,
		MaxConnections:    DefaultMaxConnections,
	}
}

// ManagedConn represents an isolated transport whose closure does not affect other connections.
type ManagedConn struct {
	Client   *ssh.Client
	Target   Target
	onClose  func()
	once     sync.Once
	closeErr error
}

func (m *ManagedConn) Close() error {
	m.once.Do(func() {
		if m.Client != nil {
			m.closeErr = m.Client.Close()
		}
		if m.onClose != nil {
			m.onClose()
		}
	})
	return m.closeErr
}

// ClientLease binds work to a specific target and configuration revision.
type ClientLease struct {
	Target  Target
	revoked *atomic.Bool
	manager *ConnectionManager
}

func (l *ClientLease) CheckValid() error {
	if l.revoked != nil && l.revoked.Load() {
		return &Error{
			Target: l.Target,
			Code:   ErrStaleRevision,
			Cause:  errors.New("host revision superseded or invalidated"),
		}
	}
	return nil
}

// GetControl returns the shared control connection for short commands.
func (l *ClientLease) GetControl(ctx context.Context) (*ssh.Client, error) {
	if err := l.CheckValid(); err != nil {
		return nil, err
	}
	return l.manager.getControl(ctx, l.Target)
}

// InvalidateControl clears the shared control connection if it matches client.
func (l *ClientLease) InvalidateControl(client *ssh.Client) {
	if l.manager != nil && client != nil {
		l.manager.invalidateControl(l.Target, client)
	}
}

// OpenDedicated creates a separate transport for a terminal or file session.
func (l *ClientLease) OpenDedicated(ctx context.Context, purpose string) (*ManagedConn, error) {
	if err := l.CheckValid(); err != nil {
		return nil, err
	}
	return l.manager.dialDedicated(ctx, l.Target, purpose)
}

// Registry returns the Registry associated with the connection manager, if any.
func (l *ClientLease) Registry() *Registry {
	if l == nil || l.manager == nil {
		return nil
	}
	return l.manager.Registry()
}

type hostConnPool struct {
	mu              sync.Mutex
	target          Target
	failures        int
	nextAllowedDial time.Time
	controlClient   *ssh.Client
	controlStopKeep chan struct{}
	dedicated       map[*ManagedConn]struct{}
	revoked         atomic.Bool
	group           singleflight.Group
	sem             *semaphore.Weighted
}

func computeBackoff(failures int, maxBackoff time.Duration) time.Duration {
	shift := min(failures, maxBackoffExponent)
	backoff := time.Duration(1<<shift) * baseBackoffDuration
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

func (p *hostConnPool) recordFailure(maxBackoff time.Duration) {
	p.mu.Lock()
	p.failures++
	p.nextAllowedDial = time.Now().Add(computeBackoff(p.failures, maxBackoff))
	p.mu.Unlock()
}

func (p *hostConnPool) recordSuccess() {
	p.mu.Lock()
	p.failures = 0
	p.nextAllowedDial = time.Time{}
	p.mu.Unlock()
}

// ConnectionManager manages SSH connection lifecycles, pooling, trust, and backoff.
type ConnectionManager struct {
	cfg          ConnectionConfig
	registry     *Registry
	localRuntime Runtime
	mu           sync.Mutex
	pools        map[ID]*hostConnPool
	closed       bool
}

// SetLocalRuntime configures the local runtime adapter used for local capability inspection.
func (m *ConnectionManager) SetLocalRuntime(rt Runtime) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.localRuntime = rt
}

func NewConnectionManager(cfg ConnectionConfig, registry *Registry) *ConnectionManager {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = DefaultOperationTimeout
	}
	if cfg.KeepaliveInterval <= 0 {
		cfg.KeepaliveInterval = DefaultKeepaliveInterval
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = DefaultMaxConnections
	}
	return &ConnectionManager{
		cfg:      cfg,
		registry: registry,
		pools:    make(map[ID]*hostConnPool),
	}
}

func (m *ConnectionManager) Registry() *Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *ConnectionManager) getPool(id ID, rev Revision) *hostConnPool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pool, ok := m.pools[id]
	if !ok || pool.target.ConnectionRevision != rev {
		if ok {
			pool.revoked.Store(true)
			m.closePoolLocked(pool)
		}
		pool = &hostConnPool{
			target:    Target{HostID: id, Revision: rev, ConnectionRevision: rev},
			dedicated: make(map[*ManagedConn]struct{}),
			sem:       semaphore.NewWeighted(int64(m.cfg.MaxConnections)),
		}
		m.pools[id] = pool
	}
	return pool
}

// Lease obtains a lease for executing operations against a host runtime revision.
func (m *ConnectionManager) Lease(ctx context.Context, id ID) (ClientLease, error) {
	if m.registry != nil {
		rec, _, err := m.registry.GetCredentials(ctx, id)
		if err != nil {
			if errors.Is(err, ErrUnsupported) {
				return ClientLease{
					Target: Target{
						HostID:             id,
						Revision:           rec.Revision,
						ConnectionRevision: rec.ConnectionRevision,
					},
				}, nil
			}
			if errors.Is(err, ErrNotFound) && id == LocalID {
				return ClientLease{
					Target: Target{
						HostID:             LocalID,
						Revision:           LocalRevision,
						ConnectionRevision: LocalRevision,
					},
				}, nil
			}
			return ClientLease{}, err
		}
		connRev := rec.ConnectionRevision
		if connRev == 0 {
			connRev = rec.Revision
		}
		pool := m.getPool(id, connRev)
		return ClientLease{
			Target:  Target{HostID: id, Revision: rec.Revision, ConnectionRevision: connRev},
			revoked: &pool.revoked,
			manager: m,
		}, nil
	}

	if id == LocalID {
		return ClientLease{
			Target: Target{
				HostID:             LocalID,
				Revision:           LocalRevision,
				ConnectionRevision: LocalRevision,
			},
		}, nil
	}
	return ClientLease{}, ErrNotFound
}

func (m *ConnectionManager) resolveSSHConfig(
	ctx context.Context,
	target Target,
	connRev Revision,
) (*ssh.ClientConfig, string, error) {
	rec, creds, err := m.registry.GetCredentials(ctx, target.HostID)
	if err != nil {
		return nil, "", err
	}
	actualConnRev := rec.ConnectionRevision
	if actualConnRev == 0 {
		actualConnRev = rec.Revision
	}
	if connRev != 0 && actualConnRev != connRev {
		return nil, "", &Error{
			Target: target,
			Code:   ErrStaleRevision,
			Cause:  errors.New("host revision mismatch"),
		}
	}
	if len(rec.HostKey) == 0 {
		return nil, "", &Error{Target: target, Code: ErrUnavailable, Cause: ErrTrustRequired}
	}

	authMethods, err := AuthMethods(rec.CredentialType, creds)
	if err != nil {
		return nil, "", &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	endpoint := net.JoinHostPort(rec.Address, fmt.Sprintf("%d", rec.Port))
	sshConfig := &ssh.ClientConfig{
		User:            rec.Username,
		Auth:            authMethods,
		HostKeyCallback: PinnedHostKeyCallback(rec.HostKey),
		Timeout:         m.cfg.HandshakeTimeout,
	}
	return sshConfig, endpoint, nil
}

func (m *ConnectionManager) dialAndHandshake(
	ctx context.Context,
	target Target,
	endpoint string,
	sshConfig *ssh.ClientConfig,
) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: m.cfg.DialTimeout}
	netConn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	go func() {
		select {
		case <-handshakeDone:
		case <-ctx.Done():
			_ = netConn.Close() //nolint:errcheck // handshake cancel cleanup
		}
	}()

	_ = netConn.SetDeadline(time.Now().Add(m.cfg.HandshakeTimeout)) //nolint:errcheck // handshake timeout

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, endpoint, sshConfig)
	if err != nil {
		_ = netConn.Close() //nolint:errcheck // failure cleanup
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	_ = netConn.SetDeadline(time.Time{}) //nolint:errcheck // clear deadline

	return ssh.NewClient(sshConn, chans, reqs), nil
}

func (m *ConnectionManager) dialTransport(ctx context.Context, target Target) (*ssh.Client, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, errors.New("connection manager is closed")
	}
	m.mu.Unlock()

	connRev := target.ConnectionRevision
	if connRev == 0 {
		connRev = target.Revision
	}
	pool := m.getPool(target.HostID, connRev)
	if pool.revoked.Load() {
		return nil, nil, &Error{Target: target, Code: ErrStaleRevision, Cause: errors.New("host revision revoked")}
	}

	pool.mu.Lock()
	if time.Now().Before(pool.nextAllowedDial) {
		pool.mu.Unlock()
		return nil, nil, &Error{
			Target: target,
			Code:   ErrUnavailable,
			Cause:  errors.New("host connection backoff active"),
		}
	}
	pool.mu.Unlock()

	if err := pool.sem.Acquire(ctx, 1); err != nil {
		return nil, nil, err
	}
	releaseSem := func() { pool.sem.Release(1) }

	sshConfig, endpoint, err := m.resolveSSHConfig(ctx, target, connRev)
	if err != nil {
		releaseSem()
		return nil, nil, err
	}

	client, err := m.dialAndHandshake(ctx, target, endpoint, sshConfig)
	if err != nil {
		releaseSem()
		if ctx.Err() == nil {
			pool.recordFailure(m.cfg.MaxBackoff)
		}
		return nil, nil, err
	}

	pool.recordSuccess()
	return client, releaseSem, nil
}

func (m *ConnectionManager) createControlConnection(
	ctx context.Context,
	pool *hostConnPool,
	target Target,
) (*ssh.Client, error) {
	pool.mu.Lock()
	if pool.controlClient != nil {
		c := pool.controlClient
		pool.mu.Unlock()
		return c, nil
	}
	pool.mu.Unlock()

	client, releaseSem, err := m.dialTransport(ctx, target)
	if err != nil {
		return nil, err
	}

	stopKeep := make(chan struct{})
	pool.mu.Lock()
	pool.controlClient = client
	pool.controlStopKeep = stopKeep
	pool.mu.Unlock()

	go func() {
		_ = client.Wait() //nolint:errcheck // connection termination watcher
		pool.mu.Lock()
		if pool.controlClient == client {
			pool.controlClient = nil
			if pool.controlStopKeep != nil {
				select {
				case <-pool.controlStopKeep:
				default:
					close(pool.controlStopKeep)
				}
				pool.controlStopKeep = nil
			}
		}
		pool.mu.Unlock()
	}()

	go m.keepaliveLoop(client, pool, stopKeep, releaseSem)

	if m.registry != nil {
		m.triggerPendingCleanups(pool.target)
	}

	return client, nil
}

func (m *ConnectionManager) triggerPendingCleanups(t Target) {
	go func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		cleanups, err := m.registry.ListPendingCleanups(cleanCtx, t.HostID)
		if err == nil && len(cleanups) > 0 {
			l, err := m.Lease(cleanCtx, t.HostID)
			if err == nil {
				svc := NewRemoteFileService(l, 0, 0, "")
				svc.SetRegistry(m.registry)
				_, _ = svc.RunPendingCleanups(cleanCtx) //nolint:errcheck // background cleanup
			}
		}
	}()
}

func (m *ConnectionManager) getControl(ctx context.Context, target Target) (*ssh.Client, error) {
	connRev := target.ConnectionRevision
	if connRev == 0 {
		connRev = target.Revision
	}
	pool := m.getPool(target.HostID, connRev)
	if pool.revoked.Load() {
		return nil, &Error{Target: target, Code: ErrStaleRevision, Cause: errors.New("host revision revoked")}
	}

	pool.mu.Lock()
	if pool.controlClient != nil {
		c := pool.controlClient
		pool.mu.Unlock()
		return c, nil
	}
	pool.mu.Unlock()

	ch := pool.group.DoChan("control", func() (interface{}, error) {
		return m.createControlConnection(ctx, pool, target)
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		client, ok := res.Val.(*ssh.Client)
		if !ok {
			return nil, errors.New("unexpected type from singleflight")
		}
		return client, nil
	}
}

func (m *ConnectionManager) invalidateControl(target Target, client *ssh.Client) {
	connRev := target.ConnectionRevision
	if connRev == 0 {
		connRev = target.Revision
	}
	m.mu.Lock()
	pool, ok := m.pools[target.HostID]
	if !ok || pool.target.ConnectionRevision != connRev {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	pool.mu.Lock()
	if pool.controlClient == client {
		pool.controlClient = nil
		if pool.controlStopKeep != nil {
			select {
			case <-pool.controlStopKeep:
			default:
				close(pool.controlStopKeep)
			}
			pool.controlStopKeep = nil
		}
		_ = client.Close() //nolint:errcheck // control client cleanup
	}
	pool.mu.Unlock()
}

func (m *ConnectionManager) keepaliveLoop(
	client *ssh.Client,
	pool *hostConnPool,
	stop chan struct{},
	releaseSem func(),
) {
	defer releaseSem()
	ticker := time.NewTicker(m.cfg.KeepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Send keepalive request with timeout
			done := make(chan error, 1)
			go func() {
				_, _, err := client.SendRequest("@keepalive@openssh.com", true, nil)
				done <- err
			}()

			select {
			case <-stop:
				return
			case err := <-done:
				if err != nil {
					// Transport failed
					pool.mu.Lock()
					if pool.controlClient == client {
						pool.controlClient = nil
						_ = client.Close() //nolint:errcheck // keepalive loop failure
					}
					pool.mu.Unlock()
					return
				}
			case <-time.After(keepaliveTimeout):
				// Keepalive timeout
				pool.mu.Lock()
				if pool.controlClient == client {
					pool.controlClient = nil
					_ = client.Close() //nolint:errcheck // keepalive timeout
				}
				pool.mu.Unlock()
				return
			}
		}
	}
}

func (m *ConnectionManager) dialDedicated(ctx context.Context, target Target, purpose string) (*ManagedConn, error) {
	client, releaseSem, err := m.dialTransport(ctx, target)
	if err != nil {
		return nil, err
	}

	connRev := target.ConnectionRevision
	if connRev == 0 {
		connRev = target.Revision
	}
	pool := m.getPool(target.HostID, connRev)
	managed := &ManagedConn{
		Client: client,
		Target: target,
	}

	pool.mu.Lock()
	pool.dedicated[managed] = struct{}{}
	pool.mu.Unlock()

	if purpose != "terminal" {
		stopWatcher := make(chan struct{})
		managed.onClose = func() {
			close(stopWatcher)
			pool.mu.Lock()
			delete(pool.dedicated, managed)
			pool.mu.Unlock()
			releaseSem()
		}

		go func() {
			select {
			case <-ctx.Done():
				_ = managed.Close() //nolint:errcheck // dedicated conn close on context cancellation
			case <-stopWatcher:
			}
		}()
	} else {
		managed.onClose = func() {
			pool.mu.Lock()
			delete(pool.dedicated, managed)
			pool.mu.Unlock()
			releaseSem()
		}
	}

	return managed, nil
}

// Invalidate closes all connections for a specific host and marks pending leases revoked.
func (m *ConnectionManager) Invalidate(id ID) {
	m.mu.Lock()
	pool, ok := m.pools[id]
	if ok {
		delete(m.pools, id)
		pool.revoked.Store(true)
		m.closePoolLocked(pool)
	}
	m.mu.Unlock()
}

func (m *ConnectionManager) closePoolLocked(pool *hostConnPool) {
	pool.mu.Lock()
	if pool.controlStopKeep != nil {
		close(pool.controlStopKeep)
		pool.controlStopKeep = nil
	}
	ctrl := pool.controlClient
	pool.controlClient = nil

	dedicatedConns := make([]*ManagedConn, 0, len(pool.dedicated))
	for conn := range pool.dedicated {
		dedicatedConns = append(dedicatedConns, conn)
	}
	pool.dedicated = make(map[*ManagedConn]struct{})
	pool.mu.Unlock()

	if ctrl != nil {
		_ = ctrl.Close() //nolint:errcheck // pool control client cleanup
	}
	for _, conn := range dedicatedConns {
		_ = conn.Close() //nolint:errcheck // pool dedicated conn cleanup
	}
}

// Close gracefully closes all active connections across all hosts.
func (m *ConnectionManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for id, pool := range m.pools {
		delete(m.pools, id)
		pool.revoked.Store(true)
		m.closePoolLocked(pool)
	}
	return nil
}
