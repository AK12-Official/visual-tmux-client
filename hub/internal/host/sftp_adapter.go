package host

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	sshFxpInit          byte = 1
	sshFxpVersion       byte = 2
	sshFxpOpen          byte = 3
	sshFxpClose         byte = 4
	sshFxpRead          byte = 5
	sshFxpWrite         byte = 6
	sshFxpLstat         byte = 7
	sshFxpFstat         byte = 8
	sshFxpSetstat       byte = 9
	sshFxpFsetstat      byte = 10
	sshFxpOpendir       byte = 11
	sshFxpReaddir       byte = 12
	sshFxpRemove        byte = 13
	sshFxpMkdir         byte = 14
	sshFxpRmdir         byte = 15
	sshFxpRealpath      byte = 16
	sshFxpStat          byte = 17
	sshFxpRename        byte = 18
	sshFxpReadlink      byte = 19
	sshFxpSymlink       byte = 20
	sshFxpStatus        byte = 101
	sshFxpHandle        byte = 102
	sshFxpData          byte = 103
	sshFxpName          byte = 104
	sshFxpAttrs         byte = 105
	sshFxpExtended      byte = 200
	sshFxpExtendedReply byte = 201

	sshFxOk               uint32 = 0
	sshFxEOF              uint32 = 1
	sshFxNoSuchFile       uint32 = 2
	sshFxPermissionDenied uint32 = 3
	sshFxFailure          uint32 = 4
	sshFxBadMessage       uint32 = 5
	sshFxNoConnection     uint32 = 6
	sshFxConnectionLost   uint32 = 7
	sshFxOpUnsupported    uint32 = 8

	sshFilexferAttrSize        uint32 = 0x00000001
	sshFilexferAttrUIDGID      uint32 = 0x00000002
	sshFilexferAttrPermissions uint32 = 0x00000004
	sshFilexferAttrACModTime   uint32 = 0x00000008
	sshFilexferAttrExtended    uint32 = 0x80000000

	PosixRenameExtension = "posix-rename@openssh.com"
	FsyncExtension       = "fsync@openssh.com"
)

// SFTPRemoteEntry contains metadata for an entry observed via SFTP.
type SFTPRemoteEntry struct {
	Name    string      `json:"name"`
	IsDir   bool        `json:"isDir"`
	IsLink  bool        `json:"isLink"`
	Size    int64       `json:"size"`
	ModTime time.Time   `json:"modTime"`
	Mode    os.FileMode `json:"mode"`
}

// SFTPAdapter coordinates bounded SFTP directory queries, extensions, and file operations.
type SFTPAdapter struct {
	sshClient  *ssh.Client
	pkgClient  *sftp.Client
	extensions map[string]string
	closed     bool
	mu         sync.Mutex
}

// NewSFTPAdapter negotiates SFTP with the server and extracts advertised protocol extensions.
func NewSFTPAdapter(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error) {
	if sshClient == nil {
		return nil, errors.New("ssh client is nil")
	}

	pkgClient, err := sftp.NewClient(sshClient)
	if err != nil {
		return nil, fmt.Errorf("sftp handshake failed: %w", err)
	}

	adapter := &SFTPAdapter{
		sshClient:  sshClient,
		pkgClient:  pkgClient,
		extensions: make(map[string]string),
	}

	// Capture extensions from pkgClient
	for _, ext := range []string{PosixRenameExtension, FsyncExtension, "statvfs@openssh.com", "hardlink@openssh.com"} {
		if val, ok := pkgClient.HasExtension(ext); ok {
			adapter.extensions[ext] = val
		}
	}

	return adapter, nil
}

// HasExtension checks if an SFTP protocol extension was negotiated during handshake.
func (a *SFTPAdapter) HasExtension(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.extensions[name]
	return ok
}

// SetExtensionForTesting overrides an extension for testing purposes.
func (a *SFTPAdapter) SetExtensionForTesting(name, val string, enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if enabled {
		a.extensions[name] = val
	} else {
		delete(a.extensions, name)
	}
}

// Client returns the underlying *sftp.Client.
func (a *SFTPAdapter) Client() *sftp.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pkgClient
}

// Close closes the SFTP client.
func (a *SFTPAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.pkgClient != nil {
		return a.pkgClient.Close()
	}
	return nil
}

