package host

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var (
	pathCoordsMu sync.Mutex
	pathCoords   = make(map[ID]*pathCoordinator)
)

func getPathCoordinator(hostID ID) *pathCoordinator {
	pathCoordsMu.Lock()
	defer pathCoordsMu.Unlock()
	c, ok := pathCoords[hostID]
	if !ok {
		c = newPathCoordinator()
		pathCoords[hostID] = c
	}
	return c
}

type pathCoordinator struct {
	mu     sync.Mutex
	active []string
	notify chan struct{}
}

func newPathCoordinator() *pathCoordinator {
	return &pathCoordinator{
		notify: make(chan struct{}),
	}
}

func pathsConflict(p1, p2 string) bool {
	if p1 == p2 {
		return true
	}
	if p1 == "/" || p2 == "/" {
		return true
	}
	if strings.HasPrefix(p1, p2+"/") {
		return true
	}
	if strings.HasPrefix(p2, p1+"/") {
		return true
	}
	return false
}

func (c *pathCoordinator) Lock(ctx context.Context, paths ...string) (func(), error) {
	for {
		c.mu.Lock()
		hasConflict := false
		for _, p := range paths {
			for _, act := range c.active {
				if pathsConflict(p, act) {
					hasConflict = true
					break
				}
			}
			if hasConflict {
				break
			}
		}

		if !hasConflict {
			c.active = append(c.active, paths...)
			c.mu.Unlock()
			return func() {
				c.mu.Lock()
				toRemove := make(map[string]int)
				for _, p := range paths {
					toRemove[p]++
				}
				newActive := make([]string, 0, len(c.active))
				for _, act := range c.active {
					if toRemove[act] > 0 {
						toRemove[act]--
					} else {
						newActive = append(newActive, act)
					}
				}
				c.active = newActive
				close(c.notify)
				c.notify = make(chan struct{})
				c.mu.Unlock()
			}, nil
		}

		notifyCh := c.notify
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-notifyCh:
			// Retry loop upon notification
		}
	}
}

// RemoteFileService provides SFTP-backed file operations for a specific host lease.
type RemoteFileService struct {
	lease                 ClientLease
	maxFileSize           int64
	maxDirEntries         int
	spoolDir              string
	enabled               bool
	stagingMu             sync.Mutex
	staging               map[string]struct{}
	coord                 *pathCoordinator
	adapterFactory        func(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error)
	registry              *Registry
	beforePosixRenameHook func()
}

// NewRemoteFileService creates a RemoteFileService tied to a revisioned client lease.
func NewRemoteFileService(lease ClientLease, maxFileSize int64, maxDirEntries int, spoolDir string) *RemoteFileService {
	if maxFileSize <= 0 {
		maxFileSize = 10 * 1024 * 1024 // 10 MiB default
	}
	if maxDirEntries <= 0 {
		maxDirEntries = 1000
	}
	if spoolDir == "" {
		spoolDir = os.TempDir()
	}
	return &RemoteFileService{
		lease:         lease,
		maxFileSize:   maxFileSize,
		maxDirEntries: maxDirEntries,
		spoolDir:      spoolDir,
		enabled:       true,
		staging:       make(map[string]struct{}),
		coord:         getPathCoordinator(lease.Target.HostID),
		registry:      lease.Registry(),
	}
}

func (s *RemoteFileService) newAdapter(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error) {
	if s.adapterFactory != nil {
		return s.adapterFactory(ctx, sshClient)
	}
	return NewSFTPAdapter(ctx, sshClient)
}

// SetAdapterFactoryForTesting overrides SFTP adapter creation for unit tests.
func (s *RemoteFileService) SetAdapterFactoryForTesting(f func(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error)) {
	s.adapterFactory = f
}

// SetRegistryForTesting overrides registry for unit tests.
func (s *RemoteFileService) SetRegistryForTesting(r *Registry) {
	s.registry = r
}

// SetBeforePosixRenameHookForTesting registers a hook invoked immediately before PosixRename.
func (s *RemoteFileService) SetBeforePosixRenameHookForTesting(f func()) {
	s.beforePosixRenameHook = f
}

// Enabled reports whether remote file operations are available on this host.
func (s *RemoteFileService) Enabled() bool {
	return s.enabled
}

