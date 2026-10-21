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

const (
	DefaultMaxRemoteFileSize = 10 * 1024 * 1024 // 10 MiB default
	defaultMaxDirEntries     = 1000
	defaultRemoteOpTimeout   = 30 * time.Second
	stageFilePerm            = 0600
	stagingTokenHexLen       = 16
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

// GetPathCoordinatorForTesting exposes getPathCoordinator for testing.
func GetPathCoordinatorForTesting(hostID ID) *pathCoordinator {
	return getPathCoordinator(hostID)
}

type pathEntry struct {
	owner uint64
	path  string
}

type pathCoordinator struct {
	mu      sync.Mutex
	nextID  uint64
	active  []pathEntry
	waiting map[uint64]uint64
	notify  chan struct{}
}

func newPathCoordinator() *pathCoordinator {
	return &pathCoordinator{
		waiting: make(map[uint64]uint64),
		notify:  make(chan struct{}),
	}
}

var (
	activeStagingMu sync.Mutex
	activeStaging   = make(map[ID]map[string]struct{})
)

func trackActiveStaging(hostID ID, stagePath string) {
	activeStagingMu.Lock()
	defer activeStagingMu.Unlock()
	if activeStaging[hostID] == nil {
		activeStaging[hostID] = make(map[string]struct{})
	}
	activeStaging[hostID][stagePath] = struct{}{}
}

func untrackActiveStaging(hostID ID, stagePath string) {
	activeStagingMu.Lock()
	defer activeStagingMu.Unlock()
	if set, ok := activeStaging[hostID]; ok {
		delete(set, stagePath)
		if len(set) == 0 {
			delete(activeStaging, hostID)
		}
	}
}

// TrackActiveStagingForTesting tracks an active staging path for test assertions.
func TrackActiveStagingForTesting(hostID ID, stagePath string) {
	trackActiveStaging(hostID, stagePath)
}

// UntrackActiveStagingForTesting removes tracking of an active staging path.
func UntrackActiveStagingForTesting(hostID ID, stagePath string) {
	untrackActiveStaging(hostID, stagePath)
}

// ActiveStagingCountForTesting returns the count of actively tracked staging files for a host.
func ActiveStagingCountForTesting(hostID ID) int {
	activeStagingMu.Lock()
	defer activeStagingMu.Unlock()
	return len(activeStaging[hostID])
}

func getActiveStaging(hostID ID) map[string]struct{} {
	activeStagingMu.Lock()
	defer activeStagingMu.Unlock()
	res := make(map[string]struct{})
	for p := range activeStaging[hostID] {
		res[p] = struct{}{}
	}
	return res
}

// LockHandle represents a lock owner context that can acquire multiple locks without self-conflict.
type LockHandle struct {
	coord *pathCoordinator
	id    uint64
}

func (c *pathCoordinator) NewHandle() *LockHandle {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	return &LockHandle{coord: c, id: id}
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

func (c *pathCoordinator) findConflictLocked(ownerID uint64, paths []string) (uint64, bool) {
	for _, p := range paths {
		for _, act := range c.active {
			if act.owner != ownerID && pathsConflict(p, act.path) {
				return act.owner, true
			}
		}
	}
	return 0, false
}

func (c *pathCoordinator) checkDeadlockLocked(ownerID, conflictingOwner uint64) error {
	curr := conflictingOwner
	for curr != 0 {
		if curr == ownerID {
			return fmt.Errorf("%w: path lock deadlock detected", files.ErrConflict)
		}
		curr = c.waiting[curr]
	}
	return nil
}

func (c *pathCoordinator) acquirePathsLocked(ownerID uint64, paths []string) func() {
	delete(c.waiting, ownerID)
	newEntries := make([]pathEntry, len(paths))
	for i, p := range paths {
		newEntries[i] = pathEntry{owner: ownerID, path: p}
	}
	c.active = append(c.active, newEntries...)
	return func() {
		c.mu.Lock()
		toRemove := make(map[string]int)
		for _, p := range paths {
			toRemove[p]++
		}
		newActive := make([]pathEntry, 0, len(c.active))
		for _, act := range c.active {
			if act.owner == ownerID && toRemove[act.path] > 0 {
				toRemove[act.path]--
			} else {
				newActive = append(newActive, act)
			}
		}
		c.active = newActive
		close(c.notify)
		c.notify = make(chan struct{})
		c.mu.Unlock()
	}
}

func (h *LockHandle) Lock(ctx context.Context, paths ...string) (func(), error) {
	c := h.coord
	for {
		c.mu.Lock()
		conflictingOwner, hasConflict := c.findConflictLocked(h.id, paths)
		if !hasConflict {
			unlock := c.acquirePathsLocked(h.id, paths)
			c.mu.Unlock()
			return unlock, nil
		}

		if err := c.checkDeadlockLocked(h.id, conflictingOwner); err != nil {
			c.mu.Unlock()
			return nil, err
		}

		c.waiting[h.id] = conflictingOwner
		notifyCh := c.notify
		c.mu.Unlock()

		select {
		case <-ctx.Done():
			c.mu.Lock()
			delete(c.waiting, h.id)
			c.mu.Unlock()
			return nil, ctx.Err()
		case <-notifyCh:
		}
	}
}

func (c *pathCoordinator) Lock(ctx context.Context, paths ...string) (func(), error) {
	return c.NewHandle().Lock(ctx, paths...)
}

type sftpSession struct {
	conn    *ManagedConn
	adapter *SFTPAdapter
}

func (s *sftpSession) Close() {
	if s.adapter != nil {
		_ = s.adapter.Close() //nolint:errcheck // sftp adapter cleanup
	}
	if s.conn != nil {
		_ = s.conn.Close() //nolint:errcheck // managed connection cleanup
	}
}

// RemoteFileService provides SFTP-backed file operations for a specific host lease.
type RemoteFileService struct {
	lease                   ClientLease
	maxFileSize             int64
	maxDirEntries           int
	spoolDir                string
	enabled                 bool
	stagingMu               sync.Mutex
	staging                 map[string]struct{}
	coord                   *pathCoordinator
	adapterFactory          func(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error)
	registry                *Registry
	beforePosixRenameHook   func()
	afterActiveTrackHook    func(stagePath string)
	afterPendingCleanupHook func(stagePath string)
}

// NewRemoteFileService creates a RemoteFileService tied to a revisioned client lease.
func NewRemoteFileService(
	lease ClientLease,
	maxFileSize int64,
	maxDirEntries int,
	spoolDir string,
) *RemoteFileService {
	if maxFileSize <= 0 {
		maxFileSize = DefaultMaxRemoteFileSize
	}
	if maxDirEntries <= 0 {
		maxDirEntries = defaultMaxDirEntries
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

func (s *RemoteFileService) openSession(ctx context.Context, purpose string) (*sftpSession, error) {
	conn, err := s.lease.OpenDedicated(ctx, purpose)
	if err != nil {
		return nil, err
	}
	adapter, err := s.newAdapter(ctx, conn.Client)
	if err != nil {
		_ = conn.Close() //nolint:errcheck // close conn on adapter failure
		return nil, err
	}
	return &sftpSession{conn: conn, adapter: adapter}, nil
}

func (s *RemoteFileService) SetRegistry(reg *Registry) {
	s.registry = reg
}

// SetAdapterFactoryForTesting overrides SFTP adapter creation for unit tests.
func (s *RemoteFileService) SetAdapterFactoryForTesting(
	f func(ctx context.Context, sshClient *ssh.Client) (*SFTPAdapter, error),
) {
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

// SetAfterActiveTrackHookForTesting registers a hook invoked immediately after tracking active staging.
func (s *RemoteFileService) SetAfterActiveTrackHookForTesting(f func(stagePath string)) {
	s.afterActiveTrackHook = f
}

// SetAfterPendingCleanupHookForTesting registers a hook invoked immediately after AddPendingCleanup.
func (s *RemoteFileService) SetAfterPendingCleanupHookForTesting(f func(stagePath string)) {
	s.afterPendingCleanupHook = f
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

func (s *RemoteFileService) opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	timeout := defaultRemoteOpTimeout
	if s.lease.manager != nil && s.lease.manager.cfg.OperationTimeout > 0 {
		timeout = s.lease.manager.cfg.OperationTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

func (s *RemoteFileService) validateListDirectory(
	ctx context.Context,
	client *sftp.Client,
	dirPath string,
) (string, error) {
	canonicalPath, err := CanonicalizeRemotePath(ctx, client, dirPath)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", fmt.Errorf("%w: %s", files.ErrPathNotAllowed, dirPath)
		}
		return "", fmt.Errorf("%w: %s", files.ErrInvalidPath, dirPath)
	}
	fi, err := client.Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", files.ErrNotFound, dirPath)
		}
		if os.IsPermission(err) {
			return "", fmt.Errorf("%w: %s", files.ErrPermissionDenied, dirPath)
		}
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, dirPath)
	}
	return canonicalPath, nil
}

func (s *RemoteFileService) mapDirEntries(
	ctx context.Context,
	client *sftp.Client,
	canonicalPath string,
	rawEntries []SFTPRemoteEntry,
) ([]files.Entry, error) {
	entries := make([]files.Entry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.HasPrefix(raw.Name, ".vtc-write-") {
			continue
		}

		isDir := raw.IsDir
		size := raw.Size
		if raw.IsLink {
			linkTarget := path.Join(canonicalPath, raw.Name)
			if targetFi, err := client.Stat(linkTarget); err == nil {
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
	return entries, nil
}

// List enumerates directory children with bounded iteration and sorting.
func (s *RemoteFileService) List(ctx context.Context, dirPath string) (files.ListResult, error) {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

	if err := s.lease.CheckValid(); err != nil {
		return files.ListResult{}, err
	}
	if err := ValidateRemotePathShape(dirPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, dirPath)
		}
		return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, dirPath)
	}

	sess, err := s.openSession(ctx, "sftp-list")
	if err != nil {
		return files.ListResult{}, err
	}
	defer sess.Close()

	canonicalPath, err := s.validateListDirectory(ctx, sess.adapter.Client(), dirPath)
	if err != nil {
		return files.ListResult{}, err
	}

	rawEntries, truncated, err := sess.adapter.ReadDirBounded(ctx, canonicalPath, s.maxDirEntries)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrNotFound, dirPath)
		}
		if errors.Is(err, os.ErrPermission) {
			return files.ListResult{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, dirPath)
		}
		return files.ListResult{}, err
	}

	entries, err := s.mapDirEntries(ctx, sess.adapter.Client(), canonicalPath, rawEntries)
	if err != nil {
		return files.ListResult{}, err
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

func (s *RemoteFileService) validateReadFile(
	ctx context.Context,
	client *sftp.Client,
	filePath string,
) (string, os.FileInfo, error) {
	canonicalPath, err := CanonicalizeRemotePath(ctx, client, filePath)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, filePath)
		}
		return "", nil, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}
	fi, err := client.Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrNotFound, filePath)
		}
		if os.IsPermission(err) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrPermissionDenied, filePath)
		}
		return "", nil, err
	}
	if err := VerifyRemoteRegularFile(fi); err != nil {
		return "", nil, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}
	if fi.Size() > s.maxFileSize {
		return "", nil, fmt.Errorf(
			"%w: %s is %d bytes, over %d byte limit",
			files.ErrFileTooLarge, filePath, fi.Size(), s.maxFileSize,
		)
	}
	return canonicalPath, fi, nil
}