// ReadDirBounded reads directory entries up to maxEntries without consuming the remainder of oversized directories.
// It stops reading and closes the directory handle as soon as maxEntries has been satisfied.
func (a *SFTPAdapter) ReadDirBounded(ctx context.Context, dirPath string, maxEntries int) ([]SFTPRemoteEntry, bool, error) {
	if maxEntries <= 0 {
		maxEntries = 1000
	}

	session, err := a.sshClient.NewSession()
	if err != nil {
		return nil, false, fmt.Errorf("open sftp channel: %w", err)
	}
	defer session.Close()

	rwc, err := session.StdinPipe()
	if err != nil {
		return nil, false, err
	}
	defer rwc.Close()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, false, err
	}

	if err := session.RequestSubsystem("sftp"); err != nil {
		return nil, false, fmt.Errorf("request sftp subsystem: %w", err)
	}

	// Channel closure on context cancellation for effective cancellation
	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-doneCh:
		}
	}()

	// 1. Handshake: send INIT (version 3)
	initPkt := make([]byte, 9)
	binary.BigEndian.PutUint32(initPkt[0:4], 5) // length
	initPkt[4] = sshFxpInit
	binary.BigEndian.PutUint32(initPkt[5:9], 3) // version 3
	if _, err := rwc.Write(initPkt); err != nil {
		return nil, false, fmt.Errorf("send init: %w", err)
	}

	// Read VERSION reply
	typ, verData, err := readSFTPPacket(stdout)
	if err != nil {
		return nil, false, fmt.Errorf("read version: %w", err)
	}
	if typ != sshFxpVersion {
		return nil, false, fmt.Errorf("expected version packet, got %d", typ)
	}
	_ = verData

	// 2. Open directory: SSH_FXP_OPENDIR
	var reqID uint32 = 1
	openPkt := buildStringPacket(sshFxpOpendir, reqID, dirPath)
	if _, err := rwc.Write(openPkt); err != nil {
		return nil, false, fmt.Errorf("send opendir: %w", err)
	}

	typ, data, err := readSFTPPacket(stdout)
	if err != nil {
		return nil, false, fmt.Errorf("read opendir reply: %w", err)
	}
	if typ == sshFxpStatus {
		statusID, code, msg := parseStatus(data)
		_ = statusID
		if code == sshFxNoSuchFile {
			return nil, false, os.ErrNotExist
		}
		if code == sshFxPermissionDenied {
			return nil, false, os.ErrPermission
		}
		return nil, false, fmt.Errorf("opendir failed (%d): %s", code, msg)
	}
	if typ != sshFxpHandle {
		return nil, false, fmt.Errorf("expected handle packet, got %d", typ)
	}
	handle := parseHandle(data)

	// Close handle on exit
	defer func() {
		reqID++
		closePkt := buildStringPacket(sshFxpClose, reqID, handle)
		_, _ = rwc.Write(closePkt)
	}()

	// 3. Loop READDIR until maxEntries or EOF
	var entries []SFTPRemoteEntry
	var truncated bool

	for {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}

		reqID++
		readPkt := buildStringPacket(sshFxpReaddir, reqID, handle)
		if _, err := rwc.Write(readPkt); err != nil {
			return nil, false, fmt.Errorf("send readdir: %w", err)
		}

		typ, data, err := readSFTPPacket(stdout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, false, ctx.Err()
			}
			return nil, false, fmt.Errorf("read readdir reply: %w", err)
		}

		if typ == sshFxpStatus {
			_, code, _ := parseStatus(data)
			if code == sshFxEOF {
				break
			}
			if code == sshFxNoSuchFile {
				return nil, false, os.ErrNotExist
			}
			return nil, false, fmt.Errorf("readdir status code: %d", code)
		}

		if typ != sshFxpName {
			return nil, false, fmt.Errorf("expected name packet, got %d", typ)
		}

		batch := parseNameEntries(data)
		for _, e := range batch {
			if e.Name == "." || e.Name == ".." {
				continue
			}
			if len(entries) < maxEntries {
				entries = append(entries, e)
			} else {
				truncated = true
				break
			}
		}

		if truncated {
			// Work does NOT scale with oversized directories: stop immediately!
			break
		}
	}

	return entries, truncated, nil
}

