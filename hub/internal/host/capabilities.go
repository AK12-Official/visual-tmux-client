package host

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// CapabilityState indicates the availability state of a subsystem.
type CapabilityState string

const (
	CapabilityStateAvailable     CapabilityState = "available"
	CapabilityStateUnavailable   CapabilityState = "unavailable"
	CapabilityStateUnknown       CapabilityState = "unknown"
	CapabilityStateNotApplicable CapabilityState = "not_applicable"
)

const (
	localProbeTimeout  = 3 * time.Second
	remoteProbeTimeout = 10 * time.Second

	reasonTmuxUnavailable = "tmux_unavailable"
	reasonHostNotTrusted  = "host_not_trusted"
	reasonSSHUnavailable  = "ssh_unavailable"
	reasonSessionFailed   = "session_failed"
	reasonSFTPUnavailable = "sftp_unavailable"
	reasonHostUnavailable = "host_unavailable"
	reasonAuthFailed      = "authentication_failed"
	reasonHostKeyMismatch = "host_key_mismatch"
	reasonFilesDisabled   = "files_disabled"
)

// CapabilityStatus describes the outcome of an individual subsystem test.
type CapabilityStatus struct {
	State      CapabilityState `json:"state"`
	Available  bool            `json:"available"`
	Error      string          `json:"error,omitempty"`
	ObservedAt time.Time       `json:"observedAt,omitempty"`
}

// FileCapability reports remote or local file subsystem guarantees.
type FileCapability struct {
	State                 CapabilityState `json:"state"`
	Enabled               bool            `json:"enabled"`
	Boundary              string          `json:"boundary"`              // "account_bound" or "root_confined"
	RootHandleConfinement bool            `json:"rootHandleConfinement"` // false for remote SFTP
	AtomicReplacement     bool            `json:"atomicReplacement"`
	Error                 string          `json:"error,omitempty"`
	ObservedAt            time.Time       `json:"observedAt,omitempty"`
}

// HostCapabilities reports independent SSH, tmux, and SFTP status for a host.
type HostCapabilities struct {
	HostID     ID               `json:"hostId"`
	Revision   Revision         `json:"revision"`
	Online     bool             `json:"online"`
	SSH        CapabilityStatus `json:"ssh"`
	Tmux       CapabilityStatus `json:"tmux"`
	SFTP       CapabilityStatus `json:"sftp"`
	Files      FileCapability   `json:"files"`
	ObservedAt time.Time        `json:"observedAt,omitempty"`
	Error      string           `json:"error,omitempty"`
}

// CapabilityChecker checks host availability and subsystem capabilities.
type CapabilityChecker interface {
	CheckCapabilities(ctx context.Context, id ID) (HostCapabilities, error)
}

// CheckCapabilities tests SSH, tmux, and SFTP independently for the specified host.
func (m *ConnectionManager) CheckCapabilities(ctx context.Context, id ID) (HostCapabilities, error) {
	if m.registry == nil {
		return HostCapabilities{}, ErrNotFound
	}

	h, err := m.registry.Get(ctx, id)
	if err != nil {
		return HostCapabilities{}, err
	}

	now := time.Now().UTC()

	if h.ConnectionType == ConnectionTypeLocal {
		return m.checkLocalCapabilities(ctx, h, now)
	}

	if !h.Trusted {
		return untrustedCapabilities(h, now), nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, remoteProbeTimeout)
	defer cancel()

	lease, err := m.Lease(probeCtx, id)
	if err != nil {
		return HostCapabilities{}, err
	}

	probeConn, err := lease.OpenDedicated(probeCtx, "probe")
	if err != nil {
		return mapSSHProbeError(err, h, now), nil
	}
	defer func() {
		_ = probeConn.Close() //nolint:errcheck // probe connection cleanup
	}()

	tmuxStatus := probeTmuxCapability(probeCtx, probeConn.Client, now)
	sftpStatus, fileCap := probeSFTPCapability(probeCtx, probeConn.Client, now)

	return HostCapabilities{
		HostID:     id,
		Revision:   h.Revision,
		Online:     true,
		SSH:        CapabilityStatus{State: CapabilityStateAvailable, Available: true, ObservedAt: now},
		Tmux:       tmuxStatus,
		SFTP:       sftpStatus,
		Files:      fileCap,
		ObservedAt: now,
	}, nil
}

