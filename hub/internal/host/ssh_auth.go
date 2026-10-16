package host

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrPassphraseRequired = errors.New("passphrase required for encrypted private key")
)

// AuthMethods converts stored credentials into Go SSH authentication methods.
func AuthMethods(kind string, creds Credentials) ([]ssh.AuthMethod, error) {
	switch kind {
	case "password":
		if creds.Password == "" {
			return nil, errors.New("password cannot be empty")
		}
		return []ssh.AuthMethod{ssh.Password(creds.Password)}, nil

	case "private_key":
		if creds.PrivateKey == "" {
			return nil, errors.New("private key cannot be empty")
		}
		var signer ssh.Signer
		var err error
		if creds.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(creds.PrivateKey), []byte(creds.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(creds.PrivateKey))
			var passErr *ssh.PassphraseMissingError
			if errors.As(err, &passErr) {
				return nil, ErrPassphraseRequired
			}
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil

	case "":
		return nil, nil

	default:
		return nil, fmt.Errorf("unsupported credential type: %q", kind)
	}
}
