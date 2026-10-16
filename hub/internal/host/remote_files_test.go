package host_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func startRealSFTPServer(t *testing.T) (net.Listener, ssh.PublicKey) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	serverConfig := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		for {
			tcpConn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sshConn, chans, reqs, err := ssh.NewServerConn(c, serverConfig)
				if err != nil {
					_ = c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for newChannel := range chans {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					ch, channelReqs, err := newChannel.Accept()
					if err != nil {
						continue
					}
					go func(in <-chan *ssh.Request, channel ssh.Channel) {
						for req := range in {
							if req.Type == "subsystem" && string(req.Payload[4:]) == "sftp" {
								_ = req.Reply(true, nil)
								server, err := sftp.NewServer(channel)
								if err == nil {
									go func() {
										_ = server.Serve()
										_ = channel.Close()
									}()
								}
								return
							}
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
						}
					}(channelReqs, ch)
				}
				_ = sshConn.Wait()
			}(tcpConn)
		}
	}()

	return listener, hostSigner.PublicKey()
}

func setupTestRemoteFiles(t *testing.T) (*host.RemoteFileService, string) {
	t.Helper()
	listener, pubKey := startRealSFTPServer(t)
	addr := listener.Addr().String()

	regDir := t.TempDir()
	r, err := host.NewRegistry(context.Background(), regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	hostID := host.ID("remote-fs")
	_, err = r.Save(context.Background(), host.HostInput{
		ID:             hostID,
		Name:           "Remote FS",
		Address:        "127.0.0.1",
		Port:           parseTestPort(addr),
		Username:       "test",
		CredentialType: "",
		HostKey:        pubKey.Marshal(),
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	mgr := host.NewConnectionManager(host.DefaultConnectionConfig(), r)
	t.Cleanup(func() { _ = mgr.Close() })

	lease, err := mgr.Lease(context.Background(), hostID)
	if err != nil {
		t.Fatal(err)
	}

	testDir := t.TempDir()
	spoolDir := t.TempDir()
	fileService := host.NewRemoteFileService(lease, 1024*1024, 20, spoolDir)

	return fileService, testDir
}

func TestRemoteFilesListBoundedAndOrdering(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Create files and directories in testDir
	_ = os.Mkdir(filepath.Join(testDir, "z_dir"), 0755)
	_ = os.Mkdir(filepath.Join(testDir, "a_dir"), 0755)
	_ = os.WriteFile(filepath.Join(testDir, "b_file.txt"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(testDir, "a_file.txt"), []byte("world"), 0644)
	_ = os.WriteFile(filepath.Join(testDir, ".vtc-write-temp123"), []byte("staging"), 0600) // Staging file should be omitted

	// Symlink to directory
	_ = os.Symlink(filepath.Join(testDir, "a_dir"), filepath.Join(testDir, "link_to_dir"))

	res, err := fileService.List(ctx, testDir)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	// Staging file must be omitted
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Name, ".vtc-write-") {
			t.Fatalf("staging file leaked into listing: %s", e.Name)
		}
	}

	// Verify order: directories first, then files, each group sorted by name
	// Expected order: a_dir, link_to_dir, z_dir, a_file.txt, b_file.txt
	expectedOrder := []struct {
		name  string
		isDir bool
	}{
		{"a_dir", true},
		{"link_to_dir", true},
		{"z_dir", true},
		{"a_file.txt", false},
		{"b_file.txt", false},
	}

	if len(res.Entries) != len(expectedOrder) {
		t.Fatalf("expected %d entries, got %d: %+v", len(expectedOrder), len(res.Entries), res.Entries)
	}

	for i, exp := range expectedOrder {
		if res.Entries[i].Name != exp.name || res.Entries[i].IsDir != exp.isDir {
			t.Errorf("entry %d mismatch: got %+v, want %+v", i, res.Entries[i], exp)
		}
	}
}

func TestRemoteFilesListOversizedDirectoryTruncation(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// Create 35 files (service limit is 20)
	for i := 0; i < 35; i++ {
		_ = os.WriteFile(filepath.Join(testDir, fmt.Sprintf("file_%03d.txt", i)), []byte("data"), 0644)
	}

	res, err := fileService.List(ctx, testDir)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if len(res.Entries) != 20 {
		t.Fatalf("expected 20 entries (capped), got %d", len(res.Entries))
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
}

func TestRemoteFilesListErrors(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Pseudo-filesystem
	_, err := fileService.List(ctx, "/proc")
	if !errors.Is(err, files.ErrPathNotAllowed) {
		t.Fatalf("expected ErrPathNotAllowed for /proc, got: %v", err)
	}

	// 2. Relative path
	_, err = fileService.List(ctx, "relative/path")
	if !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath, got: %v", err)
	}

	// 3. Not a directory
	filePath := filepath.Join(testDir, "plain.txt")
	_ = os.WriteFile(filePath, []byte("content"), 0644)
	_, err = fileService.List(ctx, filePath)
	if !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for file target, got: %v", err)
	}

	// 4. Non-existent path
	_, err = fileService.List(ctx, filepath.Join(testDir, "missing"))
	if !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestRemoteFilesReadAndClassification(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Text file
	textPath := filepath.Join(testDir, "text.txt")
	_ = os.WriteFile(textPath, []byte("Hello, UTF-8 世界!"), 0644)

	readRes, err := fileService.Read(ctx, textPath)
	if err != nil {
		t.Fatalf("read text file failed: %v", err)
	}
	defer readRes.File.Close()

	if readRes.Binary {
		t.Fatal("expected Binary=false for UTF-8 text file")
	}
	content, _ := io.ReadAll(readRes.File)
	if string(content) != "Hello, UTF-8 世界!" {
		t.Fatalf("content mismatch: %s", string(content))
	}

	// 2. Binary file with NUL byte
	binPath := filepath.Join(testDir, "binary.dat")
	_ = os.WriteFile(binPath, []byte("header\x00data"), 0644)

	binRes, err := fileService.Read(ctx, binPath)
	if err != nil {
		t.Fatalf("read binary file failed: %v", err)
	}
	defer binRes.File.Close()

	if !binRes.Binary {
		t.Fatal("expected Binary=true for file containing NUL byte")
	}

	// 3. Binary tail: text followed by invalid UTF-8 bytes at the very tail
	tailPath := filepath.Join(testDir, "tail.log")
	tailBytes := append([]byte(strings.Repeat("valid log line\n", 100)), 0xFF, 0xFE)
	_ = os.WriteFile(tailPath, tailBytes, 0644)

	tailRes, err := fileService.Read(ctx, tailPath)
	if err != nil {
		t.Fatalf("read tail file failed: %v", err)
	}
	defer tailRes.File.Close()

	if !tailRes.Binary {
		t.Fatal("expected Binary=true for file with invalid UTF-8 tail")
	}
}

func TestRemoteFilesReadOversizedAndNonRegular(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Oversized file (> 1 MiB limit)
	largePath := filepath.Join(testDir, "large.bin")
	largeFile, _ := os.Create(largePath)
	_ = largeFile.Truncate(2 * 1024 * 1024) // 2 MiB
	_ = largeFile.Close()

	_, err := fileService.Read(ctx, largePath)
	if !errors.Is(err, files.ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got: %v", err)
	}

	// 2. Directory target
	dirPath := filepath.Join(testDir, "subdir")
	_ = os.Mkdir(dirPath, 0755)

	_, err = fileService.Read(ctx, dirPath)
	if !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for directory, got: %v", err)
	}

	// 3. Missing file
	_, err = fileService.Read(ctx, filepath.Join(testDir, "missing.txt"))
	if !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestRemoteFilesContentDigest(t *testing.T) {
	data := []byte("content to hash for revision check")
	digest1, err := host.ComputeContentDigest(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	digest2, err := host.ComputeContentDigest(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 || len(digest1) != 64 {
		t.Fatalf("digest mismatch: %s != %s", digest1, digest2)
	}

	diffDigest, _ := host.ComputeContentDigest(strings.NewReader("different content"))
	if diffDigest == digest1 {
		t.Fatal("different content produced same digest")
	}
}

func TestRemoteFilesCreate(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Create empty file
	filePath := filepath.Join(testDir, "new_file.txt")
	if err := fileService.Create(ctx, filePath, false); err != nil {
		t.Fatalf("create file failed: %v", err)
	}
	fi, err := os.Stat(filePath)
	if err != nil || fi.IsDir() || fi.Size() != 0 {
		t.Fatalf("created file stat mismatch: fi=%+v, err=%v", fi, err)
	}

	// 2. Collision: creating same file returns ErrConflict
	if err := fileService.Create(ctx, filePath, false); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict creating existing file, got: %v", err)
	}

	// 3. Create directory
	subDir := filepath.Join(testDir, "new_sub_dir")
	if err := fileService.Create(ctx, subDir, true); err != nil {
		t.Fatalf("create dir failed: %v", err)
	}
	dirFi, err := os.Stat(subDir)
	if err != nil || !dirFi.IsDir() {
		t.Fatalf("created dir stat mismatch: fi=%+v, err=%v", dirFi, err)
	}

	// 4. Collision: creating same directory returns ErrConflict
	if err := fileService.Create(ctx, subDir, true); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict creating existing dir, got: %v", err)
	}

	// 5. Collision with dangling symlink: returns ErrConflict
	danglingLink := filepath.Join(testDir, "dangling_link")
	_ = os.Symlink(filepath.Join(testDir, "non_existent_target"), danglingLink)
	if err := fileService.Create(ctx, danglingLink, false); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict creating over dangling symlink, got: %v", err)
	}

	// 6. Parent does not exist: returns ErrNotFound
	missingParentPath := filepath.Join(testDir, "nonexistent", "file.txt")
	if err := fileService.Create(ctx, missingParentPath, false); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing parent, got: %v", err)
	}

	// 7. Parent is not a directory: returns ErrInvalidPath
	fileAsParentPath := filepath.Join(filePath, "file.txt")
	if err := fileService.Create(ctx, fileAsParentPath, false); !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for parent that is a file, got: %v", err)
	}

	// 8. Blocked pseudo-filesystem: returns ErrPathNotAllowed
	if err := fileService.Create(ctx, "/proc/new_file", false); !errors.Is(err, files.ErrPathNotAllowed) {
		t.Fatalf("expected ErrPathNotAllowed for /proc, got: %v", err)
	}

	// 9. Root / already exists: returns ErrConflict
	if err := fileService.Create(ctx, "/", true); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict for root /, got: %v", err)
	}
}

