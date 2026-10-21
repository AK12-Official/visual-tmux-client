package host_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"golang.org/x/crypto/ssh"
)

const testEncryptedPrivateKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABB0snt0EP
qUIzFiI1+PtoiJAAAAGAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAICNxh6U7K395126N
I88EyFnv5GdvT+io6O4eDIijASeEAAAAsN6eQ1I04fkE+vVeCkWRjrGawubr7M2mgcSS11
8BJuOAd4Di3iNIDD9ZP7APGqoAwVkgKwS1zUTJA4fz1k556vH04JuE4+qLu0OfCgUrH0v7
DWKhR9jS1C59JLKFSk3+aF4o2HIsr48Lz5kGUkTfc8xj79PsDRkkbyqhxojhDerdUxpYtK
RECXJ0e+PkPnYftJYlni7+jWTtpvGWxCCXfsh/EY5+VRR/e1B6xnd5Mlpx
-----END OPENSSH PRIVATE KEY-----`

const testEncryptedKeyPassphrase = "testpassphrase"

func generateTestHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func generateUnencryptedKey(t *testing.T) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return string(pemBytes), sshPub
}

// startDisposableSSHServer runs an in-process SSH server on a loopback TCP port.
func startDisposableSSHServer(
	t *testing.T,
	expectedUser string,
	expectedPassword string,
	authorizedKeys []ssh.PublicKey,
) (net.Listener, ssh.PublicKey) {
	t.Helper()

	hostSigner := generateTestHostKey(t)
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if conn.User() == expectedUser && string(password) == expectedPassword {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != expectedUser {
				return nil, errors.New("unknown user")
			}
			for _, authKey := range authorizedKeys {
				if bytes.Equal(key.Marshal(), authKey.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("unauthorized public key")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() }) //nolint:errcheck // listener cleanup

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close() //nolint:errcheck // conn cleanup
					return
				}
				go ssh.DiscardRequests(reqs)
				go func() {
					for newChannel := range chans {
						if newChannel.ChannelType() == "session" {
							ch, reqs, err := newChannel.Accept()
							if err == nil {
								go ssh.DiscardRequests(reqs)
								go func() {
									// keep channel open until connection closes
									_ = sshConn.Wait() //nolint:errcheck // wait conn
									_ = ch.Close()     //nolint:errcheck // close ch
								}()
							}
							continue
						}
						_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported") //nolint:errcheck // reject
					}
				}()
				_ = sshConn.Wait() //nolint:errcheck // wait conn
			}(tcpConn)
		}
	}()

	return listener, hostSigner.PublicKey()
}

func TestSSHAuthAdapters(t *testing.T) {
	// Parse encrypted key public key to add to authorized keys
	signer, err := ssh.ParsePrivateKeyWithPassphrase(
		[]byte(testEncryptedPrivateKey),
		[]byte(testEncryptedKeyPassphrase),
	)
	if err != nil {
		t.Fatalf("setup: failed to parse encrypted key: %v", err)
	}
	encryptedKeyPub := signer.PublicKey()

	unencKeyPEM, unencPub := generateUnencryptedKey(t)

	listener, serverHostKey := startDisposableSSHServer(t, "testuser", "correct-password", []ssh.PublicKey{
		encryptedKeyPub,
		unencPub,
	})

	serverAddr := listener.Addr().String()

	dialWithAuth := func(methods []ssh.AuthMethod) error {
		clientConfig := &ssh.ClientConfig{
			User: "testuser",
			Auth: methods,
			HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				if !bytes.Equal(key.Marshal(), serverHostKey.Marshal()) {
					return errors.New("host key mismatch")
				}
				return nil
			},
		}
		client, err := ssh.Dial("tcp", serverAddr, clientConfig)
		if err != nil {
			return err
		}
		_ = client.Close() //nolint:errcheck // client cleanup
		return nil
	}

	t.Run("ValidPassword", func(t *testing.T) {
		methods, err := host.AuthMethods("password", host.Credentials{Password: "correct-password"})
		if err != nil {
			t.Fatal(err)
		}
		if err := dialWithAuth(methods); err != nil {
			t.Fatalf("expected successful dial, got: %v", err)
		}
	})

	t.Run("InvalidPassword", func(t *testing.T) {
		methods, err := host.AuthMethods("password", host.Credentials{Password: "wrong-password"})
		if err != nil {
			t.Fatal(err)
		}
		if err := dialWithAuth(methods); err == nil {
			t.Fatal("expected failure with wrong password, got success")
		}
	})

	t.Run("EmptyPassword", func(t *testing.T) {
		_, err := host.AuthMethods("password", host.Credentials{Password: ""})
		if err == nil {
			t.Fatal("expected error for empty password")
		}
	})

	t.Run("ValidUnencryptedPrivateKey", func(t *testing.T) {
		methods, err := host.AuthMethods("private_key", host.Credentials{PrivateKey: unencKeyPEM})
		if err != nil {
			t.Fatal(err)
		}
		if err := dialWithAuth(methods); err != nil {
			t.Fatalf("expected successful dial with unencrypted key, got: %v", err)
		}
	})

	t.Run("ValidEncryptedPrivateKey", func(t *testing.T) {
		methods, err := host.AuthMethods("private_key", host.Credentials{
			PrivateKey: testEncryptedPrivateKey,
			Passphrase: testEncryptedKeyPassphrase,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := dialWithAuth(methods); err != nil {
			t.Fatalf("expected successful dial with encrypted key and passphrase, got: %v", err)
		}
	})

	t.Run("EncryptedPrivateKeyMissingPassphrase", func(t *testing.T) {
		_, err := host.AuthMethods("private_key", host.Credentials{
			PrivateKey: testEncryptedPrivateKey,
			Passphrase: "",
		})
		if !errors.Is(err, host.ErrPassphraseRequired) {
			t.Fatalf("expected ErrPassphraseRequired, got: %v", err)
		}
	})

	t.Run("EncryptedPrivateKeyWrongPassphrase", func(t *testing.T) {
		_, err := host.AuthMethods("private_key", host.Credentials{
			PrivateKey: testEncryptedPrivateKey,
			Passphrase: "wrong-passphrase",
		})
		if err == nil {
			t.Fatal("expected error for wrong passphrase")
		}
	})

	t.Run("InvalidPrivateKeyFormat", func(t *testing.T) {
		_, err := host.AuthMethods("private_key", host.Credentials{
			PrivateKey: "not-a-valid-key-pem",
		})
		if err == nil {
			t.Fatal("expected error for invalid private key data")
		}
	})

	t.Run("UnsupportedCredentialType", func(t *testing.T) {
		_, err := host.AuthMethods("unknown_type", host.Credentials{})
		if err == nil {
			t.Fatal("expected error for unsupported credential type")
		}
	})
}
