package host_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

func TestProbeDoesNotOfferCredentials(t *testing.T) {
	var authAttempts atomic.Int32
	hostSigner := generateTestHostKey(t)

	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			authAttempts.Add(1)
			return nil, errors.New("unexpected auth")
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			authAttempts.Add(1)
			return nil, errors.New("unexpected auth")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, _, _, _ := ssh.NewServerConn(c, serverConfig)
				if sshConn != nil {
					_ = sshConn.Close()
				}
			}(tcpConn)
		}
	}()

	hostStr, portStr, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.ParseUint(portStr, 10, 16)

	ctx := context.Background()
	key, err := host.ProbeEndpoint(ctx, hostStr, uint16(port), time.Second)
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}

	if !bytes.Equal(key.Marshal(), hostSigner.PublicKey().Marshal()) {
		t.Fatal("probe returned wrong host key")
	}

	if authAttempts.Load() != 0 {
		t.Fatalf("probe offered credentials to server; attempts=%d", authAttempts.Load())
	}
}

func TestTrustChallengeLifecycle(t *testing.T) {
	store := host.NewTrustStore(50 * time.Millisecond)
	signer := generateTestHostKey(t)
	pubKey := signer.PublicKey()

	res, err := store.CreateChallenge("host-1", 1, "127.0.0.1:22", pubKey)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChallengeToken == "" || res.Fingerprint == "" || res.KeyType == "" {
		t.Fatalf("empty challenge fields: %+v", res)
	}

	// 1. Mismatched revision
	if _, err := store.Confirm(res.ChallengeToken, "host-1", 2, "127.0.0.1:22"); !errors.Is(err, host.ErrChallengeMismatch) {
		t.Fatalf("expected ErrChallengeMismatch for wrong revision, got %v", err)
	}

	// 2. Mismatched host ID
	if _, err := store.Confirm(res.ChallengeToken, "other-host", 1, "127.0.0.1:22"); !errors.Is(err, host.ErrChallengeMismatch) {
		t.Fatalf("expected ErrChallengeMismatch for wrong host ID, got %v", err)
	}

	// 3. Mismatched endpoint
	if _, err := store.Confirm(res.ChallengeToken, "host-1", 1, "192.168.1.1:22"); !errors.Is(err, host.ErrChallengeMismatch) {
		t.Fatalf("expected ErrChallengeMismatch for wrong endpoint, got %v", err)
	}

	// 4. Successful confirmation
	keyBytes, err := store.Confirm(res.ChallengeToken, "host-1", 1, "127.0.0.1:22")
	if err != nil {
		t.Fatalf("expected successful confirmation, got %v", err)
	}
	if !bytes.Equal(keyBytes, pubKey.Marshal()) {
		t.Fatal("confirmed key does not match probed key")
	}

	// 5. Token is single-use: second confirmation must fail
	if _, err := store.Confirm(res.ChallengeToken, "host-1", 1, "127.0.0.1:22"); !errors.Is(err, host.ErrChallengeExpired) {
		t.Fatalf("expected ErrChallengeExpired on token reuse, got %v", err)
	}

	// 6. Expiration
	expRes, err := store.CreateChallenge("host-2", 1, "127.0.0.1:22", pubKey)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := store.Confirm(expRes.ChallengeToken, "host-2", 1, "127.0.0.1:22"); !errors.Is(err, host.ErrChallengeExpired) {
		t.Fatalf("expected ErrChallengeExpired after TTL, got %v", err)
	}
}

func TestPinnedHostKeyChecking(t *testing.T) {
	signer1 := generateTestHostKey(t)
	signer2 := generateTestHostKey(t)

	// Callback with pinned key 1
	callback := host.PinnedHostKeyCallback(signer1.PublicKey().Marshal())

	// Verifying key 1 should succeed
	dummyAddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:22")
	if err := callback("example.com:22", dummyAddr, signer1.PublicKey()); err != nil {
		t.Fatalf("expected pinned key to match, got %v", err)
	}

	// Verifying key 2 should fail with ErrHostKeyMismatch
	if err := callback("example.com:22", dummyAddr, signer2.PublicKey()); !errors.Is(err, host.ErrHostKeyMismatch) {
		t.Fatalf("expected ErrHostKeyMismatch for changed key, got %v", err)
	}

	// Unpinned callback must return ErrTrustRequired
	unpinnedCallback := host.PinnedHostKeyCallback(nil)
	if err := unpinnedCallback("example.com:22", dummyAddr, signer1.PublicKey()); !errors.Is(err, host.ErrTrustRequired) {
		t.Fatalf("expected ErrTrustRequired for unpinned key, got %v", err)
	}
}