// StartDirectory returns a sensible starting directory on the remote host.
// If the candidate path violates remote POSIX rules, is relative, traverses with '..',
// or lies under pseudo-filesystems (/proc, /sys, /dev), it falls back to "/" with substituted=true.
func (s *RemoteFileService) StartDirectory(candidate string) (string, bool) {
	if candidate != "" && ValidateRemotePathShape(candidate) == nil {
		return path.Clean(candidate), false
	}
	return "/", true
}

// List enumerates directory children with bounded iteration and sorting.
func (s *RemoteFileService) List(ctx context.Context, dirPath string) (files.ListResult, error) {
	if err := s.lease.CheckValid(); err != nil {
		return files.ListResult{}, err
	}
	if err := ValidateRemotePathShape(dirPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, dirPath)
		}
		return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, dirPath)
	}

	conn, err := s.lease.OpenDedicated(ctx, "sftp-list")
	if err != nil {
		return files.ListResult{}, err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return files.ListResult{}, err
	}
	defer adapter.Close()

	canonicalPath, err := CanonicalizeRemotePath(ctx, adapter.Client(), dirPath)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, dirPath)
		}
		return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, dirPath)
	}

	fi, err := adapter.Client().Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrNotFound, dirPath)
		}
		if os.IsPermission(err) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, dirPath)
		}
		return files.ListResult{}, err
	}
	if !fi.IsDir() {
		return files.ListResult{}, fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, dirPath)
	}

	rawEntries, truncated, err := adapter.ReadDirBounded(ctx, canonicalPath, s.maxDirEntries)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrNotFound, dirPath)
		}
		if errors.Is(err, os.ErrPermission) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, dirPath)
		}
		return files.ListResult{}, err
	}

	entries := make([]files.Entry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		if err := ctx.Err(); err != nil {
			return files.ListResult{}, err
		}
		if strings.HasPrefix(raw.Name, ".vtc-write-") {
			continue
		}

		isDir := raw.IsDir
		size := raw.Size
		if raw.IsLink {
			// Resolve link to check whether it points to a directory
			linkTarget := path.Join(canonicalPath, raw.Name)
			if targetFi, err := adapter.Client().Stat(linkTarget); err == nil {
				isDir = targetFi.IsDir()
				size = targetFi.Size()
			}
		}

		entry := files.Entry{
			Name:  raw.Name,
			IsDir: isDir,
			Mtime: raw.ModTime.UnixMilli(),
		}
		if !isDir {
			entry.Size = size
		}
		entries = append(entries, entry)
	}

	slices.SortStableFunc(entries, files.CompareEntries)

	if len(entries) > s.maxDirEntries {
		entries = entries[:s.maxDirEntries]
		truncated = true
	}

	return files.ListResult{
		Path:      canonicalPath,
		Entries:   entries,
		Truncated: truncated,
	}, nil
}