func TestRemoteFilesRename(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Rename regular file
	srcFile := filepath.Join(testDir, "orig.txt")
	_ = os.WriteFile(srcFile, []byte("content to move"), 0644)
	dstFile := filepath.Join(testDir, "moved.txt")

	if err := fileService.Rename(ctx, srcFile, dstFile); err != nil {
		t.Fatalf("rename file failed: %v", err)
	}
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Fatalf("expected source file to be gone after rename, err: %v", err)
	}
	content, err := os.ReadFile(dstFile)
	if err != nil || string(content) != "content to move" {
		t.Fatalf("destination file content mismatch: %s, err: %v", string(content), err)
	}

	// 2. Rename directory
	srcDir := filepath.Join(testDir, "orig_dir")
	_ = os.Mkdir(srcDir, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "inner.txt"), []byte("inner data"), 0644)
	dstDir := filepath.Join(testDir, "moved_dir")

	if err := fileService.Rename(ctx, srcDir, dstDir); err != nil {
		t.Fatalf("rename dir failed: %v", err)
	}
	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Fatalf("expected source dir to be gone, err: %v", err)
	}
	innerContent, err := os.ReadFile(filepath.Join(dstDir, "inner.txt"))
	if err != nil || string(innerContent) != "inner data" {
		t.Fatalf("inner file mismatch after dir rename: %s, err: %v", string(innerContent), err)
	}

	// 3. Collision: destination already exists returns ErrConflict
	collisionDst := filepath.Join(testDir, "existing.txt")
	_ = os.WriteFile(collisionDst, []byte("existing"), 0644)
	if err := fileService.Rename(ctx, dstFile, collisionDst); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict when destination exists, got: %v", err)
	}

	// 4. Collision with dangling symlink as destination returns ErrConflict
	danglingLink := filepath.Join(testDir, "dangling_dst")
	_ = os.Symlink(filepath.Join(testDir, "gone"), danglingLink)
	if err := fileService.Rename(ctx, dstFile, danglingLink); !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict when destination is dangling symlink, got: %v", err)
	}

	// 5. Source does not exist returns ErrNotFound
	missingSrc := filepath.Join(testDir, "nonexistent.txt")
	if err := fileService.Rename(ctx, missingSrc, filepath.Join(testDir, "target.txt")); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing source, got: %v", err)
	}

	// 6. Destination parent does not exist returns ErrNotFound
	if err := fileService.Rename(ctx, dstFile, filepath.Join(testDir, "no_such_parent", "target.txt")); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing dst parent, got: %v", err)
	}

	// 7. Destination parent is a file returns ErrInvalidPath
	if err := fileService.Rename(ctx, dstFile, filepath.Join(collisionDst, "target.txt")); !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for dst parent that is a file, got: %v", err)
	}

	// 8. Root / as source or destination returns ErrInvalidPath
	if err := fileService.Rename(ctx, "/", filepath.Join(testDir, "root_copy")); !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for root as source, got: %v", err)
	}
	if err := fileService.Rename(ctx, dstFile, "/"); !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for root as destination, got: %v", err)
	}

	// 9. Pseudo-filesystem as source or destination returns ErrPathNotAllowed
	if err := fileService.Rename(ctx, "/proc", filepath.Join(testDir, "proc_copy")); !errors.Is(err, files.ErrPathNotAllowed) {
		t.Fatalf("expected ErrPathNotAllowed for /proc source, got: %v", err)
	}
	if err := fileService.Rename(ctx, dstFile, "/proc/bad"); !errors.Is(err, files.ErrPathNotAllowed) {
		t.Fatalf("expected ErrPathNotAllowed for /proc destination, got: %v", err)
	}
}

