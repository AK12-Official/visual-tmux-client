package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
)

const (
	MaxRemotePathLength = 4096
)

var (
	ErrRemoteInvalidPath    = errors.New("invalid remote path")
	ErrRemotePathNotAllowed = errors.New("remote path not allowed")
	ErrRemoteNotRegularFile = errors.New("remote target is not a regular file")
)

var blockedRemotePrefixes = []string{"/proc", "/sys", "/dev"}

// ValidateRemotePathShape verifies the lexical shape of a remote POSIX path.
// Remote paths are strictly POSIX paths starting with '/', max 4096 characters,
// and must not contain '..' segments or pseudo-filesystem prefixes (/proc, /sys, /dev).
func ValidateRemotePathShape(p string) error {
	if p == "" {
		return fmt.Errorf("%w: path is empty", ErrRemoteInvalidPath)
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: %q is not an absolute POSIX path", ErrRemoteInvalidPath, p)
	}
	if len(p) > MaxRemotePathLength {
		return fmt.Errorf("%w: path exceeds %d characters", ErrRemoteInvalidPath, MaxRemotePathLength)
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return fmt.Errorf("%w: %q contains a parent-directory segment", ErrRemoteInvalidPath, p)
		}
	}
	if IsBlockedRemotePath(p) {
		return fmt.Errorf("%w: %q is a pseudo-filesystem interface", ErrRemotePathNotAllowed, p)
	}
	return nil
}

// IsBlockedRemotePath checks whether a path names /proc, /sys, /dev or a path under them.
func IsBlockedRemotePath(p string) bool {
	clean := path.Clean(p)
	lowered := strings.ToLower(clean)
	for _, prefix := range blockedRemotePrefixes {
		if lowered == prefix || strings.HasPrefix(lowered, prefix+"/") {
			return true
		}
	}
	return false
}

// CanonicalizeRemotePath checks the canonical target of a path on the remote SFTP server,
// ensuring symlinks do not resolve into /proc, /sys, or /dev.
func CanonicalizeRemotePath(ctx context.Context, sftpClient *sftp.Client, p string) (string, error) {
	if err := ValidateRemotePathShape(p); err != nil {
		return "", err
	}
	clean := path.Clean(p)

	if sftpClient == nil {
		return clean, nil
	}

	// Use SFTP RealPath to resolve symlinks
	real, err := sftpClient.RealPath(clean)
	if err != nil {
		// If the file does not exist, check nearest existing ancestor
		parent := clean
		for {
			parent = path.Dir(parent)
			if parent == "/" || parent == "." {
				break
			}
			if realParent, pErr := sftpClient.RealPath(parent); pErr == nil {
				if IsBlockedRemotePath(realParent) {
					return "", fmt.Errorf("%w: %q resolves to pseudo-filesystem %q", ErrRemotePathNotAllowed, p, realParent)
				}
				break
			}
		}
		return clean, nil
	}

	if IsBlockedRemotePath(real) {
		return "", fmt.Errorf("%w: %q resolves to pseudo-filesystem %q", ErrRemotePathNotAllowed, p, real)
	}
	return real, nil
}

// VerifyRemoteRegularFile checks that a remote path refers to a regular file (not device, socket, pipe, or directory).
func VerifyRemoteRegularFile(fi os.FileInfo) error {
	if fi == nil {
		return os.ErrNotExist
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s has mode %s", ErrRemoteNotRegularFile, fi.Name(), fi.Mode().String())
	}
	return nil
}