func probeLocalTmux(ctx context.Context, sessSvc Sessions, now time.Time) CapabilityStatus {
	tmuxAvailable := false
	var tmuxErr string
	if sessSvc != nil {
		if _, err := sessSvc.ListSessions(ctx); err == nil {
			tmuxAvailable = true
		} else {
			tmuxErr = reasonTmuxUnavailable
		}
	} else {
		out, err := exec.CommandContext(ctx, "tmux", "-V").CombinedOutput()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "tmux") {
			tmuxAvailable = true
		} else {
			tmuxErr = reasonTmuxUnavailable
		}
	}

	if !tmuxAvailable {
		return CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      tmuxErr,
			ObservedAt: now,
		}
	}
	return CapabilityStatus{
		State:      CapabilityStateAvailable,
		Available:  true,
		ObservedAt: now,
	}
}

func probeLocalFiles(filesSvc Files, now time.Time) FileCapability {
	fileCap := FileCapability{
		State:                 CapabilityStateAvailable,
		Enabled:               true,
		Boundary:              "account_bound",
		RootHandleConfinement: false,
		AtomicReplacement:     true,
		ObservedAt:            now,
	}

	if filesSvc != nil {
		if !filesSvc.Enabled() {
			fileCap.State = CapabilityStateUnavailable
			fileCap.Enabled = false
			fileCap.Error = reasonFilesDisabled
		}
		if g, ok := filesSvc.(interface {
			Boundary() string
			RootHandleConfinement() bool
		}); ok {
			fileCap.Boundary = g.Boundary()
			fileCap.RootHandleConfinement = g.RootHandleConfinement()
		}
	}
	return fileCap
}

func (m *ConnectionManager) checkLocalCapabilities(
	ctx context.Context,
	h Host,
	now time.Time,
) (HostCapabilities, error) {
	probeCtx, cancel := context.WithTimeout(ctx, localProbeTimeout)
	defer cancel()

	m.mu.Lock()
	sessSvc := m.localRuntime.Sessions
	filesSvc := m.localRuntime.Files
	m.mu.Unlock()

	tmuxStatus := probeLocalTmux(probeCtx, sessSvc, now)
	fileCap := probeLocalFiles(filesSvc, now)

	return HostCapabilities{
		HostID:   h.ID,
		Revision: h.Revision,
		Online:   true,
		SSH: CapabilityStatus{
			State:      CapabilityStateNotApplicable,
			Available:  false,
			ObservedAt: now,
		},
		Tmux:       tmuxStatus,
		SFTP:       CapabilityStatus{State: CapabilityStateNotApplicable, Available: false, ObservedAt: now},
		Files:      fileCap,
		ObservedAt: now,
	}, nil
}

func untrustedCapabilities(h Host, now time.Time) HostCapabilities {
	return HostCapabilities{
		HostID:   h.ID,
		Revision: h.Revision,
		Online:   false,
		SSH: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonHostNotTrusted,
			ObservedAt: now,
		},
		Tmux: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonHostNotTrusted,
			ObservedAt: now,
		},
		SFTP: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonHostNotTrusted,
			ObservedAt: now,
		},
		Files: FileCapability{
			State:      CapabilityStateUnavailable,
			Enabled:    false,
			Error:      reasonHostNotTrusted,
			ObservedAt: now,
		},
		ObservedAt: now,
		Error:      reasonHostNotTrusted,
	}
}