func TestRemoteFilesDelete(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Delete regular file
	regularFile := filepath.Join(testDir, "to_delete.txt")
	_ = os.WriteFile(regularFile, []byte("delete me"), 0644)
	if err := fileService.Delete(ctx, regularFile, false); err != nil {
		t.Fatalf("delete file failed: %v", err)
	}
	if _, err := os.Stat(regularFile); !os.IsNotExist(err) {
		t.Fatalf("file should not exist after delete, err: %v", err)
	}

	// 2. Delete dangling symlink
	danglingLink := filepath.Join(testDir, "dangling_link")
	_ = os.Symlink(filepath.Join(testDir, "no_such_target"), danglingLink)
	if err := fileService.Delete(ctx, danglingLink, false); err != nil {
		t.Fatalf("delete dangling link failed: %v", err)
	}
	if _, err := os.Lstat(danglingLink); !os.IsNotExist(err) {
		t.Fatalf("dangling link should be gone, err: %v", err)
	}

	// 3. Delete symlink to directory: must remove symlink, NOT target directory or its contents
	targetDir := filepath.Join(testDir, "symlink_target_dir")
	_ = os.Mkdir(targetDir, 0755)
	_ = os.WriteFile(filepath.Join(targetDir, "keep.txt"), []byte("keep me"), 0644)
	symlinkToDir := filepath.Join(testDir, "link_to_target_dir")
	_ = os.Symlink(targetDir, symlinkToDir)

	if err := fileService.Delete(ctx, symlinkToDir, false); err != nil {
		t.Fatalf("delete symlink to dir failed: %v", err)
	}
	if _, err := os.Lstat(symlinkToDir); !os.IsNotExist(err) {
		t.Fatalf("symlink to dir should be gone, err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "keep.txt")); err != nil {
		t.Fatalf("target directory contents must NOT be removed when deleting a symlink, err: %v", err)
	}

	// 4. Delete empty directory (recursive = false)
	emptyDir := filepath.Join(testDir, "empty_dir")
	_ = os.Mkdir(emptyDir, 0755)
	if err := fileService.Delete(ctx, emptyDir, false); err != nil {
		t.Fatalf("delete empty dir failed: %v", err)
	}
	if _, err := os.Stat(emptyDir); !os.IsNotExist(err) {
		t.Fatalf("empty dir should be gone, err: %v", err)
	}

	// 5. Non-empty directory with recursive = false: returns ErrDirNotEmpty
	nonEmptyDir := filepath.Join(testDir, "non_empty_dir")
	_ = os.Mkdir(nonEmptyDir, 0755)
	_ = os.WriteFile(filepath.Join(nonEmptyDir, "child.txt"), []byte("child"), 0644)

	if err := fileService.Delete(ctx, nonEmptyDir, false); !errors.Is(err, files.ErrDirNotEmpty) {
		t.Fatalf("expected ErrDirNotEmpty for non-empty dir without recursive, got: %v", err)
	}
	if _, err := os.Stat(nonEmptyDir); err != nil {
		t.Fatalf("non-empty dir should still exist after refused delete: %v", err)
	}

	// 6. Confirmed recursive delete (recursive = true): removes tree completely
	subSubDir := filepath.Join(nonEmptyDir, "sub_sub")
	_ = os.Mkdir(subSubDir, 0755)
	_ = os.WriteFile(filepath.Join(subSubDir, "deep.txt"), []byte("deep"), 0644)
	// Add a symlink inside the tree pointing outside to ensure it doesn't delete outside files
	outsideFile := filepath.Join(testDir, "safe_outside.txt")
	_ = os.WriteFile(outsideFile, []byte("safe"), 0644)
	_ = os.Symlink(outsideFile, filepath.Join(subSubDir, "link_to_outside"))

	if err := fileService.Delete(ctx, nonEmptyDir, true); err != nil {
		t.Fatalf("recursive delete failed: %v", err)
	}
	if _, err := os.Stat(nonEmptyDir); !os.IsNotExist(err) {
		t.Fatalf("nonEmptyDir should be completely deleted, err: %v", err)
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("recursive delete must not follow symlinks outside: %v", err)
	}

	// 7. Non-existent path returns ErrNotFound
	if err := fileService.Delete(ctx, filepath.Join(testDir, "missing_entry"), false); !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing path, got: %v", err)
	}

	// 8. Root / returns ErrInvalidPath
	if err := fileService.Delete(ctx, "/", true); !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for root /, got: %v", err)
	}

	// 9. Pseudo-filesystem returns ErrPathNotAllowed
	if err := fileService.Delete(ctx, "/proc", true); !errors.Is(err, files.ErrPathNotAllowed) {
		t.Fatalf("expected ErrPathNotAllowed for /proc, got: %v", err)
	}
}