func (s *RemoteFileService) spoolRemoteContent(
	client *sftp.Client,
	canonicalPath string,
) (*os.File, int64, error) {
	spool, err := os.CreateTemp(s.spoolDir, "vtc-remote-spool-*")
	if err != nil {
		return nil, 0, fmt.Errorf("create spool file: %w", err)
	}
	_ = os.Remove(spool.Name()) //nolint:errcheck // unlink temporary file

	failSpool := func(err error) (*os.File, int64, error) {
		_ = spool.Close() //nolint:errcheck // spool cleanup
		return nil, 0, err
	}

	remoteFile, err := client.Open(canonicalPath)
	if err != nil {
		return failSpool(err)
	}
	defer func() {
		_ = remoteFile.Close() //nolint:errcheck // remote file cleanup
	}()

	written, err := io.Copy(spool, io.LimitReader(remoteFile, s.maxFileSize+1))
	if err != nil {
		return failSpool(fmt.Errorf("spool remote file: %w", err))
	}
	if written > s.maxFileSize {
		return failSpool(fmt.Errorf(
			"%w: remote file grew over %d bytes limit",
			files.ErrFileTooLarge, s.maxFileSize,
		))
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return failSpool(err)
	}
	return spool, written, nil
}

// Read streams a remote file through a bounded private local spool, checking regular file type,
// size limit, and full-content text/binary classification.
func (s *RemoteFileService) Read(ctx context.Context, filePath string) (files.ReadResult, error) {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

	if err := s.lease.CheckValid(); err != nil {
		return files.ReadResult{}, err
	}
	if err := ValidateRemotePathShape(filePath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, filePath)
		}
		return files.ReadResult{}, fmt.Errorf("%w: %s", files.ErrInvalidPath, filePath)
	}

	sess, err := s.openSession(ctx, "sftp-read")
	if err != nil {
		return files.ReadResult{}, err
	}
	defer sess.Close()

	canonicalPath, fi, err := s.validateReadFile(ctx, sess.adapter.Client(), filePath)
	if err != nil {
		return files.ReadResult{}, err
	}

	spool, written, err := s.spoolRemoteContent(sess.adapter.Client(), canonicalPath)
	if err != nil {
		return files.ReadResult{}, err
	}

	isBinary, err := files.LooksBinary(ctx, spool, written)
	if err != nil {
		_ = spool.Close() //nolint:errcheck // spool cleanup
		return files.ReadResult{}, err
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		_ = spool.Close() //nolint:errcheck // spool cleanup
		return files.ReadResult{}, err
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
	defer func() {
		_ = f.Close() //nolint:errcheck // file close
	}()
	return ComputeContentDigest(f)
}