// Read streams a remote file through a bounded private local spool, checking regular file type,
// size limit, and full-content text/binary classification.
func (s *RemoteFileService) Read(ctx context.Context, filePath string) (files.ReadResult, error) {
	if err := s.lease.CheckValid(); err != nil {
		return files.ReadResult{}, err
	}
	if err := ValidateRemotePathShape(filePath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, filePath)
		}
		return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}

	conn, err := s.lease.OpenDedicated(ctx, "sftp-read")
	if err != nil {
		return files.ReadResult{}, err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return files.ReadResult{}, err
	}
	defer adapter.Close()

	canonicalPath, err := CanonicalizeRemotePath(ctx, adapter.Client(), filePath)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, filePath)
		}
		return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}

	fi, err := adapter.Client().Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrNotFound, filePath)
		}
		if os.IsPermission(err) {
			return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, filePath)
		}
		return files.ReadResult{}, err
	}
	if err := VerifyRemoteRegularFile(fi); err != nil {
		return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}
	if fi.Size() > s.maxFileSize {
		return files.ReadResult{}, fmt.Errorf("%w: %s is %d bytes, over %d byte limit", files.ErrFileTooLarge, filePath, fi.Size(), s.maxFileSize)
	}

	// Create private bounded local spool file
	spool, err := os.CreateTemp(s.spoolDir, "vtc-remote-spool-*")
	if err != nil {
		return files.ReadResult{}, fmt.Errorf("create spool file: %w", err)
	}
	// Unlink immediately so file is cleaned up when closed
	_ = os.Remove(spool.Name())

	failSpool := func(err error) (files.ReadResult, error) {
		_ = spool.Close()
		return files.ReadResult{}, err
	}

	remoteFile, err := adapter.Client().Open(canonicalPath)
	if err != nil {
		return failSpool(err)
	}
	defer remoteFile.Close()

	// Stream remote content into local spool bounded by maxFileSize + 1 to detect growth
	written, err := io.Copy(spool, io.LimitReader(remoteFile, s.maxFileSize+1))
	if err != nil {
		return failSpool(fmt.Errorf("spool remote file: %w", err))
	}
	if written > s.maxFileSize {
		return failSpool(fmt.Errorf("%w: remote file grew over %d bytes limit", files.ErrFileTooLarge, s.maxFileSize))
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return failSpool(err)
	}

	// Full-content binary/text classification
	isBinary, err := files.LooksBinary(ctx, spool, written)
	if err != nil {
		return failSpool(err)
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return failSpool(err)
	}

	return files.ReadResult{
		File:       spool,
		Size:       written,
		Mtime:      fi.ModTime().UnixMilli(),
		MtimeNanos: fi.ModTime().UnixNano(),
		Binary:     isBinary,
	}, nil
}

// ComputeContentDigest calculates SHA-256 digest of content for remote revisions.
func ComputeContentDigest(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *RemoteFileService) hashRemoteFile(ctx context.Context, client *sftp.Client, p string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := client.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return ComputeContentDigest(f)
}