func TestRemoteFilesStartDirectoryAndWhitespace(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. StartDirectory tests
	tests := []struct {
		name            string
		candidate       string
		wantPath        string
		wantSubstituted bool
	}{
		{"valid absolute path", "/home/remote/workspace", "/home/remote/workspace", false},
		{"valid path with trailing slash", "/home/remote/workspace/", "/home/remote/workspace", false},
		{"valid path with spaces", "/home/remote/my documents/project 1", "/home/remote/my documents/project 1", false},
		{"empty candidate falls back to root", "", "/", true},
		{"relative path falls back to root", "relative/dir", "/", true},
		{"parent directory segment falls back to root", "/home/remote/../etc", "/", true},
		{"pseudo-filesystem /proc falls back to root", "/proc/123/cwd", "/", true},
		{"pseudo-filesystem /sys falls back to root", "/sys", "/", true},
		{"pseudo-filesystem /dev falls back to root", "/dev", "/", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotSubstituted := fileService.StartDirectory(tc.candidate)
			if gotPath != tc.wantPath || gotSubstituted != tc.wantSubstituted {
				t.Errorf("StartDirectory(%q) = (%q, %v), want (%q, %v)", tc.candidate, gotPath, gotSubstituted, tc.wantPath, tc.wantSubstituted)
			}
		})
	}

	// 2. Whitespace operations
	spaceDir := filepath.Join(testDir, "folder with spaces")
	if err := fileService.Create(ctx, spaceDir, true); err != nil {
		t.Fatalf("create folder with spaces failed: %v", err)
	}

	spaceFile := filepath.Join(spaceDir, "file with spaces.txt")
	if err := fileService.Create(ctx, spaceFile, false); err != nil {
		t.Fatalf("create file with spaces failed: %v", err)
	}

	renamedSpaceFile := filepath.Join(spaceDir, "renamed with spaces.txt")
	if err := fileService.Rename(ctx, spaceFile, renamedSpaceFile); err != nil {
		t.Fatalf("rename file with spaces failed: %v", err)
	}

	if _, err := os.Stat(renamedSpaceFile); err != nil {
		t.Fatalf("renamed file with spaces should exist: %v", err)
	}

	if err := fileService.Delete(ctx, renamedSpaceFile, false); err != nil {
		t.Fatalf("delete file with spaces failed: %v", err)
	}

	if err := fileService.Delete(ctx, spaceDir, false); err != nil {
		t.Fatalf("delete dir with spaces failed: %v", err)
	}
}