func (s *RemoteFileService) resolveTargetInParent(
	ctx context.Context,
	client *sftp.Client,
	cleaned string,
	lockHandle *LockHandle,
) (string, func(), error) {
	parent := path.Dir(cleaned)
	base := path.Base(cleaned)
	canonicalParent, err := CanonicalizeRemotePath(ctx, client, parent)
	if err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, parent)
		}
		return "", nil, fmt.Errorf("%w: %s", files.ErrInvalidPath, parent)
	}

	parentFi, err := client.Stat(canonicalParent)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrNotFound, parent)
		}
		if os.IsPermission(err) {
			return "", nil, fmt.Errorf("%w: %s", files.ErrPermissionDenied, parent)
		}
		return "", nil, err
	}
	if !parentFi.IsDir() {
		return "", nil, fmt.Errorf("%w: %s is not a directory", files.ErrInvalidPath, parent)
	}

	target := path.Join(canonicalParent, base)
	if IsBlockedRemotePath(target) {
		return "", nil, fmt.Errorf("%w: %s", files.ErrPathNotAllowed, target)
	}

	var unlockTarget func()
	if target != cleaned {
		unlockTarget, err = lockHandle.Lock(ctx, target)
		if err != nil {
			return "", nil, err
		}
	}
	return target, unlockTarget, nil
}

