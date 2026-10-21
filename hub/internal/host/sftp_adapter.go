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

	defaultMaxEntries = 1000

	initPacketLen     = 9
	initPayloadLen    = 5
	sftpProtocolVer3  = 3
	maxSFTPPacketSize = 256 * 1024

	uint32Size = 4
	uint64Size = 8

	statusHeaderMinLen = 8
	statusMsgLenOffset = 12
	handleHeaderMinLen = 8
	nameHeaderMinLen   = 8

	s_IFMT   = 0170000
	s_IFDIR  = 0040000
	s_IFLNK  = 0120000
	s_IFSOCK = 0140000
	s_IFIFO  = 0010000
	s_IFCHR  = 0020000
	s_IFBLK  = 0060000
	permMask = 0777
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
func (a *SFTPAdapter) ReadDirBounded(
	ctx context.Context,
	dirPath string,
	maxEntries int,
) ([]SFTPRemoteEntry, bool, error) {
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}

	session, err := a.sshClient.NewSession()
	if err != nil {
		return nil, false, fmt.Errorf("open sftp channel: %w", err)
	}
	defer func() {
		_ = session.Close() //nolint:errcheck // bounded readdir session cleanup
	}()

	rwc, err := session.StdinPipe()
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = rwc.Close() //nolint:errcheck // stdin pipe cleanup
	}()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, false, err
	}

	if err := session.RequestSubsystem("sftp"); err != nil {
		return nil, false, fmt.Errorf("request sftp subsystem: %w", err)
	}

	doneCh := make(chan struct{})
	defer close(doneCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close() //nolint:errcheck // cancel sftp session
		case <-doneCh:
		}
	}()

	if err := sendSFTPInit(rwc, stdout); err != nil {
		return nil, false, err
	}

	var reqID uint32 = 1
	handle, err := openSFTPDir(rwc, stdout, reqID, dirPath)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		reqID++
		closePkt := buildStringPacket(sshFxpClose, reqID, handle)
		_ = writeSFTPPacket(rwc, closePkt) //nolint:errcheck // best effort handle close
	}()

	return fetchDirEntries(ctx, rwc, stdout, handle, &reqID, maxEntries)
}

func writeSFTPPacket(w io.Writer, p []byte) error {
	_, err := w.Write(p)
	return err
}

func sendSFTPInit(rwc io.Writer, stdout io.Reader) error {
	initPkt := make([]byte, initPacketLen)
	binary.BigEndian.PutUint32(initPkt[0:uint32Size], initPayloadLen)
	initPkt[uint32Size] = sshFxpInit
	binary.BigEndian.PutUint32(initPkt[uint32Size+1:initPacketLen], sftpProtocolVer3)
	if _, err := rwc.Write(initPkt); err != nil {
		return fmt.Errorf("send init: %w", err)
	}
	typ, _, err := readSFTPPacket(stdout)
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if typ != sshFxpVersion {
		return fmt.Errorf("expected version packet, got %d", typ)
	}
	return nil
}

func openSFTPDir(rwc io.Writer, stdout io.Reader, reqID uint32, dirPath string) (string, error) {
	openPkt := buildStringPacket(sshFxpOpendir, reqID, dirPath)
	if _, err := rwc.Write(openPkt); err != nil {
		return "", fmt.Errorf("send opendir: %w", err)
	}

	typ, data, err := readSFTPPacket(stdout)
	if err != nil {
		return "", fmt.Errorf("read opendir reply: %w", err)
	}
	if typ == sshFxpStatus {
		_, code, msg := parseStatus(data)
		if code == sshFxNoSuchFile {
			return "", os.ErrNotExist
		}
		if code == sshFxPermissionDenied {
			return "", os.ErrPermission
		}
		return "", fmt.Errorf("opendir failed (%d): %s", code, msg)
	}
	if typ != sshFxpHandle {
		return "", fmt.Errorf("expected handle packet, got %d", typ)
	}
	return parseHandle(data), nil
}

func fetchDirEntries(
	ctx context.Context,
	rwc io.Writer,
	stdout io.Reader,
	handle string,
	reqID *uint32,
	maxEntries int,
) ([]SFTPRemoteEntry, bool, error) {
	var entries []SFTPRemoteEntry
	var truncated bool

	for {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}

		*reqID++
		readPkt := buildStringPacket(sshFxpReaddir, *reqID, handle)
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
			break
		}
	}

	return entries, truncated, nil
}