func TestRemoteFilesRemoteLocalPathSeparation(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// Path in testDir is outside any standard local project roots.
	// Verify that remote file operations on testDir succeed completely
	// without any cross-root or not-allowed errors from local confinement rules.
	remoteCustomPath := filepath.Join(testDir, "remote_only_dir")
	if err := fileService.Create(ctx, remoteCustomPath, true); err != nil {
		t.Fatalf("remote create failed: %v", err)
	}
	remoteCustomFile := filepath.Join(remoteCustomPath, "remote_file.txt")
	if err := fileService.Create(ctx, remoteCustomFile, false); err != nil {
		t.Fatalf("remote create file failed: %v", err)
	}

	res, err := fileService.List(ctx, remoteCustomPath)
	if err != nil {
		t.Fatalf("remote list failed: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Name != "remote_file.txt" {
		t.Fatalf("remote list entries mismatch: %+v", res.Entries)
	}
}

func TestRemoteFilesContentDerivedPreconditions(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	filePath := filepath.Join(testDir, "precondition.txt")
	initialBytes := []byte("initial data")
	if err := os.WriteFile(filePath, initialBytes, 0644); err != nil {
		t.Fatal(err)
	}

	readRes, err := fileService.Read(ctx, filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer readRes.File.Close()

	initialDigest, err := host.ComputeContentDigest(readRes.File)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Normal save with matching expected mtime and digest succeeds
	expected := &files.ExpectedMtime{
		Millis: readRes.Mtime,
		Nanos:  &readRes.MtimeNanos,
		Digest: initialDigest,
	}
	newBody := strings.NewReader("updated data v1")
	writeRes, err := fileService.Write(ctx, filePath, newBody, int64(len("updated data v1")), expected, true)
	if err != nil {
		t.Fatalf("normal save failed: %v", err)
	}
	if writeRes.Mtime == 0 {
		t.Fatalf("expected non-zero resulting mtime")
	}

	// 2. Same-time, same-size content change detection:
	// External process modifies file from "updated data v1" (15 bytes) to "tampered bytes!" (15 bytes)
	// and restores the modification time to the exact writeRes mtime!
	tamperedBytes := []byte("tampered bytes!") // exactly 15 bytes
	if err := os.WriteFile(filePath, tamperedBytes, 0644); err != nil {
		t.Fatal(err)
	}
	resTime := time.Unix(0, writeRes.MtimeNanos)
	if err := os.Chtimes(filePath, resTime, resTime); err != nil {
		t.Fatal(err)
	}

	// Caller attempts to save using observed writeRes timestamp AND old digest
	staleExpected := &files.ExpectedMtime{
		Millis: writeRes.Mtime,
		Nanos:  &writeRes.MtimeNanos,
		Digest: initialDigest, // does not match tampered bytes
	}
	failBody := strings.NewReader("save attempting to overwrite")
	_, err = fileService.Write(ctx, filePath, failBody, int64(len("save attempting to overwrite")), staleExpected, true)
	if !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict for same-time same-size content change, got: %v", err)
	}

	// Verify file content was preserved
	curBytes, err := os.ReadFile(filePath)
	if err != nil || string(curBytes) != "tampered bytes!" {
		t.Fatalf("file content corrupted after conflict: %s", string(curBytes))
	}
}

func TestRemoteFilesSerializationConflictingHubOperations(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Two browser saves race on the same file:
	filePath := filepath.Join(testDir, "two_saves.txt")
	_ = os.WriteFile(filePath, []byte("start"), 0644)
	readRes, err := fileService.Read(ctx, filePath)
	if err != nil {
		t.Fatal(err)
	}
	initialDigest, err := host.ComputeContentDigest(readRes.File)
	if err != nil {
		t.Fatal(err)
	}
	_ = readRes.File.Close()

	// Both writers observe the same read result and content digest
	expected1 := &files.ExpectedMtime{Millis: readRes.Mtime, Nanos: &readRes.MtimeNanos, Digest: initialDigest}
	expected2 := &files.ExpectedMtime{Millis: readRes.Mtime, Nanos: &readRes.MtimeNanos, Digest: initialDigest}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, errs[0] = fileService.Write(ctx, filePath, strings.NewReader("writer1 content"), int64(len("writer1 content")), expected1, true)
	}()
	go func() {
		defer wg.Done()
		_, errs[1] = fileService.Write(ctx, filePath, strings.NewReader("writer2 content"), int64(len("writer2 content")), expected2, true)
	}()
	wg.Wait()

	// Exactly one must succeed and one must fail with ErrConflict
	successCount := 0
	conflictCount := 0
	for _, err := range errs {
		if err == nil {
			successCount++
		} else if errors.Is(err, files.ErrConflict) {
			conflictCount++
		}
	}
	if successCount != 1 || conflictCount != 1 {
		t.Fatalf("expected 1 success and 1 conflict for two browser saves, got errs: %v", errs)
	}

	// 2. Save / Delete race on the same file:
	raceFilePath := filepath.Join(testDir, "race_delete.txt")
	_ = os.WriteFile(raceFilePath, []byte("race"), 0644)

	var wg2 sync.WaitGroup
	var writeErr, delErr error
	wg2.Add(2)

	go func() {
		defer wg2.Done()
		_, writeErr = fileService.Write(ctx, raceFilePath, strings.NewReader("write during delete"), int64(len("write during delete")), nil, true)
	}()
	go func() {
		defer wg2.Done()
		delErr = fileService.Delete(ctx, raceFilePath, false)
	}()
	wg2.Wait()

	if writeErr != nil && !errors.Is(writeErr, files.ErrConflict) && !errors.Is(writeErr, files.ErrNotFound) {
		t.Fatalf("unexpected write error in save/delete race: %v", writeErr)
	}
	if delErr != nil && !errors.Is(delErr, files.ErrNotFound) {
		t.Fatalf("unexpected delete error in save/delete race: %v", delErr)
	}

	// Either Write ran first then Delete (both succeeded, file gone),
	// or Delete ran first (Delete succeeded, Write returned ErrConflict/ErrNotFound because target removed).
	// Crucially, neither leaked temporary staging files or panicked!
	stagingFiles, _ := filepath.Glob(filepath.Join(testDir, ".vtc-write-*"))
	if len(stagingFiles) != 0 {
		t.Fatalf("leaked staging files after save/delete race: %v", stagingFiles)
	}

	// 3. Write and Ancestor Delete race:
	treeDir := filepath.Join(testDir, "ancestor_tree")
	_ = os.MkdirAll(filepath.Join(treeDir, "sub"), 0755)
	treeFile := filepath.Join(treeDir, "sub", "target.txt")
	_ = os.WriteFile(treeFile, []byte("nested"), 0644)

	var wg3 sync.WaitGroup
	var ancestorWriteErr, ancestorDelErr error
	wg3.Add(2)

	go func() {
		defer wg3.Done()
		_, ancestorWriteErr = fileService.Write(ctx, treeFile, strings.NewReader("update nested"), int64(len("update nested")), nil, true)
	}()
	go func() {
		defer wg3.Done()
		ancestorDelErr = fileService.Delete(ctx, treeDir, true)
	}()
	wg3.Wait()

	// Conflicting operations serialized cleanly:
	// If write was first: write succeeded, then ancestor delete deleted directory.
	// If delete was first: delete succeeded, write returned ErrNotFound.
	if ancestorWriteErr != nil && !errors.Is(ancestorWriteErr, files.ErrNotFound) && !errors.Is(ancestorWriteErr, files.ErrConflict) {
		t.Fatalf("unexpected ancestor write error: %v", ancestorWriteErr)
	}
	if ancestorDelErr != nil {
		t.Fatalf("ancestor delete failed: %v", ancestorDelErr)
	}
}