type targetMetadata struct {
	existed      bool
	originDigest string
	originUID    int
	originGID    int
	curPerm      os.FileMode
}

func (s *RemoteFileService) inspectExistingTarget(
	ctx context.Context,
	adapter *SFTPAdapter,
	target, p string,
	expected *files.ExpectedMtime,
	allowOtherNames bool,
) (targetMetadata, error) {
	targetFi, statErr := adapter.Client().Lstat(target)
	if statErr == nil {
		if !targetFi.Mode().IsRegular() {
			return targetMetadata{}, fmt.Errorf("%w: %s is not a regular file", files.ErrInvalidPath, p)
		}
		var originUID, originGID int
		if fstat, ok := targetFi.Sys().(*sftp.FileStat); ok {
			originUID = int(fstat.UID)
			originGID = int(fstat.GID)
		}
		if !adapter.HasExtension(PosixRenameExtension) {
			return targetMetadata{}, fmt.Errorf(
				"%w: %w: atomic replacement is unsupported by remote server",
				ErrUnsupported, files.ErrWriteFailed,
			)
		}
		if !allowOtherNames {
			return targetMetadata{}, fmt.Errorf(
				"%w: replacement does not update other hard-link names",
				files.ErrHasOtherNames,
			)
		}
		var originDigest string
		if expected != nil {
			if !expected.Matches(targetFi) {
				return targetMetadata{}, fmt.Errorf("%w: %s modification time changed", files.ErrConflict, p)
			}
			var err error
			originDigest, err = s.hashRemoteFile(ctx, adapter.Client(), target)
			if err != nil {
				return targetMetadata{}, err
			}
			if expected.Digest != "" && originDigest != expected.Digest {
				return targetMetadata{}, fmt.Errorf("%w: %s content digest mismatch", files.ErrConflict, p)
			}
		}
		return targetMetadata{
			existed:      true,
			originDigest: originDigest,
			originUID:    originUID,
			originGID:    originGID,
			curPerm:      targetFi.Mode().Perm(),
		}, nil
	}

	if !os.IsNotExist(statErr) {
		if os.IsPermission(statErr) {
			return targetMetadata{}, fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return targetMetadata{}, statErr
	}
	if expected != nil {
		return targetMetadata{}, fmt.Errorf("%w: %s does not exist", files.ErrNotFound, p)
	}
	return targetMetadata{existed: false}, nil
}

func (s *RemoteFileService) createStagingFile(
	ctx context.Context,
	client *sftp.Client,
	parentDir string,
) (string, string, *sftp.File, error) {
	var randBytes [8]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return "", "", nil, fmt.Errorf("generate staging name: %w", err)
	}
	token := hex.EncodeToString(randBytes[:])
	stageName := fmt.Sprintf(".vtc-write-%s", token)
	stagePath := path.Join(parentDir, stageName)
	cleanupID := fmt.Sprintf("%s:%s", s.lease.Target.HostID, token)

	stageFile, err := client.OpenFile(stagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		if os.IsPermission(err) {
			return "", "", nil, fmt.Errorf("%w: %s", files.ErrPermissionDenied, stagePath)
		}
		return "", "", nil, fmt.Errorf("%w: create staging file: %w", files.ErrWriteFailed, err)
	}
	if chmodErr := stageFile.Chmod(stageFilePerm); chmodErr != nil {
		_ = stageFile.Close()        //nolint:errcheck // cleanup staging on error
		_ = client.Remove(stagePath) //nolint:errcheck // remove staging on error
		return "", "", nil, fmt.Errorf(
			"%w: secure staging permissions: %w",
			files.ErrPermissionDenied, chmodErr,
		)
	}

	s.stagingMu.Lock()
	s.staging[stagePath] = struct{}{}
	s.stagingMu.Unlock()
	trackActiveStaging(s.lease.Target.HostID, stagePath)

	if s.afterActiveTrackHook != nil {
		s.afterActiveTrackHook(stagePath)
	}

	if s.registry != nil {
		if err := s.registry.AddPendingCleanup(ctx, PendingCleanupRecord{
			ID:          cleanupID,
			HostID:      s.lease.Target.HostID,
			StagingPath: stagePath,
			Token:       token,
			CreatedAt:   time.Now().Unix(),
		}); err != nil {
			untrackActiveStaging(s.lease.Target.HostID, stagePath)
			s.stagingMu.Lock()
			delete(s.staging, stagePath)
			s.stagingMu.Unlock()
			_ = stageFile.Close()        //nolint:errcheck // error recovery
			_ = client.Remove(stagePath) //nolint:errcheck // error recovery
			return "", "", nil, fmt.Errorf("%w: register pending cleanup: %w", files.ErrWriteFailed, err)
		}
	}
	if s.afterPendingCleanupHook != nil {
		s.afterPendingCleanupHook(stagePath)
	}

	return stagePath, cleanupID, stageFile, nil
}

