package host_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type virtualOversizedLister struct {
	totalCount int64
	itemsRead  atomic.Int64
}

type virtualFileInfo struct {
	name string
}

func (v *virtualFileInfo) Name() string       { return v.name }
func (v *virtualFileInfo) Size() int64        { return 1024 }
func (v *virtualFileInfo) Mode() os.FileMode  { return 0644 }
func (v *virtualFileInfo) ModTime() time.Time { return time.Unix(100000, 0) }
func (v *virtualFileInfo) IsDir() bool        { return false }
func (v *virtualFileInfo) Sys() any           { return nil }

type virtualListerAt struct {
	parent *virtualOversizedLister
}

func (v *virtualListerAt) ListAt(slice []os.FileInfo, offset int64) (int, error) {
	if offset >= v.parent.totalCount {
		return 0, io.EOF
	}
	n := 0
	for i := range slice {
		cur := offset + int64(i)
		if cur >= v.parent.totalCount {
			return n, io.EOF
		}
		slice[i] = &virtualFileInfo{name: fmt.Sprintf("file_%08d.txt", cur)}
		n++
		v.parent.itemsRead.Add(1)
	}
	return n, nil
}

type virtualFileHandler struct {
	lister *virtualOversizedLister
	sftp.Handlers
}

func (h *virtualFileHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	return &virtualListerAt{parent: h.lister}, nil
}

func startOversizedSFTPServer(t *testing.T, lister *virtualOversizedLister) (net.Listener, ssh.PublicKey) {
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

	handler := sftp.InMemHandler()
	handler.FileList = &virtualFileHandler{lister: lister}

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
								server := sftp.NewRequestServer(channel, handler)
								_ = server.Serve()
								_ = channel.Close()
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

func dialTestSSHClient(t *testing.T, addr string) *ssh.Client {
	t.Helper()
	cfg := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatalf("dial test ssh: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestSFTPAdapterBoundedDirectoryIterationDoesNotScaleWithOversizedDir(t *testing.T) {
	const oversizedCount = 50000
	lister := &virtualOversizedLister{totalCount: oversizedCount}
	listener, _ := startOversizedSFTPServer(t, lister)

	sshClient := dialTestSSHClient(t, listener.Addr().String())

	adapter, err := host.NewSFTPAdapter(context.Background(), sshClient)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const maxEntries = 15
	entries, truncated, err := adapter.ReadDirBounded(ctx, "/oversized", maxEntries)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != maxEntries {
		t.Fatalf("expected exactly %d entries, got %d", maxEntries, len(entries))
	}
	if !truncated {
		t.Fatal("expected truncated=true for 50,000 files directory with limit 15")
	}

	// CRITICAL VERIFICATION:
	// The directory contained 50,000 files.
	// Bounded iteration must only read the first batch of entries needed to satisfy maxEntries
	// and close the handle immediately without scaling with the 50,000 files.
	itemsRead := lister.itemsRead.Load()
	if itemsRead >= oversizedCount {
		t.Fatalf("bounded iteration scaled with entire directory! Read %d of %d items", itemsRead, oversizedCount)
	}
	if itemsRead > 500 {
		t.Fatalf("excessive items read for limit %d: %d items read", maxEntries, itemsRead)
	}
	t.Logf("Successfully read %d entries (truncated=%v). Only %d / %d items were touched on server.",
		len(entries), truncated, itemsRead, oversizedCount)
}

func TestSFTPAdapterExtensionNegotiation(t *testing.T) {
	lister := &virtualOversizedLister{totalCount: 10}
	listener, _ := startOversizedSFTPServer(t, lister)

	sshClient := dialTestSSHClient(t, listener.Addr().String())

	adapter, err := host.NewSFTPAdapter(context.Background(), sshClient)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	// Verify extension checking works without panic
	_ = adapter.HasExtension(host.PosixRenameExtension)
	_ = adapter.HasExtension(host.FsyncExtension)
	_ = adapter.HasExtension("nonexistent@extension.com")
}

func TestSFTPAdapterEffectiveCancellation(t *testing.T) {
	lister := &virtualOversizedLister{totalCount: 1000}
	listener, _ := startOversizedSFTPServer(t, lister)

	sshClient := dialTestSSHClient(t, listener.Addr().String())

	adapter, err := host.NewSFTPAdapter(context.Background(), sshClient)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()

	// Pre-cancelled context must fail immediately
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err = adapter.ReadDirBounded(cancelledCtx, "/dir", 10)
	if err == nil {
		t.Fatal("expected error with pre-cancelled context, got nil")
	}
}