func TestRemoteFilesWriteStagingAndAtomicReplacement(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Length mismatch returns ErrInvalidBody and cleans up staging
	filePath := filepath.Join(testDir, "length_check.txt")
	body := strings.NewReader("actual 15 bytes")
	_, err := fileService.Write(ctx, filePath, body, 10, nil, true)
	if !errors.Is(err, files.ErrInvalidBody) {
		t.Fatalf("expected ErrInvalidBody for length mismatch, got: %v", err)
	}
	stagingFiles, _ := filepath.Glob(filepath.Join(testDir, ".vtc-write-*"))
	if len(stagingFiles) != 0 {
		t.Fatalf("staging file not cleaned up on length mismatch: %v", stagingFiles)
	}

	// 2. Hard-link limitation check: allowOtherNames = false on existing target returns ErrHasOtherNames
	existingPath := filepath.Join(testDir, "existing_target.txt")
	_ = os.WriteFile(existingPath, []byte("target"), 0644)
	_, err = fileService.Write(ctx, existingPath, strings.NewReader("new content"), int64(len("new content")), nil, false)
	if !errors.Is(err, files.ErrHasOtherNames) {
		t.Fatalf("expected ErrHasOtherNames when allowOtherNames=false on existing target, got: %v", err)
	}

	// With allowOtherNames = true, write succeeds
	_, err = fileService.Write(ctx, existingPath, strings.NewReader("new content"), int64(len("new content")), nil, true)
	if err != nil {
		t.Fatalf("write with allowOtherNames=true failed: %v", err)
	}
	content, _ := os.ReadFile(existingPath)
	if string(content) != "new content" {
		t.Fatalf("content mismatch: %s", string(content))
	}

	// 3. Metadata preservation: existing file mode 0755 is preserved across replacement
	scriptPath := filepath.Join(testDir, "script.sh")
	_ = os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0755)
	_, err = fileService.Write(ctx, scriptPath, strings.NewReader("#!/bin/sh\necho hi\n"), int64(len("#!/bin/sh\necho hi\n")), nil, true)
	if err != nil {
		t.Fatalf("replace script failed: %v", err)
	}
	scriptFi, _ := os.Stat(scriptPath)
	if scriptFi.Mode().Perm() != 0755 {
		t.Fatalf("expected preserved mode 0755, got: %v", scriptFi.Mode().Perm())
	}

	// 4. Forced save rechecks target type: writing over directory returns ErrInvalidPath
	dirTarget := filepath.Join(testDir, "target_dir")
	_ = os.Mkdir(dirTarget, 0755)
	_, err = fileService.Write(ctx, dirTarget, strings.NewReader("data"), int64(len("data")), nil, true)
	if !errors.Is(err, files.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath when writing over directory, got: %v", err)
	}

	// 5. Normal save to non-existent target returns ErrNotFound
	missingPath := filepath.Join(testDir, "non_existent.txt")
	now := time.Now().UnixMilli()
	_, err = fileService.Write(ctx, missingPath, strings.NewReader("data"), int64(len("data")), &files.ExpectedMtime{Millis: now}, true)
	if !errors.Is(err, files.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for normal save to missing target, got: %v", err)
	}

	// 6. Target removed while write was in flight: forced save returns ErrConflict and does not recreate target
	removedPath := filepath.Join(testDir, "remove_mid_write.txt")
	_ = os.WriteFile(removedPath, []byte("start data"), 0644)

	removeReader := &callbackReader{
		r: strings.NewReader("new data"),
		onRead: func() {
			_ = os.Remove(removedPath)
		},
	}
	_, err = fileService.Write(ctx, removedPath, removeReader, int64(len("new data")), nil, true)
	if !errors.Is(err, files.ErrConflict) {
		t.Fatalf("expected ErrConflict when target removed during upload, got: %v", err)
	}
	if _, err := os.Stat(removedPath); !os.IsNotExist(err) {
		t.Fatalf("target must NOT be recreated if removed during upload, stat err: %v", err)
	}

	// 7. Unsupported atomic replacement: server never uses delete-then-rename or truncate-in-place
	unsupportedFile := filepath.Join(testDir, "unsupported_replacement.txt")
	_ = os.WriteFile(unsupportedFile, []byte("precious original data"), 0644)

	fileService.SetAdapterFactoryForTesting(func(ctx context.Context, sshClient *ssh.Client) (*host.SFTPAdapter, error) {
		ad, err := host.NewSFTPAdapter(ctx, sshClient)
		if err != nil {
			return nil, err
		}
		ad.SetExtensionForTesting(host.PosixRenameExtension, "", false)
		return ad, nil
	})
	defer fileService.SetAdapterFactoryForTesting(nil)

	_, err = fileService.Write(ctx, unsupportedFile, strings.NewReader("new payload"), int64(len("new payload")), nil, true)
	if !errors.Is(err, files.ErrWriteFailed) {
		t.Fatalf("expected ErrWriteFailed when atomic replacement is unsupported, got: %v", err)
	}

	// Verify original file was neither deleted nor truncated
	unsupportedContent, err := os.ReadFile(unsupportedFile)
	if err != nil || string(unsupportedContent) != "precious original data" {
		t.Fatalf("file content must be preserved intact without truncate or delete: got %q, err: %v", string(unsupportedContent), err)
	}

	// Verify no staging file left behind
	stagingFiles, _ = filepath.Glob(filepath.Join(testDir, ".vtc-write-*"))
	if len(stagingFiles) != 0 {
		t.Fatalf("staging files leaked after unsupported replacement refusal: %v", stagingFiles)
	}

	// However, creating a brand new file on an unsupported server still works using standard non-overwriting rename
	brandNewFile := filepath.Join(testDir, "brand_new_file.txt")
	_, err = fileService.Write(ctx, brandNewFile, strings.NewReader("brand new payload"), int64(len("brand new payload")), nil, true)
	if err != nil {
		t.Fatalf("creating new file on server without posix-rename should succeed: %v", err)
	}
	newContent, _ := os.ReadFile(brandNewFile)
	if string(newContent) != "brand new payload" {
		t.Fatalf("new file content mismatch: %s", string(newContent))
	}
}