func (s *RemoteFileService) transferStagedContent(
	ctx context.Context,
	body io.Reader,
	declaredSize int64,
	stageFile *sftp.File,
) error {
	if deadliner, ok := body.(interface{ SetReadDeadline(time.Time) error }); ok {
		stopDeadliner := make(chan struct{})
		defer close(stopDeadliner)
		go func() {
			select {
			case <-ctx.Done():
				_ = deadliner.SetReadDeadline(time.Now()) //nolint:errcheck // abort stuck body read on context done
			case <-stopDeadliner:
			}
		}()
	}
	if closer, ok := body.(io.Closer); ok {
		stopCloser := make(chan struct{})
		defer close(stopCloser)
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close() //nolint:errcheck // body closer on context done
			case <-stopCloser:
			}
		}()
	}

	written, copyErr := io.Copy(stageFile, io.LimitReader(body, declaredSize+1))
	closeErr := stageFile.Close()
	if copyErr != nil {
		return fmt.Errorf("%w: transfer failed: %w", files.ErrWriteFailed, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close staging file: %w", files.ErrWriteFailed, closeErr)
	}
	if written != declaredSize {
		return fmt.Errorf(
			"%w: body size mismatch (expected %d, got %d)",
			files.ErrInvalidBody, declaredSize, written,
		)
	}
	return nil
}

func isConnLossOrDone(ctx context.Context, err error) bool {
	return isConnectionLoss(err) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
}

func (s *RemoteFileService) verifyTargetDigest(
	ctx context.Context,
	client *sftp.Client,
	target string,
	curTargetFi os.FileInfo,
	originDigest string,
	expected *files.ExpectedMtime,
) error {
	if expected == nil {
		return nil
	}
	if !expected.Matches(curTargetFi) {
		return fmt.Errorf("%w: target was modified during write", files.ErrConflict)
	}
	curDigest, err := s.hashRemoteFile(ctx, client, target)
	if err != nil {
		return err
	}
	if (expected.Digest != "" && curDigest != expected.Digest) ||
		(expected.Digest == "" && curDigest != originDigest) {
		return fmt.Errorf("%w: target content changed during write", files.ErrConflict)
	}
	return nil
}

func extractTargetOwnership(curTargetFi os.FileInfo, originUID, originGID int) (int, int) {
	curUID := originUID
	curGID := originGID
	if fstat, ok := curTargetFi.Sys().(*sftp.FileStat); ok {
		curUID = int(fstat.UID)
		curGID = int(fstat.GID)
	}
	return curUID, curGID
}

func (s *RemoteFileService) verifyTargetBeforeReplace(
	ctx context.Context,
	client *sftp.Client,
	target string,
	meta targetMetadata,
	expected *files.ExpectedMtime,
) (int, int, os.FileMode, error) {
	curTargetFi, err := client.Lstat(target)
	if !meta.existed {
		if err == nil {
			return 0, 0, 0, fmt.Errorf("%w: target was created during write", files.ErrConflict)
		}
		return 0, 0, 0, nil
	}

	if err != nil {
		if isConnLossOrDone(ctx, err) {
			return 0, 0, 0, fmt.Errorf("%w: stat before replace: %w", files.ErrWriteFailed, err)
		}
		return 0, 0, 0, fmt.Errorf("%w: target was removed or moved during write", files.ErrConflict)
	}
	if !curTargetFi.Mode().IsRegular() {
		return 0, 0, 0, fmt.Errorf("%w: target is not a regular file", files.ErrInvalidPath)
	}
	if err := s.verifyTargetDigest(ctx, client, target, curTargetFi, meta.originDigest, expected); err != nil {
		return 0, 0, 0, err
	}

	curUID, curGID := extractTargetOwnership(curTargetFi, meta.originUID, meta.originGID)
	return curUID, curGID, curTargetFi.Mode().Perm(), nil
}

func isStageChownNeeded(client *sftp.Client, stagePath string, curUID, curGID int) bool {
	stageFi, statErr := client.Lstat(stagePath)
	if statErr == nil {
		if sstat, ok := stageFi.Sys().(*sftp.FileStat); ok {
			if int(sstat.UID) == curUID && int(sstat.GID) == curGID {
				return false
			}
		}
	}
	return true
}

func (s *RemoteFileService) applyStagedOwnership(
	ctx context.Context,
	client *sftp.Client,
	stagePath string,
	curUID, curGID int,
	curPerm os.FileMode,
) error {
	if err := client.Chmod(stagePath, curPerm); err != nil {
		if isConnLossOrDone(ctx, err) {
			return &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: err}
		}
		return fmt.Errorf("%w: preserve permissions: %w", files.ErrWriteFailed, err)
	}
	if isStageChownNeeded(client, stagePath, curUID, curGID) {
		if err := client.Chown(stagePath, curUID, curGID); err != nil {
			if isConnLossOrDone(ctx, err) {
				return &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: err}
			}
			return fmt.Errorf("%w: preserve owner: %w", files.ErrWriteFailed, err)
		}
	}
	return nil
}