func (s *RemoteFileService) Write(
	ctx context.Context, p string, body io.Reader, declaredSize int64,
	expected *files.ExpectedMtime, allowOtherNames bool,
) (files.WriteResult, error) {
	if err := s.lease.CheckValid(); err != nil {
		return files.WriteResult{}, err
	}
	if err := ValidateRemotePathShape(p); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, p)
		}
		return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, p)
	}
	if declaredSize < 0 {
		return files.WriteResult{}, fmt.Errorf("%w: declared size is negative", files.ErrInvalidBody)
	}
	if declaredSize > s.maxFileSize {
		return files.WriteResult{}, fmt.Errorf("%w: %s declared size %d exceeds limit %d", files.ErrFileTooLarge, p, declaredSize, s.maxFileSize)
	}

	cleaned := path.Clean(p)
	if cleaned == "/" {
		return files.WriteResult{}, fmt.Errorf("%w: the filesystem root cannot be written", files.ErrInvalidPath)
	}

	unlock, err := s.coord.Lock(ctx, cleaned)
	if err != nil {
		return files.WriteResult{}, err
	}
	defer unlock()

	conn, err := s.lease.OpenDedicated(ctx, "sftp-write")
	if err != nil {
		return files.WriteResult{}, err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return files.WriteResult{}, err
	}
	defer adapter.Close()

	parent := path.Dir(cleaned)
	base := path.Base(cleaned)
	canonicalParent, err := CanonicalizeRemotePath(ctx, adapter.Client(), parent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, parent)
		}
		return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, parent)
	}
	parentFi, err := adapter.Client().Stat(canonicalParent)
	if err != nil {
		if os.IsNotExist(err) {
			return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrNotFound, parent)
		}
		if os.IsPermission(err) {
			return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, parent)
		}
		return files.WriteResult{}, err
	}
	if !parentFi.IsDir() {
		return files.WriteResult{}, fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, parent)
	}

	target := path.Join(canonicalParent, base)
	if IsBlockedRemotePath(target) {
		return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, target)
	}

	// 1. Initial target inspection
	targetFi, statErr := adapter.Client().Lstat(target)
	targetExisted := statErr == nil

	var originDigest string
	var originPerm os.FileMode = 0644
	var originUid, originGid int

	if targetExisted {
		if !targetFi.Mode().IsRegular() {
			return files.WriteResult{}, fmt.Errorf("%w: %s is not a regular file", files.ErrInvalidPath, p)
		}
		originPerm = targetFi.Mode().Perm()
		if fstat, ok := targetFi.Sys().(*sftp.FileStat); ok {
			originUid = int(fstat.UID)
			originGid = int(fstat.GID)
		}

		// Remote atomic replacement check: server must support POSIX rename
		if !adapter.HasExtension(PosixRenameExtension) {
			return files.WriteResult{}, fmt.Errorf("%w: %w: atomic replacement is unsupported by remote server", ErrUnsupported, files.ErrWriteFailed)
		}

		// Hard-link limitation check:
		if !allowOtherNames {
			return files.WriteResult{}, fmt.Errorf("%w: replacement does not update other hard-link names", files.ErrHasOtherNames)
		}

		// If normal save (expected != nil), check expected mtime & digest
		if expected != nil {
			if !expected.Matches(targetFi) {
				return files.WriteResult{}, fmt.Errorf("%w: %s modification time changed", files.ErrConflict, p)
			}
			var err error
			originDigest, err = s.hashRemoteFile(ctx, adapter.Client(), target)
			if err != nil {
				return files.WriteResult{}, err
			}
			if expected.Digest != "" && originDigest != expected.Digest {
				return files.WriteResult{}, fmt.Errorf("%w: %s content digest mismatch", files.ErrConflict, p)
			}
		}
	} else {
		if !os.IsNotExist(statErr) {
			if os.IsPermission(statErr) {
				return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
			}
			return files.WriteResult{}, statErr
		}
		// Target did not exist. If caller passed expected mtime for a missing target:
		if expected != nil {
			return files.WriteResult{}, fmt.Errorf("%w: %s does not exist", files.ErrNotFound, p)
		}
	}

	// 2. Stage complete content in target directory
	var randBytes [8]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return files.WriteResult{}, fmt.Errorf("generate staging name: %w", err)
	}
	token := hex.EncodeToString(randBytes[:])
	stageName := fmt.Sprintf(".vtc-write-%s", token)
	stagePath := path.Join(canonicalParent, stageName)
	cleanupID := fmt.Sprintf("%s:%s", s.lease.Target.HostID, token)

	stageFile, err := adapter.Client().OpenFile(stagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		if os.IsPermission(err) {
			return files.WriteResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, stagePath)
		}
		return files.WriteResult{}, fmt.Errorf("%w: create staging file: %w", files.ErrWriteFailed, err)
	}

	if s.registry != nil {
		_ = s.registry.AddPendingCleanup(ctx, PendingCleanupRecord{
			ID:          cleanupID,
			HostID:      s.lease.Target.HostID,
			StagingPath: stagePath,
			Token:       token,
			CreatedAt:   time.Now().Unix(),
		})
	}

	s.stagingMu.Lock()
	s.staging[stagePath] = struct{}{}
	s.stagingMu.Unlock()

	committed := false
	defer func() {
		s.stagingMu.Lock()
		delete(s.staging, stagePath)
		s.stagingMu.Unlock()
		if !committed {
			if remErr := adapter.Client().Remove(stagePath); remErr == nil {
				if s.registry != nil {
					_ = s.registry.RemovePendingCleanup(context.Background(), cleanupID)
				}
			}
		} else {
			if s.registry != nil {
				_ = s.registry.RemovePendingCleanup(context.Background(), cleanupID)
			}
		}
	}()

	// Copy body bounded by declaredSize + 1
	written, copyErr := io.Copy(stageFile, io.LimitReader(body, declaredSize+1))
	closeErr := stageFile.Close()
	if copyErr != nil {
		return files.WriteResult{}, fmt.Errorf("%w: transfer failed: %w", files.ErrWriteFailed, copyErr)
	}
	if closeErr != nil {
		return files.WriteResult{}, fmt.Errorf("%w: close staging file: %w", files.ErrWriteFailed, closeErr)
	}
	if written != declaredSize {
		return files.WriteResult{}, fmt.Errorf("%w: body size mismatch (expected %d, got %d)", files.ErrInvalidBody, declaredSize, written)
	}

	// 3. Re-check target immediately before atomic replacement
	curTargetFi, err := adapter.Client().Lstat(target)
	if targetExisted {
		if err != nil {
			if isConnectionLoss(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return files.WriteResult{}, fmt.Errorf("%w: stat before replace: %w", files.ErrWriteFailed, err)
			}
			// Target was removed or renamed during transfer
			return files.WriteResult{}, fmt.Errorf("%w: target was removed or moved during write", files.ErrConflict)
		}
		if !curTargetFi.Mode().IsRegular() {
			return files.WriteResult{}, fmt.Errorf("%w: target is not a regular file", files.ErrInvalidPath)
		}
		if expected != nil {
			if !expected.Matches(curTargetFi) {
				return files.WriteResult{}, fmt.Errorf("%w: target was modified during write", files.ErrConflict)
			}
			curDigest, err := s.hashRemoteFile(ctx, adapter.Client(), target)
			if err != nil {
				return files.WriteResult{}, err
			}
			if expected.Digest != "" && curDigest != expected.Digest {
				return files.WriteResult{}, fmt.Errorf("%w: target content changed during write", files.ErrConflict)
			}
			if expected.Digest == "" && curDigest != originDigest {
				return files.WriteResult{}, fmt.Errorf("%w: target content changed during write", files.ErrConflict)
			}
		}
	} else {
		if err == nil {
			// Name was taken while write was in flight
			return files.WriteResult{}, fmt.Errorf("%w: target was created during write", files.ErrConflict)
		}
	}

	// 4. Perform atomic replacement
	if s.beforePosixRenameHook != nil {
		s.beforePosixRenameHook()
	}
	if targetExisted {

		_ = adapter.Client().Chmod(stagePath, originPerm)
		_ = adapter.Client().Chown(stagePath, originUid, originGid)
		if err := adapter.Client().PosixRename(stagePath, target); err != nil {
			if isConnectionLoss(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return files.WriteResult{}, &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: err}
			}
			return files.WriteResult{}, fmt.Errorf("%w: posix rename failed: %w", files.ErrWriteFailed, err)
		}
	} else {
		// New file
		var renameErr error
		if adapter.HasExtension(PosixRenameExtension) {
			renameErr = adapter.Client().PosixRename(stagePath, target)
		} else {
			renameErr = adapter.Client().Rename(stagePath, target)
		}
		if renameErr != nil {
			if isConnectionLoss(renameErr) || errors.Is(renameErr, context.Canceled) || errors.Is(renameErr, context.DeadlineExceeded) || ctx.Err() != nil {
				return files.WriteResult{}, &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: renameErr}
			}
			return files.WriteResult{}, fmt.Errorf("%w: rename failed: %w", files.ErrWriteFailed, renameErr)
		}
	}

	committed = true

	resFi, err := adapter.Client().Stat(target)
	if err != nil {
		now := time.Now()
		return files.WriteResult{Mtime: now.UnixMilli(), MtimeNanos: now.UnixNano()}, nil
	}
	return files.WriteResult{
		Mtime:      resFi.ModTime().UnixMilli(),
		MtimeNanos: resFi.ModTime().UnixNano(),
	}, nil
}

