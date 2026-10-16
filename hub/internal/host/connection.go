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
		DialTimeout:       10 * time.Second,
		HandshakeTimeout:  10 * time.Second,
		OperationTimeout:  30 * time.Second,
		KeepaliveInterval: 15 * time.Second,
		MaxBackoff:        30 * time.Second,
		MaxConnections:    16,
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
		return &Error{Target: l.Target, Code: ErrStaleRevision, Cause: errors.New("host revision superseded or invalidated")}
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

// ConnectionManager manages SSH connection lifecycles, pooling, trust, and backoff.
type ConnectionManager struct {
	cfg      ConnectionConfig
	registry *Registry
	mu       sync.Mutex
	pools    map[ID]*hostConnPool
	closed   bool
}

func NewConnectionManager(cfg ConnectionConfig, registry *Registry) *ConnectionManager {
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = 30 * time.Second
	}
	if cfg.KeepaliveInterval <= 0 {
		cfg.KeepaliveInterval = 15 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 16
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
	if !ok || pool.target.Revision != rev {
		if ok {
			pool.revoked.Store(true)
			m.closePoolLocked(pool)
		}
		pool = &hostConnPool{
			target:    Target{HostID: id, Revision: rev},
			dedicated: make(map[*ManagedConn]struct{}),
			sem:       semaphore.NewWeighted(int64(m.cfg.MaxConnections)),
		}
		m.pools[id] = pool
	}
	return pool
}

// Lease obtains a lease for executing operations against a host runtime revision.
func (m *ConnectionManager) Lease(ctx context.Context, id ID) (ClientLease, error) {
	if id == LocalID {
		return ClientLease{Target: Target{HostID: LocalID, Revision: LocalRevision}}, nil
	}
	rec, _, err := m.registry.GetCredentials(ctx, id)
	if err != nil {
		return ClientLease{}, err
	}
	pool := m.getPool(id, rec.Revision)
	return ClientLease{
		Target:  Target{HostID: id, Revision: rec.Revision},
		revoked: &pool.revoked,
		manager: m,
	}, nil
}

func (m *ConnectionManager) dialTransport(ctx context.Context, target Target) (*ssh.Client, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, errors.New("connection manager is closed")
	}
	m.mu.Unlock()

	pool := m.getPool(target.HostID, target.Revision)
	if pool.revoked.Load() {
		return nil, nil, &Error{Target: target, Code: ErrStaleRevision, Cause: errors.New("host revision revoked")}
	}

	// Backoff check
	pool.mu.Lock()
	if time.Now().Before(pool.nextAllowedDial) {
		pool.mu.Unlock()
		return nil, nil, &Error{Target: target, Code: ErrUnavailable, Cause: errors.New("host connection backoff active")}
	}
	pool.mu.Unlock()

	// Concurrency bounding
	if err := pool.sem.Acquire(ctx, 1); err != nil {
		return nil, nil, err
	}
	releaseSem := func() { pool.sem.Release(1) }

	rec, creds, err := m.registry.GetCredentials(ctx, target.HostID)
	if err != nil {
		releaseSem()
		return nil, nil, err
	}
	if rec.Revision != target.Revision {
		releaseSem()
		return nil, nil, &Error{Target: target, Code: ErrStaleRevision, Cause: errors.New("host revision mismatch")}
	}
	if len(rec.HostKey) == 0 {
		releaseSem()
		return nil, nil, &Error{Target: target, Code: ErrUnavailable, Cause: ErrTrustRequired}
	}

	authMethods, err := AuthMethods(rec.CredentialType, creds)
	if err != nil {
		releaseSem()
		return nil, nil, &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	endpoint := net.JoinHostPort(rec.Address, fmt.Sprintf("%d", rec.Port))

	dialer := net.Dialer{Timeout: m.cfg.DialTimeout}
	netConn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		pool.mu.Lock()
		pool.failures++
		backoff := time.Duration(1<<min(pool.failures, 6)) * 250 * time.Millisecond
		if backoff > m.cfg.MaxBackoff {
			backoff = m.cfg.MaxBackoff
		}
		pool.nextAllowedDial = time.Now().Add(backoff)
		pool.mu.Unlock()

		releaseSem()
		return nil, nil, &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	// Handshake timeout and cancellation
	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	go func() {
		select {
		case <-handshakeDone:
		case <-ctx.Done():
			_ = netConn.Close()
		}
	}()

	_ = netConn.SetDeadline(time.Now().Add(m.cfg.HandshakeTimeout))

	sshConfig := &ssh.ClientConfig{
		User:            rec.Username,
		Auth:            authMethods,
		HostKeyCallback: PinnedHostKeyCallback(rec.HostKey),
		Timeout:         m.cfg.HandshakeTimeout,
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, endpoint, sshConfig)
	if err != nil {
		_ = netConn.Close()
		if ctx.Err() != nil {
			releaseSem()
			return nil, nil, ctx.Err()
		}
		pool.mu.Lock()
		pool.failures++
		backoff := time.Duration(1<<min(pool.failures, 6)) * 250 * time.Millisecond
		if backoff > m.cfg.MaxBackoff {
			backoff = m.cfg.MaxBackoff
		}
		pool.nextAllowedDial = time.Now().Add(backoff)
		pool.mu.Unlock()

		releaseSem()
		return nil, nil, &Error{Target: target, Code: ErrUnavailable, Cause: err}
	}

	// Clear handshake deadline for normal operation
	_ = netConn.SetDeadline(time.Time{})

	pool.mu.Lock()
	pool.failures = 0
	pool.nextAllowedDial = time.Time{}
	pool.mu.Unlock()

	client := ssh.NewClient(sshConn, chans, reqs)
	return client, releaseSem, nil
}

func (m *ConnectionManager) getControl(ctx context.Context, target Target) (*ssh.Client, error) {
	pool := m.getPool(target.HostID, target.Revision)
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

		// Monitor connection termination immediately
		go func() {
			_ = client.Wait()
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

		// Start keepalive monitor
		go m.keepaliveLoop(client, pool, stopKeep, releaseSem)

		return client, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.(*ssh.Client), nil
	}
}

func (m *ConnectionManager) invalidateControl(target Target, client *ssh.Client) {
	pool := m.getPool(target.HostID, target.Revision)
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
		_ = client.Close()
	}
	pool.mu.Unlock()
}

func (m *ConnectionManager) keepaliveLoop(client *ssh.Client, pool *hostConnPool, stop chan struct{}, releaseSem func()) {
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
						_ = client.Close()
					}
					pool.mu.Unlock()
					return
				}
			case <-time.After(5 * time.Second):
				// Keepalive timeout
				pool.mu.Lock()
				if pool.controlClient == client {
					pool.controlClient = nil
					_ = client.Close()
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

	pool := m.getPool(target.HostID, target.Revision)
	managed := &ManagedConn{
		Client: client,
		Target: target,
	}

	pool.mu.Lock()
	pool.dedicated[managed] = struct{}{}
	pool.mu.Unlock()

	managed.onClose = func() {
		pool.mu.Lock()
		delete(pool.dedicated, managed)
		pool.mu.Unlock()
		releaseSem()
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
		_ = ctrl.Close()
	}
	for _, conn := range dedicatedConns {
		_ = conn.Close()
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