func readSFTPPacket(r io.Reader) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	pktLen := binary.BigEndian.Uint32(lenBuf[:])
	if pktLen == 0 || pktLen > 256*1024 {
		return 0, nil, fmt.Errorf("invalid sftp packet length: %d", pktLen)
	}
	buf := make([]byte, pktLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

func buildStringPacket(typ byte, id uint32, str string) []byte {
	strBytes := []byte(str)
	pktLen := uint32(1 + 4 + 4 + len(strBytes))
	b := make([]byte, 4+pktLen)
	binary.BigEndian.PutUint32(b[0:4], pktLen)
	b[4] = typ
	binary.BigEndian.PutUint32(b[5:9], id)
	binary.BigEndian.PutUint32(b[9:13], uint32(len(strBytes)))
	copy(b[13:], strBytes)
	return b
}

func parseStatus(data []byte) (id uint32, code uint32, msg string) {
	if len(data) < 8 {
		return 0, sshFxFailure, ""
	}
	id = binary.BigEndian.Uint32(data[0:4])
	code = binary.BigEndian.Uint32(data[4:8])
	if len(data) >= 12 {
		msgLen := binary.BigEndian.Uint32(data[8:12])
		if len(data) >= int(12+msgLen) {
			msg = string(data[12 : 12+msgLen])
		}
	}
	return id, code, msg
}

func parseHandle(data []byte) string {
	if len(data) < 8 {
		return ""
	}
	handleLen := binary.BigEndian.Uint32(data[4:8])
	if len(data) >= int(8+handleLen) {
		return string(data[8 : 8+handleLen])
	}
	return ""
}

func parseNameEntries(data []byte) []SFTPRemoteEntry {
	if len(data) < 8 {
		return nil
	}
	count := binary.BigEndian.Uint32(data[4:8])
	data = data[8:]
	entries := make([]SFTPRemoteEntry, 0, count)

	for i := uint32(0); i < count && len(data) >= 4; i++ {
		nameLen := int(binary.BigEndian.Uint32(data[0:4]))
		data = data[4:]
		if len(data) < nameLen {
			break
		}
		filename := string(data[:nameLen])
		data = data[nameLen:]

		// Discard longname
		if len(data) < 4 {
			break
		}
		longLen := int(binary.BigEndian.Uint32(data[0:4]))
		data = data[4:]
		if len(data) < longLen {
			break
		}
		data = data[longLen:]

		// Parse ATTRS
		if len(data) < 4 {
			break
		}
		flags := binary.BigEndian.Uint32(data[0:4])
		data = data[4:]

		var size int64
		if flags&sshFilexferAttrSize != 0 {
			if len(data) < 8 {
				break
			}
			size = int64(binary.BigEndian.Uint64(data[0:8]))
			data = data[8:]
		}

		if flags&sshFilexferAttrUIDGID != 0 {
			if len(data) < 8 {
				break
			}
			data = data[8:]
		}

		var mode os.FileMode
		var isDir, isLink bool
		if flags&sshFilexferAttrPermissions != 0 {
			if len(data) < 4 {
				break
			}
			perm := binary.BigEndian.Uint32(data[0:4])
			mode = os.FileMode(perm & 0777)
			if perm&0040000 != 0 { // S_IFDIR
				isDir = true
				mode |= os.ModeDir
			}
			if perm&0120000 != 0 { // S_IFLNK
				isLink = true
				mode |= os.ModeSymlink
			}
			data = data[4:]
		}

		var modTime time.Time
		if flags&sshFilexferAttrACModTime != 0 {
			if len(data) < 8 {
				break
			}
			_ = binary.BigEndian.Uint32(data[0:4]) // atime
			mtime := binary.BigEndian.Uint32(data[4:8])
			modTime = time.Unix(int64(mtime), 0)
			data = data[8:]
		}

		if flags&sshFilexferAttrExtended != 0 {
			if len(data) < 4 {
				break
			}
			extCount := binary.BigEndian.Uint32(data[0:4])
			data = data[4:]
			for j := uint32(0); j < extCount; j++ {
				if len(data) < 4 {
					break
				}
				kLen := int(binary.BigEndian.Uint32(data[0:4]))
				data = data[4:]
				if len(data) < kLen {
					break
				}
				data = data[kLen:]
				if len(data) < 4 {
					break
				}
				vLen := int(binary.BigEndian.Uint32(data[0:4]))
				data = data[4:]
				if len(data) < vLen {
					break
				}
				data = data[vLen:]
			}
		}

		entries = append(entries, SFTPRemoteEntry{
			Name:    path.Base(filename),
			IsDir:   isDir,
			IsLink:  isLink,
			Size:    size,
			ModTime: modTime,
			Mode:    mode,
		})
	}

	return entries
}