// IsIdentifiableStagingCandidate verifies if the given path and token identify a Hub-owned staging file.
func IsIdentifiableStagingCandidate(p string, token string) bool {
	if token == "" || len(token) != 16 {
		return false
	}
	for _, c := range token {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	base := path.Base(p)
	return base == fmt.Sprintf(".vtc-write-%s", token)
}

func isConnectionLoss(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection lost") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "channel closed") ||
		strings.Contains(msg, "session closed")
}

// RunPendingCleanups iterates through pending staging records for this host, verifies each
// staging candidate is an identifiable Hub-owned staging file, removes it, and cleans the record.
func (s *RemoteFileService) RunPendingCleanups(ctx context.Context) (int, error) {
	if s.registry == nil {
		return 0, nil
	}
	records, err := s.registry.ListPendingCleanups(ctx, s.lease.Target.HostID)
	if err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, nil
	}

	var conn *ManagedConn
	var adapter *SFTPAdapter
	if s.adapterFactory != nil && s.lease.manager == nil {
		var err error
		adapter, err = s.adapterFactory(ctx, nil)
		if err != nil {
			return 0, err
		}
	} else {
		var err error
		conn, err = s.lease.OpenDedicated(ctx, "sftp-cleanup")
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		adapter, err = s.newAdapter(ctx, conn.Client)
		if err != nil {
			return 0, err
		}
	}
	defer adapter.Close()

	cleaned := 0
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return cleaned, err
		}
		// Refusal to remove unidentifiable staging candidates:
		// Must match .vtc-write-<token> with exactly 16 hex characters.
		if !IsIdentifiableStagingCandidate(rec.StagingPath, rec.Token) {
			// Unidentifiable candidate: strictly refuse to remove remote file!
			// Remove the invalid DB record so it does not persist indefinitely.
			_ = s.registry.RemovePendingCleanup(ctx, rec.ID)
			continue
		}
		if err := ValidateRemotePathShape(rec.StagingPath); err != nil {
			_ = s.registry.RemovePendingCleanup(ctx, rec.ID)
			continue
		}

		fi, statErr := adapter.Client().Lstat(rec.StagingPath)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				_ = s.registry.RemovePendingCleanup(ctx, rec.ID)
				cleaned++
			}
			continue
		}
		// If not a regular file (e.g. someone created a directory or symlink at that path), refuse to remove!
		if !fi.Mode().IsRegular() {
			_ = s.registry.RemovePendingCleanup(ctx, rec.ID)
			continue
		}

		if err := adapter.Client().Remove(rec.StagingPath); err == nil || os.IsNotExist(err) {
			_ = s.registry.RemovePendingCleanup(ctx, rec.ID)
			cleaned++
		}
	}
	return cleaned, nil
}

