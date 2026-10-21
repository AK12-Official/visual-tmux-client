package host

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ErrHostKeyMismatch   = errors.New("ssh host key mismatch")
	ErrTrustRequired     = errors.New("host key not trusted")
	ErrChallengeExpired  = errors.New("trust challenge expired or invalid")
	ErrChallengeMismatch = errors.New("trust challenge parameter mismatch")
)

// ProbeResult contains public metadata from a pre-authentication probe.
type ProbeResult struct {
	ChallengeToken string    `json:"challengeToken"`
	Fingerprint    string    `json:"fingerprint"`
	KeyType        string    `json:"keyType"`
	HostKey        []byte    `json:"hostKey"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// TrustChallenge binds observed host key metadata to a specific endpoint and revision.
type TrustChallenge struct {
	Token       string
	HostID      ID
	Revision    Revision
	Endpoint    string
	HostKey     []byte
	Fingerprint string
	KeyType     string
	ExpiresAt   time.Time
}

// TrustStore tracks short-lived trust confirmation challenges.
type TrustStore struct {
	mu         sync.Mutex
	ttl        time.Duration
	challenges map[string]TrustChallenge
}

const (
	defaultTrustTTL     = 5 * time.Minute
	challengeTokenBytes = 16
	defaultDialTimeout  = 10 * time.Second
)

// NewTrustStore initializes an in-memory challenge store with a default TTL.
func NewTrustStore(ttl time.Duration) *TrustStore {
	if ttl <= 0 {
		ttl = defaultTrustTTL
	}
	return &TrustStore{
		ttl:        ttl,
		challenges: make(map[string]TrustChallenge),
	}
}

func (s *TrustStore) sweepLocked() {
	now := time.Now()
	for k, v := range s.challenges {
		if now.After(v.ExpiresAt) {
			delete(s.challenges, k)
		}
	}
}

// CreateChallenge records an observed key challenge.
func (s *TrustStore) CreateChallenge(
	hostID ID, revision Revision, endpoint string, key ssh.PublicKey,
) (ProbeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	tokenBytes := make([]byte, challengeTokenBytes)
	if _, err := rand.Read(tokenBytes); err != nil {
		return ProbeResult{}, err
	}
	token := hex.EncodeToString(tokenBytes)
	expiresAt := time.Now().Add(s.ttl)

	marshaledKey := key.Marshal()
	fp := ssh.FingerprintSHA256(key)
	keyType := key.Type()

	s.challenges[token] = TrustChallenge{
		Token:       token,
		HostID:      hostID,
		Revision:    revision,
		Endpoint:    endpoint,
		HostKey:     marshaledKey,
		Fingerprint: fp,
		KeyType:     keyType,
		ExpiresAt:   expiresAt,
	}

	return ProbeResult{
		ChallengeToken: token,
		Fingerprint:    fp,
		KeyType:        keyType,
		HostKey:        marshaledKey,
		ExpiresAt:      expiresAt,
	}, nil
}

// Confirm validates challenge parameters and returns the pinned key bytes, consuming the token.
func (s *TrustStore) Confirm(
	token string, expectedHostID ID, expectedRevision Revision, expectedEndpoint string,
) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()

	c, ok := s.challenges[token]
	if !ok || time.Now().After(c.ExpiresAt) {
		delete(s.challenges, token)
		return nil, ErrChallengeExpired
	}

	if c.HostID != expectedHostID || c.Revision != expectedRevision || c.Endpoint != expectedEndpoint {
		return nil, ErrChallengeMismatch
	}

	delete(s.challenges, token)
	return c.HostKey, nil
}

// ProbeEndpoint probes an SSH server's public host key without presenting any authentication credentials.
func ProbeEndpoint(ctx context.Context, address string, port uint16, dialTimeout time.Duration) (ssh.PublicKey, error) {
	if port == 0 {
		port = 22
	}
	endpoint := net.JoinHostPort(address, fmt.Sprintf("%d", port))
	var capturedKey ssh.PublicKey
	var mu sync.Mutex

	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}

	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("probe dial: %w", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck // probe connection cleanup

	deadline := time.Now().Add(dialTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline) //nolint:errcheck // set probe deadline

	stopCancel := make(chan struct{})
	defer close(stopCancel)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close() //nolint:errcheck // probe cancellation
		case <-stopCancel:
		}
	}()

	// ClientConfig has NO auth methods configured, guaranteeing no credentials are sent.
	clientConfig := &ssh.ClientConfig{
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			mu.Lock()
			capturedKey = key
			mu.Unlock()
			return nil
		},
		Timeout: dialTimeout,
	}

	sshConn, chans, reqs, _ := ssh.NewClientConn( //nolint:errcheck // handshake fails intentionally due to no auth
		conn, endpoint, clientConfig,
	)
	_ = conn.SetDeadline(time.Time{}) //nolint:errcheck // clear deadline
	if sshConn != nil {
		_ = sshConn.Close() //nolint:errcheck // probe connection cleanup
	}
	_ = chans
	_ = reqs

	mu.Lock()
	key := capturedKey
	mu.Unlock()

	if key == nil {
		return nil, errors.New("failed to obtain server host key during handshake")
	}
	return key, nil
}

// PinnedHostKeyCallback returns an ssh.HostKeyCallback verifying against the pinned host key.
func PinnedHostKeyCallback(pinned []byte) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if len(pinned) == 0 {
			return ErrTrustRequired
		}
		if !bytes.Equal(key.Marshal(), pinned) {
			return ErrHostKeyMismatch
		}
		return nil
	}
}
