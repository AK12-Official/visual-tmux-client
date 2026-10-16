package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	ErrUnsupportedEnvironment = errors.New("unsupported remote environment")
	ErrTmuxUnavailable        = errors.New("tmux is not installed or unavailable on remote host")
)

// RemoteEnv describes a validated remote Unix environment.
type RemoteEnv struct {
	OS          string
	Home        string
	TmuxVersion string
}

// ProbeRemoteEnvironment checks that the remote host runs a supported Unix OS,
// has a working POSIX command environment, has tmux installed, and returns its home directory.
func ProbeRemoteEnvironment(ctx context.Context, client *ssh.Client) (*RemoteEnv, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("open ssh session for environment probe: %w", err)
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	probeScript := `sh -c 'uname -s; echo "---ENV-SEP---"; echo "$HOME"; echo "---ENV-SEP---"; tmux -V'`
	if err := session.Run(probeScript); err != nil {
		errOutput := strings.TrimSpace(stderr.String())
		if strings.Contains(errOutput, "tmux") || strings.Contains(stdout.String(), "not found") {
			return nil, fmt.Errorf("%w: %s", ErrTmuxUnavailable, errOutput)
		}
		return nil, fmt.Errorf("%w: probe failed: %s (%w)", ErrUnsupportedEnvironment, errOutput, err)
	}

	parts := strings.Split(stdout.String(), "---ENV-SEP---")
	if len(parts) < 3 {
		return nil, fmt.Errorf("%w: invalid probe output structure", ErrUnsupportedEnvironment)
	}

	osName := strings.TrimSpace(parts[0])
	homeDir := strings.TrimSpace(parts[1])
	tmuxVer := strings.TrimSpace(parts[2])

	switch strings.ToLower(osName) {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		// Supported Unix-like operating systems
	default:
		return nil, fmt.Errorf("%w: unsupported operating system %q", ErrUnsupportedEnvironment, osName)
	}

	if homeDir == "" {
		return nil, fmt.Errorf("%w: empty remote home directory", ErrUnsupportedEnvironment)
	}

	if !strings.HasPrefix(strings.ToLower(tmuxVer), "tmux") {
		return nil, fmt.Errorf("%w: unrecognized tmux version output: %q", ErrTmuxUnavailable, tmuxVer)
	}

	return &RemoteEnv{
		OS:          osName,
		Home:        homeDir,
		TmuxVersion: tmuxVer,
	}, nil
}