func (s *RemoteFileService) Create(ctx context.Context, p string, isDir bool) error {

	if err := s.lease.CheckValid(); err != nil {
		return err
	}
	if err := ValidateRemotePathShape(p); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, p)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, p)
	}

	cleaned := path.Clean(p)
	if cleaned == "/" {
		return fmt.Errorf("%w: %s already exists", files.ErrConflict, p)
	}

	unlock, err := s.coord.Lock(ctx, cleaned)
	if err != nil {
		return err
	}
	defer unlock()

	conn, err := s.lease.OpenDedicated(ctx, "sftp-create")
	if err != nil {
		return err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return err
	}
	defer adapter.Close()

	parent := path.Dir(cleaned)
	base := path.Base(cleaned)

	canonicalParent, err := CanonicalizeRemotePath(ctx, adapter.Client(), parent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, parent)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, parent)
	}

	parentFi, err := adapter.Client().Stat(canonicalParent)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, parent)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, parent)
		}
		return err
	}
	if !parentFi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, parent)
	}

	target := path.Join(canonicalParent, base)
	if IsBlockedRemotePath(target) {
		return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, target)
	}

	// Lstat so dangling symlinks count as existing entries
	if _, err := adapter.Client().Lstat(target); err == nil {
		return fmt.Errorf("%w: %s already exists", files.ErrConflict, p)
	} else if !os.IsNotExist(err) {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return err
	}

	if isDir {
		if err := adapter.Client().Mkdir(target); err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("%w: %s already exists", files.ErrConflict, p)
			}
			if os.IsPermission(err) {
				return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
			}
			return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
		}
		return nil
	}

	file, err := adapter.Client().OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s already exists", files.ErrConflict, p)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
	}
	return file.Close()
}