func (s *RemoteFileService) replaceExistingRemoteFile(
	ctx context.Context,
	adapter *SFTPAdapter,
	stagePath, target string,
	curUID, curGID int,
	curPerm os.FileMode,
) error {
	if err := s.applyStagedOwnership(ctx, adapter.Client(), stagePath, curUID, curGID, curPerm); err != nil {
		return err
	}
	if s.beforePosixRenameHook != nil {
		s.beforePosixRenameHook()
	}
	if err := adapter.Client().PosixRename(stagePath, target); err != nil {
		if isConnLossOrDone(ctx, err) {
			return &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: err}
		}
		return fmt.Errorf("%w: posix rename failed: %w", files.ErrWriteFailed, err)
	}
	return nil
}

func (s *RemoteFileService) replaceNewRemoteFile(
	ctx context.Context,
	adapter *SFTPAdapter,
	stagePath, target string,
) error {
	if s.beforePosixRenameHook != nil {
		s.beforePosixRenameHook()
	}
	var renameErr error
	if adapter.HasExtension(PosixRenameExtension) {
		renameErr = adapter.Client().PosixRename(stagePath, target)
	} else {
		renameErr = adapter.Client().Rename(stagePath, target)
	}
	if renameErr != nil {
		if isConnLossOrDone(ctx, renameErr) {
			return &Error{Target: s.lease.Target, Code: ErrOutcomeUnknown, Cause: renameErr}
		}
		return fmt.Errorf("%w: rename failed: %w", files.ErrWriteFailed, renameErr)
	}
	return nil
}

func (s *RemoteFileService) replaceRemoteFile(
	ctx context.Context,
	adapter *SFTPAdapter,
	stagePath, target string,
	meta targetMetadata,
	curUID, curGID int,
	curPerm os.FileMode,
) error {
	if meta.existed {
		return s.replaceExistingRemoteFile(ctx, adapter, stagePath, target, curUID, curGID, curPerm)
	}
	return s.replaceNewRemoteFile(ctx, adapter, stagePath, target)
}

func (s *RemoteFileService) validateWriteRequest(p string, declaredSize int64) (string, error) {
	if err := s.lease.CheckValid(); err != nil {
		return "", err
	}
	if err := ValidateRemotePathShape(p); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", fmt.Errorf("%w: %s", files.ErrPathNotAllowed, p)
		}
		return "", fmt.Errorf("%w: %s", files.ErrInvalidPath, p)
	}
	if declaredSize < 0 {
		return "", fmt.Errorf("%w: declared size is negative", files.ErrInvalidBody)
	}
	if declaredSize > s.maxFileSize {
		return "", fmt.Errorf(
			"%w: %s declared size %d exceeds limit %d",
			files.ErrFileTooLarge, p, declaredSize, s.maxFileSize,
		)
	}
	cleaned := path.Clean(p)
	if cleaned == "/" {
		return "", fmt.Errorf("%w: the filesystem root cannot be written", files.ErrInvalidPath)
	}
	return cleaned, nil
}

func (s *RemoteFileService) stageAndWriteContent(
	ctx context.Context,
	client *sftp.Client,
	target string,
	body io.Reader,
	declaredSize int64,
) (string, func(committed bool), error) {
	stagePath, cleanupID, stageFile, err := s.createStagingFile(
		ctx, client, path.Dir(target),
	)
	if err != nil {
		return "", nil, err
	}

	cleanup := func(committed bool) {
		untrackActiveStaging(s.lease.Target.HostID, stagePath)
		s.stagingMu.Lock()
		delete(s.staging, stagePath)
		s.stagingMu.Unlock()
		if !committed {
			if remErr := client.Remove(stagePath); remErr == nil {
				if s.registry != nil {
					_ = s.registry.RemovePendingCleanup(context.Background(), cleanupID) //nolint:errcheck // cleanup
				}
			}
		} else if s.registry != nil {
			_ = s.registry.RemovePendingCleanup(context.Background(), cleanupID) //nolint:errcheck // cleanup
		}
	}

	if err := s.transferStagedContent(ctx, body, declaredSize, stageFile); err != nil {
		cleanup(false)
		return "", nil, err
	}

	return stagePath, cleanup, nil
}