func mapSSHProbeError(err error, h Host, now time.Time) HostCapabilities {
	errStr := reasonHostUnavailable
	var fullErr strings.Builder
	fullErr.WriteString(err.Error())
	for curr := err; curr != nil; curr = errors.Unwrap(curr) {
		fullErr.WriteString(" ")
		fullErr.WriteString(curr.Error())
	}
	errLow := strings.ToLower(fullErr.String())
	if strings.Contains(errLow, "unable to authenticate") ||
		strings.Contains(errLow, "authentication failed") ||
		strings.Contains(errLow, "invalid password") {
		errStr = reasonAuthFailed
	} else if strings.Contains(errLow, "host key mismatch") {
		errStr = reasonHostKeyMismatch
	} else if strings.Contains(errLow, "not trusted") {
		errStr = reasonHostNotTrusted
	}
	return HostCapabilities{
		HostID:   h.ID,
		Revision: h.Revision,
		Online:   false,
		SSH: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      errStr,
			ObservedAt: now,
		},
		Tmux: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonSSHUnavailable,
			ObservedAt: now,
		},
		SFTP: CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonSSHUnavailable,
			ObservedAt: now,
		},
		Files: FileCapability{
			State:      CapabilityStateUnavailable,
			Enabled:    false,
			Error:      reasonSSHUnavailable,
			ObservedAt: now,
		},
		ObservedAt: now,
		Error:      errStr,
	}
}

func probeTmuxCapability(ctx context.Context, client *ssh.Client, now time.Time) CapabilityStatus {
	tmuxSess, err := client.NewSession()
	if err != nil {
		return CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonSessionFailed,
			ObservedAt: now,
		}
	}
	stopTmux := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = tmuxSess.Close() //nolint:errcheck // probe cancel cleanup
		case <-stopTmux:
		}
	}()
	out, err := tmuxSess.CombinedOutput("tmux -V")
	close(stopTmux)
	_ = tmuxSess.Close() //nolint:errcheck // probe session cleanup
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "tmux") {
		return CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonTmuxUnavailable,
			ObservedAt: now,
		}
	}
	return CapabilityStatus{
		State:      CapabilityStateAvailable,
		Available:  true,
		ObservedAt: now,
	}
}

func probeSFTPCapability(
	ctx context.Context,
	client *ssh.Client,
	now time.Time,
) (CapabilityStatus, FileCapability) {
	sftpStatus := CapabilityStatus{State: CapabilityStateAvailable, Available: true, ObservedAt: now}
	sftpSess, err := client.NewSession()
	if err != nil {
		sftpStatus = CapabilityStatus{
			State:      CapabilityStateUnavailable,
			Available:  false,
			Error:      reasonSessionFailed,
			ObservedAt: now,
		}
	} else {
		stopSftp := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = sftpSess.Close() //nolint:errcheck // probe cancel cleanup
			case <-stopSftp:
			}
		}()
		reqErr := sftpSess.RequestSubsystem("sftp")
		close(stopSftp)
		_ = sftpSess.Close() //nolint:errcheck // probe session cleanup
		if reqErr != nil {
			sftpStatus = CapabilityStatus{
				State:      CapabilityStateUnavailable,
				Available:  false,
				Error:      reasonSFTPUnavailable,
				ObservedAt: now,
			}
		}
	}

	fileCap := FileCapability{
		State:                 CapabilityStateAvailable,
		Enabled:               sftpStatus.Available,
		Boundary:              "account_bound",
		RootHandleConfinement: false,
		ObservedAt:            now,
	}
	if sftpStatus.Available {
		if adapter, err := NewSFTPAdapter(ctx, client); err == nil {
			fileCap.AtomicReplacement = adapter.HasExtension(PosixRenameExtension)
			_ = adapter.Close() //nolint:errcheck // probe adapter cleanup
		}
	} else {
		fileCap.State = CapabilityStateUnavailable
		fileCap.Error = reasonSFTPUnavailable
	}

	return sftpStatus, fileCap
}