type callbackReader struct {
	r      io.Reader
	onRead func()
	once   sync.Once
}

func (cr *callbackReader) Read(p []byte) (int, error) {
	cr.once.Do(cr.onRead)
	return cr.r.Read(p)
}

func TestRemoteFilesPendingStagingAndOutcomeUnknown(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	// 1. Verify Bounded Pending Cleanup Records (Max 50)
	regDir := t.TempDir()
	store, err := host.OpenStore(ctx, regDir)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer store.Close()

	testHostID := host.ID("bounded-host")
	for i := 1; i <= 55; i++ {
		token := fmt.Sprintf("%016x", i)
		err := store.AddPendingCleanup(ctx, host.PendingCleanupRecord{
			ID:          fmt.Sprintf("clean-%d", i),
			HostID:      testHostID,
			StagingPath: fmt.Sprintf("/tmp/.vtc-write-%s", token),
			Token:       token,
			CreatedAt:   int64(1000 + i),
		})
		if err != nil {
			t.Fatalf("AddPendingCleanup failed at %d: %v", i, err)
		}
	}

	records, err := store.ListPendingCleanups(ctx, testHostID)
	if err != nil {
		t.Fatalf("ListPendingCleanups failed: %v", err)
	}
	if len(records) != host.MaxPendingCleanupsPerHost {
		t.Fatalf("expected bounded count of %d, got %d", host.MaxPendingCleanupsPerHost, len(records))
	}
	// Verify oldest 5 records (1..5) were evicted, so first record is 6
	expectedFirstToken := fmt.Sprintf("%016x", 6)
	if records[0].Token != expectedFirstToken {
		t.Fatalf("expected first record token to be %s, got %s", expectedFirstToken, records[0].Token)
	}

	// 2. Disconnect/Error Before Replacement (Destination untouched, staging cleaned up)
	targetFile := filepath.Join(testDir, "orig_target.txt")
	_ = os.WriteFile(targetFile, []byte("original pristine content"), 0644)

	// Use an io.Reader that fails immediately
	errReader := &failingReader{err: io.ErrUnexpectedEOF}

	_, writeErr := fileService.Write(ctx, targetFile, errReader, 100, nil, true)
	if writeErr == nil {
		t.Fatalf("expected error from failed body transfer")
	}
	if errors.Is(writeErr, host.ErrOutcomeUnknown) {
		t.Fatalf("transfer failure before replacement must NOT be ErrOutcomeUnknown, got: %v", writeErr)
	}

	// Verify destination remains unchanged
	targetContent, err := os.ReadFile(targetFile)
	if err != nil || string(targetContent) != "original pristine content" {
		t.Fatalf("destination content must remain unchanged after interrupted upload: got %q", string(targetContent))
	}

	// Verify staging file was cleaned up
	staged, _ := filepath.Glob(filepath.Join(testDir, ".vtc-write-*"))
	if len(staged) != 0 {
		t.Fatalf("staging files leaked after transfer failure: %v", staged)
	}

	// 3. Restart Cleanup Recovery: Removes owned staging files matching registered token
	tokenValid := "0123456789abcdef"
	stageValidPath := filepath.Join(testDir, fmt.Sprintf(".vtc-write-%s", tokenValid))
	_ = os.WriteFile(stageValidPath, []byte("leftover staging from dead upload"), 0644)

	// Inject pending cleanup record for fileService's host
	reg := fileServiceLeaseRegistry(t, fileService)
	hostID := host.ID("remote-fs")
	cleanRecID := fmt.Sprintf("%s:%s", hostID, tokenValid)
	err = reg.AddPendingCleanup(ctx, host.PendingCleanupRecord{
		ID:          cleanRecID,
		HostID:      hostID,
		StagingPath: stageValidPath,
		Token:       tokenValid,
		CreatedAt:   time.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("AddPendingCleanup failed: %v", err)
	}

	// 4. Refusal to remove unidentifiable staging candidates
	// A. A normal user file
	userFile := filepath.Join(testDir, "my_database.db")
	_ = os.WriteFile(userFile, []byte("crucial database data"), 0644)
	_ = reg.AddPendingCleanup(ctx, host.PendingCleanupRecord{
		ID:          fmt.Sprintf("%s:malicious-1", hostID),
		HostID:      hostID,
		StagingPath: userFile,
		Token:       "0123456789abcdef", // Token doesn't match base name
		CreatedAt:   time.Now().Unix(),
	})

	// B. A file with staging prefix but non-hex/wrong token
	wrongTokenFile := filepath.Join(testDir, ".vtc-write-nothexcharacters")
	_ = os.WriteFile(wrongTokenFile, []byte("arbitrary candidate"), 0644)
	_ = reg.AddPendingCleanup(ctx, host.PendingCleanupRecord{
		ID:          fmt.Sprintf("%s:malicious-2", hostID),
		HostID:      hostID,
		StagingPath: wrongTokenFile,
		Token:       "nothexcharacters",
		CreatedAt:   time.Now().Unix(),
	})

	// C. A directory named like a staging file
	stageDir := filepath.Join(testDir, ".vtc-write-deadbeefcafebabe")
	_ = os.Mkdir(stageDir, 0755)
	_ = reg.AddPendingCleanup(ctx, host.PendingCleanupRecord{
		ID:          fmt.Sprintf("%s:malicious-3", hostID),
		HostID:      hostID,
		StagingPath: stageDir,
		Token:       "deadbeefcafebabe",
		CreatedAt:   time.Now().Unix(),
	})

	// Run pending cleanups!
	cleanedCount, err := fileService.RunPendingCleanups(ctx)
	if err != nil {
		t.Fatalf("RunPendingCleanups failed: %v", err)
	}
	if cleanedCount < 1 {
		t.Fatalf("expected at least 1 valid staging file cleaned, got: %d", cleanedCount)
	}

	// Verify valid staging file WAS removed
	if _, err := os.Stat(stageValidPath); !os.IsNotExist(err) {
		t.Fatalf("valid staging file should be removed by recovery cleanup, err: %v", err)
	}

	// Verify unidentifiable / non-regular candidates were strictly PRESERVED
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("userFile must NOT be deleted by cleanup: %v", err)
	}
	if _, err := os.Stat(wrongTokenFile); err != nil {
		t.Fatalf("wrongTokenFile must NOT be deleted by cleanup: %v", err)
	}
	if _, err := os.Stat(stageDir); err != nil {
		t.Fatalf("stageDir directory must NOT be deleted by cleanup: %v", err)
	}

	// Verify candidate validation helper
	if host.IsIdentifiableStagingCandidate("/a/b/.vtc-write-0123456789abcdef", "0123456789abcdef") != true {
		t.Errorf("expected true for valid candidate")
	}
	if host.IsIdentifiableStagingCandidate("/a/b/.vtc-write-0123456789abcdef", "differenttoken12") != false {
		t.Errorf("expected false for token mismatch")
	}
	if host.IsIdentifiableStagingCandidate("/a/b/myfile.txt", "myfile.txt") != false {
		t.Errorf("expected false for non-staging file")
	}
}

