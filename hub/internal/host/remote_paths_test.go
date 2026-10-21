package host_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
)

type dummyFileInfo struct {
	name string
	mode os.FileMode
}

func (d *dummyFileInfo) Name() string       { return d.name }
func (d *dummyFileInfo) Size() int64        { return 100 }
func (d *dummyFileInfo) Mode() os.FileMode  { return d.mode }
func (d *dummyFileInfo) ModTime() time.Time { return time.Now() }
func (d *dummyFileInfo) IsDir() bool        { return d.mode.IsDir() }
func (d *dummyFileInfo) Sys() any           { return nil }

func TestValidateRemotePathShape(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"empty path", "", host.ErrRemoteInvalidPath},
		{"relative path", "foo/bar", host.ErrRemoteInvalidPath},
		{"relative with dot", "./foo", host.ErrRemoteInvalidPath},
		{"parent directory segment", "/home/user/../etc", host.ErrRemoteInvalidPath},
		{"parent directory at start", "/../etc", host.ErrRemoteInvalidPath},
		{"path too long", "/" + strings.Repeat("a", 4097), host.ErrRemoteInvalidPath},
		{"pseudo proc", "/proc/1/cmdline", host.ErrRemotePathNotAllowed},
		{"pseudo proc root", "/proc", host.ErrRemotePathNotAllowed},
		{"pseudo sys", "/sys/class/net", host.ErrRemotePathNotAllowed},
		{"pseudo dev", "/dev/null", host.ErrRemotePathNotAllowed},
		{"pseudo uppercase", "/PROC/cpuinfo", host.ErrRemotePathNotAllowed},
		{"valid posix path", "/home/alice/project/main.go", nil},
		{"valid root path", "/etc/hosts", nil},
		{"valid nested path", "/var/log/nginx/access.log", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := host.ValidateRemotePathShape(tc.path)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
			}
		})
	}
}

func parseTestPort(addr string) uint16 {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(p)
}

func TestCanonicalizeRemotePathSymlinkToPseudoFilesystem(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		resolved  string
		wantBlock bool
	}{
		{"symlink to proc", "/home/alice/proclink", "/proc/version", true},
		{"symlink to sys", "/home/alice/syslink", "/sys/kernel", true},
		{"symlink to dev", "/home/alice/devlink", "/dev/zero", true},
		{"symlink to safe", "/home/alice/safelink", "/home/alice/target.txt", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if host.IsBlockedRemotePath(tc.resolved) != tc.wantBlock {
				t.Fatalf(
					"IsBlockedRemotePath(%q) = %v, want %v",
					tc.resolved, host.IsBlockedRemotePath(tc.resolved), tc.wantBlock,
				)
			}
		})
	}
}

func TestVerifyRemoteRegularFile(t *testing.T) {
	cases := []struct {
		name    string
		fi      os.FileInfo
		wantErr error
	}{
		{"regular file", &dummyFileInfo{name: "file.txt", mode: 0644}, nil},
		{"directory", &dummyFileInfo{name: "dir", mode: os.ModeDir | 0755}, host.ErrRemoteNotRegularFile},
		{"socket", &dummyFileInfo{name: "sock", mode: os.ModeSocket | 0666}, host.ErrRemoteNotRegularFile},
		{"named pipe", &dummyFileInfo{name: "pipe", mode: os.ModeNamedPipe | 0666}, host.ErrRemoteNotRegularFile},
		{"device", &dummyFileInfo{name: "dev", mode: os.ModeDevice | 0666}, host.ErrRemoteNotRegularFile},
		{"nil info", nil, os.ErrNotExist},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := host.VerifyRemoteRegularFile(tc.fi)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected nil, got: %v", err)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
			}
		})
	}
}

func TestRemoteCapabilitiesAccountBoundBoundary(t *testing.T) {
	ctx := context.Background()
	listener, pubKey := startCapabilitySSHServer(t, mockServerOptions{
		expectedUser:     "alice",
		expectedPassword: "password123",
		tmuxEnabled:      true,
		sftpEnabled:      true,
	})
	addr := listener.Addr().String()

	regDir := t.TempDir()
	r, err := host.NewRegistry(ctx, regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }() //nolint:errcheck // cleanup

	hostID := host.ID("remote-boundary")
	_, err = r.Save(ctx, host.HostInput{
		ID:             hostID,
		Name:           "Remote Boundary",
		Address:        "127.0.0.1",
		Port:           parseTestPort(addr),
		Username:       "alice",
		CredentialType: "password",
		Credentials:    host.Credentials{Password: "password123"},
		HostKey:        pubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
	defer func() { _ = mgr.Close() }() //nolint:errcheck // cleanup

	caps, err := mgr.CheckCapabilities(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify remote capability metadata reports account-bound boundary
	// and does NOT claim root-handle confinement
	if caps.Files.Boundary != "account_bound" {
		t.Fatalf("expected boundary account_bound, got: %s", caps.Files.Boundary)
	}
	if caps.Files.RootHandleConfinement {
		t.Fatal("remote SFTP must not claim root-handle confinement")
	}
	if !caps.Files.Enabled {
		t.Fatal("remote SFTP should be reported as enabled")
	}
}