func (s *RemoteFileService) Write(
	ctx context.Context, p string, body io.Reader, declaredSize int64,
	expected *files.ExpectedMtime, allowOtherNames bool,
) (files.WriteResult, error) {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

	cleaned, err := s.validateWriteRequest(p, declaredSize)
	if err != nil {
		return files.WriteResult{}, err
	}

	lockHandle := s.coord.NewHandle()
	unlock, err := lockHandle.Lock(ctx, cleaned)
	if err != nil {
		return files.WriteResult{}, err
	}
	defer unlock()

	sess, err := s.openSession(ctx, "sftp-write")
	if err != nil {
		return files.WriteResult{}, err
	}
	defer sess.Close()

	target, unlockTarget, err := s.resolveTargetInParent(ctx, sess.adapter.Client(), cleaned, lockHandle)
	if err != nil {
		return files.WriteResult{}, err
	}
	if unlockTarget != nil {
		defer unlockTarget()
	}

	meta, err := s.inspectExistingTarget(ctx, sess.adapter, target, p, expected, allowOtherNames)
	if err != nil {
		return files.WriteResult{}, err
	}

	stagePath, cleanupStage, err := s.stageAndWriteContent(
		ctx, sess.adapter.Client(), target, body, declaredSize,
	)
	if err != nil {
		return files.WriteResult{}, err
	}

	committed := false
	defer func() {
		cleanupStage(committed)
	}()

	curUID, curGID, curPerm, err := s.verifyTargetBeforeReplace(
		ctx, sess.adapter.Client(), target, meta, expected,
	)
	if err != nil {
		return files.WriteResult{}, err
	}

	if err := s.replaceRemoteFile(
		ctx, sess.adapter, stagePath, target, meta, curUID, curGID, curPerm,
	); err != nil {
		return files.WriteResult{}, err
	}

	committed = true

	resFi, err := sess.adapter.Client().Stat(target)
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
	if token == "" || len(token) != stagingTokenHexLen {
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

func (s *RemoteFileService) obtainCleanupAdapter(ctx context.Context) (*SFTPAdapter, func(), error) {
	if s.adapterFactory != nil && s.lease.manager == nil {
		adapter, err := s.adapterFactory(ctx, nil)
		if err != nil {
			return nil, nil, err
		}
		return adapter, func() {
			_ = adapter.Close() //nolint:errcheck // cleanup adapter close
		}, nil
	}
	sess, err := s.openSession(ctx, "sftp-cleanup")
	if err != nil {
		return nil, nil, err
	}
	return sess.adapter, sess.Close, nil
}

func (s *RemoteFileService) cleanupSingleRecord(
	ctx context.Context,
	client *sftp.Client,
	rec PendingCleanupRecord,
) bool {
	if !IsIdentifiableStagingCandidate(rec.StagingPath, rec.Token) ||
		ValidateRemotePathShape(rec.StagingPath) != nil {
		_ = s.registry.RemovePendingCleanup(ctx, rec.ID) //nolint:errcheck // cleanup removal
		return false
	}

	fi, statErr := client.Lstat(rec.StagingPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			_ = s.registry.RemovePendingCleanup(ctx, rec.ID) //nolint:errcheck // cleanup removal
			return true
		}
		return false
	}
	if !fi.Mode().IsRegular() {
		_ = s.registry.RemovePendingCleanup(ctx, rec.ID) //nolint:errcheck // cleanup removal
		return false
	}

	if err := client.Remove(rec.StagingPath); err == nil || os.IsNotExist(err) {
		_ = s.registry.RemovePendingCleanup(ctx, rec.ID) //nolint:errcheck // cleanup removal
		return true
	}
	return false
}

// RunPendingCleanups iterates through pending staging records for this host, verifies each
// staging candidate is an identifiable Hub-owned staging file, removes it, and cleans the record.
func (s *RemoteFileService) RunPendingCleanups(ctx context.Context) (int, error) {
	if s.registry == nil {
		return 0, nil
	}
	records, err := s.registry.ListPendingCleanups(ctx, s.lease.Target.HostID)
	if err != nil || len(records) == 0 {
		return 0, err
	}

	adapter, closeAdapter, err := s.obtainCleanupAdapter(ctx)
	if err != nil {
		return 0, err
	}
	defer closeAdapter()

	activeSet := getActiveStaging(s.lease.Target.HostID)
	cleaned := 0
	for _, rec := range records {
		if err := ctx.Err(); err != nil {
			return cleaned, err
		}
		if _, isActive := activeSet[rec.StagingPath]; isActive {
			continue
		}
		if s.cleanupSingleRecord(ctx, adapter.Client(), rec) {
			cleaned++
		}
	}
	return cleaned, nil
}

func createDirectoryEntry(client *sftp.Client, target, p string) error {
	if err := client.Mkdir(target); err != nil {
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

func createFileEntry(client *sftp.Client, target, p string) error {
	file, err := client.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
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

func (s *RemoteFileService) Create(ctx context.Context, p string, isDir bool) error {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

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

	lockHandle := s.coord.NewHandle()
	unlock, err := lockHandle.Lock(ctx, cleaned)
	if err != nil {
		return err
	}
	defer unlock()

	sess, err := s.openSession(ctx, "sftp-create")
	if err != nil {
		return err
	}
	defer sess.Close()

	target, unlockTarget, err := s.resolveTargetInParent(ctx, sess.adapter.Client(), cleaned, lockHandle)
	if err != nil {
		return err
	}
	if unlockTarget != nil {
		defer unlockTarget()
	}

	if _, err := sess.adapter.Client().Lstat(target); err == nil {
		return fmt.Errorf("%w: %s already exists", files.ErrConflict, p)
	} else if !os.IsNotExist(err) {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, p)
		}
		return err
	}

	if isDir {
		return createDirectoryEntry(sess.adapter.Client(), target, p)
	}
	return createFileEntry(sess.adapter.Client(), target, p)
}

func validateRenamePaths(oldPath, newPath string) (string, string, error) {
	if err := ValidateRemotePathShape(oldPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", "", fmt.Errorf("%w: %s", files.ErrPathNotAllowed, oldPath)
		}
		return "", "", fmt.Errorf("%w: %s", files.ErrInvalidPath, oldPath)
	}
	if err := ValidateRemotePathShape(newPath); err != nil {
		if errors.Is(err, ErrRemotePathNotAllowed) {
			return "", "", fmt.Errorf("%w: %s", files.ErrPathNotAllowed, newPath)
		}
		return "", "", fmt.Errorf("%w: %s", files.ErrInvalidPath, newPath)
	}

	cleanOld := path.Clean(oldPath)
	if cleanOld == "/" {
		return "", "", fmt.Errorf("%w: the filesystem root is not an entry", files.ErrInvalidPath)
	}
	cleanNew := path.Clean(newPath)
	if cleanNew == "/" {
		return "", "", fmt.Errorf("%w: the filesystem root is not an entry", files.ErrInvalidPath)
	}
	return cleanOld, cleanNew, nil
}

func (s *RemoteFileService) resolveRenameTargets(
	ctx context.Context,
	client *sftp.Client,
	cleanOld, cleanNew string,
	lockHandle *LockHandle,
) (string, string, func(), error) {
	srcTarget, unlockSrc, err := s.resolveTargetInParent(ctx, client, cleanOld, lockHandle)
	if err != nil {
		return "", "", nil, err
	}

	if _, err := client.Lstat(srcTarget); err != nil {
		if unlockSrc != nil {
			unlockSrc()
		}
		if os.IsNotExist(err) {
			return "", "", nil, fmt.Errorf("%w: %s", files.ErrNotFound, cleanOld)
		}
		if os.IsPermission(err) {
			return "", "", nil, fmt.Errorf("%w: %s", files.ErrPermissionDenied, cleanOld)
		}
		return "", "", nil, err
	}

	dstTarget, unlockDst, err := s.resolveTargetInParent(ctx, client, cleanNew, lockHandle)
	if err != nil {
		if unlockSrc != nil {
			unlockSrc()
		}
		return "", "", nil, err
	}

	unlockBoth := func() {
		if unlockDst != nil {
			unlockDst()
		}
		if unlockSrc != nil {
			unlockSrc()
		}
	}
	return srcTarget, dstTarget, unlockBoth, nil
}

func (s *RemoteFileService) Rename(ctx context.Context, oldPath, newPath string) error {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

	if err := s.lease.CheckValid(); err != nil {
		return err
	}
	cleanOld, cleanNew, err := validateRenamePaths(oldPath, newPath)
	if err != nil {
		return err
	}

	lockHandle := s.coord.NewHandle()
	unlock, err := lockHandle.Lock(ctx, cleanOld, cleanNew)
	if err != nil {
		return err
	}
	defer unlock()

	sess, err := s.openSession(ctx, "sftp-rename")
	if err != nil {
		return err
	}
	defer sess.Close()

	srcTarget, dstTarget, unlockExtra, err := s.resolveRenameTargets(
		ctx, sess.adapter.Client(), cleanOld, cleanNew, lockHandle,
	)
	if err != nil {
		return err
	}
	if unlockExtra != nil {
		defer unlockExtra()
	}

	if _, err := sess.adapter.Client().Lstat(dstTarget); err == nil {
		return fmt.Errorf("%w: %s already exists", files.ErrConflict, newPath)
	} else if !os.IsNotExist(err) {
		if os.IsPermission(err) {
			return fmt.Errorf("%w: %s", files.ErrPermissionDenied, newPath)
		}
		return err
	}

	if err := sess.adapter.Client().Rename(srcTarget, dstTarget); err != nil {
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

func removeFileTarget(client *sftp.Client, target, p string) error {
	if err := client.Remove(target); err != nil {
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

func (s *RemoteFileService) removeDirectoryTarget(
	ctx context.Context,
	adapter *SFTPAdapter,
	target, p string,
	recursive bool,
) error {
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

func (s *RemoteFileService) Delete(ctx context.Context, p string, recursive bool) error {
	ctx, cancel := s.opContext(ctx)
	defer cancel()

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

	lockHandle := s.coord.NewHandle()
	unlock, err := lockHandle.Lock(ctx, cleaned)
	if err != nil {
		return err
	}
	defer unlock()

	sess, err := s.openSession(ctx, "sftp-delete")
	if err != nil {
		return err
	}
	defer sess.Close()

	target, unlockTarget, err := s.resolveTargetInParent(ctx, sess.adapter.Client(), cleaned, lockHandle)
	if err != nil {
		return err
	}
	if unlockTarget != nil {
		defer unlockTarget()
	}

	info, err := sess.adapter.Client().Lstat(target)
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
		return removeFileTarget(sess.adapter.Client(), target, p)
	}
	return s.removeDirectoryTarget(ctx, sess.adapter, target, p, recursive)
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