func readSFTPPacket(r io.Reader) (byte, []byte, error) {
	var lenBuf [uint32Size]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	pktLen := binary.BigEndian.Uint32(lenBuf[:])
	if pktLen == 0 || pktLen > maxSFTPPacketSize {
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
	pktLen := uint32(1 + uint32Size + uint32Size + len(strBytes))
	b := make([]byte, uint32Size+pktLen)
	binary.BigEndian.PutUint32(b[0:uint32Size], pktLen)
	b[uint32Size] = typ
	binary.BigEndian.PutUint32(b[uint32Size+1:uint32Size+1+uint32Size], id)
	offset := uint32Size + 1 + uint32Size
	binary.BigEndian.PutUint32(b[offset:offset+uint32Size], uint32(len(strBytes)))
	copy(b[offset+uint32Size:], strBytes)
	return b
}

func parseStatus(data []byte) (id uint32, code uint32, msg string) {
	if len(data) < statusHeaderMinLen {
		return 0, sshFxFailure, ""
	}
	id = binary.BigEndian.Uint32(data[0:uint32Size])
	code = binary.BigEndian.Uint32(data[uint32Size:statusHeaderMinLen])
	if len(data) >= statusMsgLenOffset {
		msgLen := binary.BigEndian.Uint32(data[statusHeaderMinLen:statusMsgLenOffset])
		if len(data) >= int(statusMsgLenOffset+msgLen) {
			msg = string(data[statusMsgLenOffset : statusMsgLenOffset+msgLen])
		}
	}
	return id, code, msg
}

func parseHandle(data []byte) string {
	if len(data) < handleHeaderMinLen {
		return ""
	}
	handleLen := binary.BigEndian.Uint32(data[uint32Size:handleHeaderMinLen])
	if len(data) >= int(handleHeaderMinLen+handleLen) {
		return string(data[handleHeaderMinLen : handleHeaderMinLen+handleLen])
	}
	return ""
}

func parseNameEntries(data []byte) []SFTPRemoteEntry {
	if len(data) < nameHeaderMinLen {
		return nil
	}
	count := binary.BigEndian.Uint32(data[uint32Size:nameHeaderMinLen])
	data = data[nameHeaderMinLen:]
	entries := make([]SFTPRemoteEntry, 0, count)

	for i := uint32(0); i < count && len(data) >= uint32Size; i++ {
		entry, remaining, ok := parseSingleNameEntry(data)
		if !ok {
			break
		}
		entries = append(entries, entry)
		data = remaining
	}

	return entries
}

func parseSingleNameEntry(data []byte) (SFTPRemoteEntry, []byte, bool) {
	nameLen := int(binary.BigEndian.Uint32(data[0:uint32Size]))
	data = data[uint32Size:]
	if len(data) < nameLen {
		return SFTPRemoteEntry{}, nil, false
	}
	filename := string(data[:nameLen])
	data = data[nameLen:]

	// Discard longname
	if len(data) < uint32Size {
		return SFTPRemoteEntry{}, nil, false
	}
	longLen := int(binary.BigEndian.Uint32(data[0:uint32Size]))
	data = data[uint32Size:]
	if len(data) < longLen {
		return SFTPRemoteEntry{}, nil, false
	}
	data = data[longLen:]

	entry, remaining, ok := parseEntryAttrs(filename, data)
	return entry, remaining, ok
}

func parseEntryAttrs(filename string, data []byte) (SFTPRemoteEntry, []byte, bool) {
	if len(data) < uint32Size {
		return SFTPRemoteEntry{}, nil, false
	}
	flags := binary.BigEndian.Uint32(data[0:uint32Size])
	data = data[uint32Size:]

	var size int64
	if flags&sshFilexferAttrSize != 0 {
		if len(data) < uint64Size {
			return SFTPRemoteEntry{}, nil, false
		}
		size = int64(binary.BigEndian.Uint64(data[0:uint64Size]))
		data = data[uint64Size:]
	}

	if flags&sshFilexferAttrUIDGID != 0 {
		if len(data) < uint64Size {
			return SFTPRemoteEntry{}, nil, false
		}
		data = data[uint64Size:]
	}

	var mode os.FileMode
	var isDir, isLink bool
	if flags&sshFilexferAttrPermissions != 0 {
		if len(data) < uint32Size {
			return SFTPRemoteEntry{}, nil, false
		}
		perm := binary.BigEndian.Uint32(data[0:uint32Size])
		mode, isDir, isLink = decodeFilePerm(perm)
		data = data[uint32Size:]
	}

	var modTime time.Time
	if flags&sshFilexferAttrACModTime != 0 {
		if len(data) < uint64Size {
			return SFTPRemoteEntry{}, nil, false
		}
		mtime := binary.BigEndian.Uint32(data[uint32Size:uint64Size])
		modTime = time.Unix(int64(mtime), 0)
		data = data[uint64Size:]
	}

	if flags&sshFilexferAttrExtended != 0 {
		data = skipExtendedAttrs(data)
	}

	return SFTPRemoteEntry{
		Name:    path.Base(filename),
		IsDir:   isDir,
		IsLink:  isLink,
		Size:    size,
		ModTime: modTime,
		Mode:    mode,
	}, data, true
}

func decodeFilePerm(perm uint32) (mode os.FileMode, isDir, isLink bool) {
	mode = os.FileMode(perm & permMask)
	switch perm & s_IFMT {
	case s_IFDIR:
		isDir = true
		mode |= os.ModeDir
	case s_IFLNK:
		isLink = true
		mode |= os.ModeSymlink
	case s_IFSOCK:
		mode |= os.ModeSocket
	case s_IFIFO:
		mode |= os.ModeNamedPipe
	case s_IFCHR:
		mode |= os.ModeCharDevice
	case s_IFBLK:
		mode |= os.ModeDevice
	}
	return mode, isDir, isLink
}

func skipExtendedAttrs(data []byte) []byte {
	if len(data) < uint32Size {
		return data
	}
	extCount := binary.BigEndian.Uint32(data[0:uint32Size])
	data = data[uint32Size:]
	for j := uint32(0); j < extCount; j++ {
		if len(data) < uint32Size {
			break
		}
		kLen := int(binary.BigEndian.Uint32(data[0:uint32Size]))
		data = data[uint32Size:]
		if len(data) < kLen {
			break
		}
		data = data[kLen:]
		if len(data) < uint32Size {
			break
		}
		vLen := int(binary.BigEndian.Uint32(data[0:uint32Size]))
		data = data[uint32Size:]
		if len(data) < vLen {
			break
		}
		data = data[vLen:]
	}
	return data
}
