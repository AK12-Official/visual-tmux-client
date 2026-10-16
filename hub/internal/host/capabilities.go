package host

import (
	"context"
	"errors"
	"strings"
	"time"
)

// CapabilityStatus describes the outcome of an individual subsystem test.
type CapabilityStatus struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// FileCapability reports remote or local file subsystem guarantees.
type FileCapability struct {
	Enabled               bool   `json:"enabled"`
	Boundary              string `json:"boundary"`              // "account_bound" or "root_confined"
	RootHandleConfinement bool   `json:"rootHandleConfinement"` // false for remote SFTP
	AtomicReplacement     bool   `json:"atomicReplacement"`
	Error                 string `json:"error,omitempty"`
}

// HostCapabilities reports independent SSH, tmux, and SFTP status for a host.
type HostCapabilities struct {
	HostID   ID               `json:"hostId"`
	Revision Revision         `json:"revision"`
	Online   bool             `json:"online"`
	SSH      CapabilityStatus `json:"ssh"`
	Tmux     CapabilityStatus `json:"tmux"`
	SFTP     CapabilityStatus `json:"sftp"`
	Files    FileCapability   `json:"files"`
	Error    string           `json:"error,omitempty"`
}

// CapabilityChecker checks host availability and subsystem capabilities.
type CapabilityChecker interface {
	CheckCapabilities(ctx context.Context, id ID) (HostCapabilities, error)
}

// CheckCapabilities tests SSH, tmux, and SFTP independently for the specified host.
func (m *ConnectionManager) CheckCapabilities(ctx context.Context, id ID) (HostCapabilities, error) {
	if id == LocalID {
		return HostCapabilities{
			HostID:   LocalID,
			Revision: LocalRevision,
			Online:   true,
			SSH:      CapabilityStatus{Available: true},
			Tmux:     CapabilityStatus{Available: true},
			SFTP:     CapabilityStatus{Available: false, Error: "not_applicable"},
			Files:    FileCapability{Enabled: true, Boundary: "account_bound", RootHandleConfinement: false, AtomicReplacement: true},
		}, nil
	}

	if m.registry == nil {
		return HostCapabilities{}, ErrNotFound
	}

	h, err := m.registry.Get(ctx, id)
	if err != nil {
		return HostCapabilities{}, err
	}

	if !h.Trusted {
		return HostCapabilities{
			HostID:   id,
			Revision: h.Revision,
			Online:   false,
			SSH:      CapabilityStatus{Available: false, Error: "host_not_trusted"},
			Tmux:     CapabilityStatus{Available: false, Error: "host_not_trusted"},
			SFTP:     CapabilityStatus{Available: false, Error: "host_not_trusted"},
			Error:    "host_not_trusted",
		}, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	lease, err := m.Lease(probeCtx, id)
	if err != nil {
		return HostCapabilities{}, err
	}

	client, err := lease.GetControl(probeCtx)
	if err != nil {
		errStr := "host_unavailable"
		var fullErr strings.Builder
		fullErr.WriteString(err.Error())
		for curr := err; curr != nil; curr = errors.Unwrap(curr) {
			fullErr.WriteString(" ")
			fullErr.WriteString(curr.Error())
		}
		errLow := strings.ToLower(fullErr.String())
		if strings.Contains(errLow, "unable to authenticate") || strings.Contains(errLow, "authentication failed") || strings.Contains(errLow, "invalid password") {
			errStr = "authentication_failed"
		} else if strings.Contains(errLow, "host key mismatch") {
			errStr = "host_key_mismatch"
		} else if strings.Contains(errLow, "not trusted") {
			errStr = "host_not_trusted"
		}
		return HostCapabilities{
			HostID:   id,
			Revision: h.Revision,
			Online:   false,
			SSH:      CapabilityStatus{Available: false, Error: errStr},
			Tmux:     CapabilityStatus{Available: false, Error: "ssh_unavailable"},
			SFTP:     CapabilityStatus{Available: false, Error: "ssh_unavailable"},
			Error:    errStr,
		}, nil
	}

	// SSH connected: probe tmux and SFTP independently
	tmuxStatus := CapabilityStatus{Available: true}
	tmuxSess, err := client.NewSession()
	if err != nil {
		tmuxStatus = CapabilityStatus{Available: false, Error: "session_failed"}
	} else {
		out, err := tmuxSess.CombinedOutput("tmux -V")
		_ = tmuxSess.Close()
		if err != nil || !strings.Contains(strings.ToLower(string(out)), "tmux") {
			tmuxStatus = CapabilityStatus{Available: false, Error: "tmux_unavailable"}
		}
	}

	sftpStatus := CapabilityStatus{Available: true}
	sftpSess, err := client.NewSession()
	if err != nil {
		sftpStatus = CapabilityStatus{Available: false, Error: "session_failed"}
	} else {
		if err := sftpSess.RequestSubsystem("sftp"); err != nil {
			sftpStatus = CapabilityStatus{Available: false, Error: "sftp_unavailable"}
		}
		_ = sftpSess.Close()
	}

	fileCap := FileCapability{
		Enabled:               sftpStatus.Available,
		Boundary:              "account_bound",
		RootHandleConfinement: false,
	}
	if sftpStatus.Available {
		if adapter, err := NewSFTPAdapter(probeCtx, client); err == nil {
			fileCap.AtomicReplacement = adapter.HasExtension(PosixRenameExtension)
			_ = adapter.Close()
		}
	} else {
		fileCap.Error = "sftp_unavailable"
	}

	return HostCapabilities{
		HostID:   id,
		Revision: h.Revision,
		Online:   true,
		SSH:      CapabilityStatus{Available: true},
		Tmux:     tmuxStatus,
		SFTP:     sftpStatus,
		Files:    fileCap,
	}, nil
}