func (s *RemoteFileService) Rename(ctx context.Context, oldPath, newPath string) error {
	if err := s.lease.CheckValid(); err != nil {
		return err
	}
	if err := ValidateRemotePathShape(oldPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, oldPath)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, oldPath)
	}
	if err := ValidateRemotePathShape(newPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, newPath)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, newPath)
	}

	cleanOld := path.Clean(oldPath)
	if cleanOld == "/" {
		return fmt.Errorf("%w: the filesystem root is not an entry", files.ErrInvalidPath)
	}
	cleanNew := path.Clean(newPath)
	if cleanNew == "/" {
		return fmt.Errorf("%w: the filesystem root is not an entry", files.ErrInvalidPath)
	}

	unlock, err := s.coord.Lock(ctx, cleanOld, cleanNew)
	if err != nil {
		return err
	}
	defer unlock()

	conn, err := s.lease.OpenDedicated(ctx, "sftp-rename")
	if err != nil {
		return err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return err
	}
	defer adapter.Close()

	oldParent := path.Dir(cleanOld)
	oldBase := path.Base(cleanOld)
	canonicalOldParent, err := CanonicalizeRemotePath(ctx, adapter.Client(), oldParent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, oldParent)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, oldParent)
	}
	srcTarget := path.Join(canonicalOldParent, oldBase)
	if IsBlockedRemotePath(srcTarget) {
		return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, oldPath)
	}
	if _, err := adapter.Client().Lstat(srcTarget); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, oldPath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, oldPath)
		}
		return err
	}

	newParent := path.Dir(cleanNew)
	newBase := path.Base(cleanNew)
	canonicalNewParent, err := CanonicalizeRemotePath(ctx, adapter.Client(), newParent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, newParent)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, newParent)
	}
	newParentFi, err := adapter.Client().Stat(canonicalNewParent)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, newPath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, newPath)
		}
		return err
	}
	if !newParentFi.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, newParent)
	}
	dstTarget := path.Join(canonicalNewParent, newBase)
	if IsBlockedRemotePath(dstTarget) {
		return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, newPath)
	}

	// Non-overwriting collision check: target must not exist
	if _, err := adapter.Client().Lstat(dstTarget); err == nil {
		return fmt.Errorf("%w: %s already exists", files.ErrConflict, newPath)
	} else if !os.IsNotExist(err) {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, newPath)
		}
		return err
	}

	if err := adapter.Client().Rename(srcTarget, dstTarget); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s already exists", files.ErrConflict, newPath)
		}
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, oldPath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, newPath)
		}
		return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
	}
	return nil
}

func (s *RemoteFileService) Delete(ctx context.Context, p string, recursive bool) error {
	if err := s.lease.CheckValid(); err != nil {
		return err
	}
	if err := ValidateRemotePathShape(p); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, p)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, p)
	}

	cleaned := path.Clean(p)
	if cleaned == "/" {
		return fmt.Errorf("%w: the filesystem root is not an entry", files.ErrInvalidPath)
	}

	unlock, err := s.coord.Lock(ctx, cleaned)
	if err != nil {
		return err
	}
	defer unlock()

	conn, err := s.lease.OpenDedicated(ctx, "sftp-delete")
	if err != nil {
		return err
	}
	defer conn.Close()

	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		return err
	}
	defer adapter.Close()

	parent := path.Dir(cleaned)
	base := path.Base(cleaned)
	canonicalParent, err := CanonicalizeRemotePath(ctx, adapter.Client(), parent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, parent)
		}
		return fmt.Errorf("%w: %s", files.ErrInvalidPath, parent)
	}
	target := path.Join(canonicalParent, base)
	if IsBlockedRemotePath(target) {
		return fmt.Errorf("%w: %s", files.ErrPathNotAllowed, p)
	}

	info, err := adapter.Client().Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, p)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return err
	}

	if !info.IsDir() {
		if err := adapter.Client().Remove(target); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", files.ErrNotFound, p)
			}
			if os.IsPermission(err) {
				return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
			}
			return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
		}
		return nil
	}

	if !recursive {
		rawEntries, _, err := adapter.ReadDirBounded(ctx, target, 1)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: %s", files.ErrNotFound, p)
			}
			if errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
			}
			return err
		}
		if len(rawEntries) > 0 {
			return fmt.Errorf("%w: %s", files.ErrDirNotEmpty, p)
		}
		if err := adapter.Client().RemoveDirectory(target); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", files.ErrNotFound, p)
			}
			if os.IsPermission(err) {
				return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
			}
			return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
		}
		return nil
	}

	if err := s.removeDirectoryRecursive(ctx, adapter.Client(), target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", files.ErrNotFound, p)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return fmt.Errorf("%w: %w", files.ErrWriteFailed, err)
	}
	return nil
}

func (s *RemoteFileService) removeDirectoryRecursive(ctx context.Context, client *sftp.Client, dirPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := client.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := path.Join(dirPath, entry.Name())
		if entry.IsDir() && entry.Mode()&os.ModeSymlink == 0 {
			if err := s.removeDirectoryRecursive(ctx, client, child); err != nil {
				return err
			}
		} else {
			if err := client.Remove(child); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if err := client.RemoveDirectory(dirPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