type failingReader struct {
	err error
}

func (fr *failingReader) Read(p []byte) (int, error) {
	return 0, fr.err
}

func fileServiceLeaseRegistry(t *testing.T, fs *host.RemoteFileService) *host.Registry {
	t.Helper()
	regDir := t.TempDir()
	r, err := host.NewRegistry(context.Background(), regDir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	fs.SetRegistryForTesting(r)
	return r
}

func TestRemoteFilesOutcomeUnknownOnReplacementDisconnect(t *testing.T) {
	fileService, testDir := setupTestRemoteFiles(t)
	ctx := context.Background()

	targetFile := filepath.Join(testDir, "uncertain_outcome.txt")
	_ = os.WriteFile(targetFile, []byte("precious base file"), 0644)

	var capturedSSH *ssh.Client
	fileService.SetAdapterFactoryForTesting(func(ctx context.Context, sshClient *ssh.Client) (*host.SFTPAdapter, error) {
		capturedSSH = sshClient
		return host.NewSFTPAdapter(ctx, sshClient)
	})
	defer fileService.SetAdapterFactoryForTesting(nil)

	fileService.SetBeforePosixRenameHookForTesting(func() {
		if capturedSSH != nil {
			_ = capturedSSH.Close()
		}
	})
	defer fileService.SetBeforePosixRenameHookForTesting(nil)

	bodyStr := "incoming replacement content"
	_, err := fileService.Write(ctx, targetFile, strings.NewReader(bodyStr), int64(len(bodyStr)), nil, true)
	if err == nil {
		t.Fatalf("expected error due to connection loss during commit")
	}

	if !errors.Is(err, host.ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown on disconnect during atomic replacement, got: %v", err)
	}
}
