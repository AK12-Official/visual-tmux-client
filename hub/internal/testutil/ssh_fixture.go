package testutil

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/creack/pty"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Known valid encrypted private key and passphrase for disposable tests.
const (
	TestEncryptedPrivateKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAACmFlczI1Ni1jdHIAAAAGYmNyeXB0AAAAGAAAABB0snt0EP
qUIzFiI1+PtoiJAAAAGAAAAAEAAAAzAAAAC3NzaC1lZDI1NTE5AAAAICNxh6U7K395126N
I88EyFnv5GdvT+io6O4eDIijASeEAAAAsN6eQ1I04fkE+vVeCkWRjrGawubr7M2mgcSS11
8BJuOAd4Di3iNIDD9ZP7APGqoAwVkgKwS1zUTJA4fz1k556vH04JuE4+qLu0OfCgUrH0v7
DWKhR9jS1C59JLKFSk3+aF4o2HIsr48Lz5kGUkTfc8xj79PsDRkkbyqhxojhDerdUxpYtK
RECXJ0e+PkPnYftJYlni7+jWTtpvGWxCCXfsh/EY5+VRR/e1B6xnd5Mlpx
-----END OPENSSH PRIVATE KEY-----`
	TestEncryptedKeyPassphrase = "testpassphrase"

	dirPermPrivate         = 0o700
	filePermExec           = 0o755
	extraEnvCapacity       = 4
	ptyReqMinPayload       = 8
	windowChangeMinPayload = 8
	subsystemHeaderLen     = 4
	execHeaderLen          = 4
)

// DisposableSSHConfig parameterizes a disposable SSH server fixture.
type DisposableSSHConfig struct {
	User               string
	Password           string
	UsePrivateKey      bool
	UseEncryptedKey    bool
	KeyPassphrase      string
	DisablePosixRename bool
	TmuxSocketName     string
}

// DisposableSSHServer is a self-contained in-process SSH/SFTP server backed by an isolated tmux server.
type DisposableSSHServer struct {
	t           *testing.T
	Listener    net.Listener
	Addr        string
	Port        int
	HostSigner  ssh.Signer
	HostPubKey  ssh.PublicKey
	Fingerprint string
	KeyType     string
	User        string
	Password    string
	UserKeyPEM  string
	UserSigner  ssh.Signer
	Passphrase  string
	RootDir     string
	SocketDir   string
	TmuxEnv     []string
	TmuxSocket  string

	mu     sync.Mutex
	conns  []net.Conn
	closed bool
}

func generateHostSigner(t *testing.T) (ssh.Signer, ssh.PublicKey, string, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer from host key: %v", err)
	}
	hostPubKey := hostSigner.PublicKey()
	return hostSigner, hostPubKey, ssh.FingerprintSHA256(hostPubKey), hostPubKey.Type()
}

func resolveUserCredentials(
	t *testing.T, cfg DisposableSSHConfig,
) (user, password, userKeyPEM string, userSigner ssh.Signer, passphrase string) {
	t.Helper()
	user = cfg.User
	if user == "" {
		user = "testuser"
	}
	password = cfg.Password
	if password == "" && !cfg.UsePrivateKey && !cfg.UseEncryptedKey {
		password = "testpassword"
	}

	if cfg.UseEncryptedKey {
		userKeyPEM = TestEncryptedPrivateKey
		passphrase = cfg.KeyPassphrase
		if passphrase == "" {
			passphrase = TestEncryptedKeyPassphrase
		}
		s, err := ssh.ParsePrivateKeyWithPassphrase([]byte(userKeyPEM), []byte(passphrase))
		if err != nil {
			t.Fatalf("parse encrypted key: %v", err)
		}
		userSigner = s
	} else if cfg.UsePrivateKey {
		_, userPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate user key: %v", err)
		}
		pkcs8, err := x509.MarshalPKCS8PrivateKey(userPriv)
		if err != nil {
			t.Fatalf("marshal pkcs8: %v", err)
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
		userKeyPEM = string(pemBytes)
		s, err := ssh.NewSignerFromKey(userPriv)
		if err != nil {
			t.Fatalf("signer from user key: %v", err)
		}
		userSigner = s
	}
	return user, password, userKeyPEM, userSigner, passphrase
}

func setupTmuxEnvironment(t *testing.T, rootDir, socketName, socketDir string) ([]string, string) {
	t.Helper()
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("tmux binary not found: %v", err)
	}

	binDir := filepath.Join(rootDir, "bin")
	if err := os.MkdirAll(binDir, filePermExec); err != nil {
		t.Fatalf("create bin dir: %v", err)
	}
	wrapperPath := filepath.Join(binDir, "tmux")
	wrapperScript := fmt.Sprintf("#!/bin/sh\nexec %q -L %s \"$@\"\n", realTmux, socketName)
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), filePermExec); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}

	tmuxEnv := make([]string, 0, len(os.Environ())+extraEnvCapacity)
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if k != "TMUX" && k != "TMUX_PANE" && k != "TMUX_TMPDIR" && k != "HOME" && k != "PATH" {
			tmuxEnv = append(tmuxEnv, entry)
		}
	}
	tmuxEnv = append(tmuxEnv,
		"TMUX_TMPDIR="+socketDir,
		"HOME="+rootDir,
		"PATH="+binDir+":"+os.Getenv("PATH"),
	)

	startCmd := exec.Command(realTmux, "-L", socketName, "start-server")
	startCmd.Env = tmuxEnv
	_ = startCmd.Run() //nolint:errcheck // best-effort start isolated server

	return tmuxEnv, realTmux
}

func buildServerConfig(server *DisposableSSHServer, hostSigner ssh.Signer) *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			server.mu.Lock()
			closed := server.closed
			server.mu.Unlock()
			if closed {
				return nil, errors.New("server closed")
			}
			if conn.User() == server.User && server.Password != "" && string(pass) == server.Password {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		},
		PublicKeyCallback: func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			server.mu.Lock()
			closed := server.closed
			server.mu.Unlock()
			if closed {
				return nil, errors.New("server closed")
			}
			if conn.User() != server.User {
				return nil, errors.New("unknown user")
			}
			if server.UserSigner != nil && bytes.Equal(key.Marshal(), server.UserSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("unauthorized public key")
		},
	}
	cfg.AddHostKey(hostSigner)
	return cfg
}

// NewDisposableSSHServer starts an isolated SSH/SFTP server with an isolated tmux backend.
func NewDisposableSSHServer(t *testing.T, cfg DisposableSSHConfig) *DisposableSSHServer {
	t.Helper()

	hostSigner, hostPubKey, fingerprint, keyType := generateHostSigner(t)
	user, password, userKeyPEM, userSigner, passphrase := resolveUserCredentials(t, cfg)

	rootDir, err := os.MkdirTemp("", "vtc-sftp")
	if err != nil {
		t.Fatalf("create root dir: %v", err)
	}
	if err := os.Chmod(rootDir, dirPermPrivate); err != nil {
		t.Fatalf("chmod root dir: %v", err)
	}

	socketDir := SocketDir(t)
	socketName := cfg.TmuxSocketName
	if socketName == "" {
		socketName = "vtc-iso"
	}

	tmuxEnv, _ := setupTmuxEnvironment(t, rootDir, socketName, socketDir)

	server := &DisposableSSHServer{
		t:           t,
		HostSigner:  hostSigner,
		HostPubKey:  hostPubKey,
		Fingerprint: fingerprint,
		KeyType:     keyType,
		User:        user,
		Password:    password,
		UserKeyPEM:  userKeyPEM,
		UserSigner:  userSigner,
		Passphrase:  passphrase,
		RootDir:     rootDir,
		SocketDir:   socketDir,
		TmuxEnv:     tmuxEnv,
		TmuxSocket:  socketName,
	}

	serverConfig := buildServerConfig(server, hostSigner)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	server.Listener = listener
	server.Addr = "127.0.0.1"
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		server.Port = tcpAddr.Port
	}

	t.Cleanup(func() {
		server.Close()
	})

	go server.serve(serverConfig)

	return server
}

func (s *DisposableSSHServer) serve(config *ssh.ServerConfig) {
	for {
		tcpConn, err := s.Listener.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = tcpConn.Close() //nolint:errcheck // closing connection on closed server
			return
		}
		s.conns = append(s.conns, tcpConn)
		s.mu.Unlock()

		go func(c net.Conn) {
			sshConn, chans, reqs, err := ssh.NewServerConn(c, config)
			if err != nil {
				_ = c.Close() //nolint:errcheck // close failed handshake connection
				return
			}
			go ssh.DiscardRequests(reqs)
			for newChannel := range chans {
				if newChannel.ChannelType() != "session" {
					_ = newChannel.Reject( //nolint:errcheck // best effort reject
						ssh.UnknownChannelType, "unsupported channel type",
					)
					continue
				}
				ch, requests, err := newChannel.Accept()
				if err != nil {
					continue
				}
				go s.handleSession(ch, requests)
			}
			_ = sshConn.Wait() //nolint:errcheck // best effort wait
		}(tcpConn)
	}
}

func handlePTYReq(req *ssh.Request, ptyCols, ptyRows *uint32) {
	if len(req.Payload) >= ptyReqMinPayload {
		termLen := binary.BigEndian.Uint32(req.Payload[:subsystemHeaderLen])
		offset := subsystemHeaderLen + termLen
		if uint32(len(req.Payload)) >= offset+ptyReqMinPayload {
			*ptyCols = binary.BigEndian.Uint32(req.Payload[offset : offset+subsystemHeaderLen])
			*ptyRows = binary.BigEndian.Uint32(req.Payload[offset+subsystemHeaderLen : offset+ptyReqMinPayload])
		}
	}
	_ = req.Reply(true, nil) //nolint:errcheck // best effort reply
}

func handleWindowChange(req *ssh.Request, ptmxLock *sync.Mutex, ptmx *os.File, ptyCols, ptyRows *uint32) {
	if len(req.Payload) >= windowChangeMinPayload {
		cols := binary.BigEndian.Uint32(req.Payload[:subsystemHeaderLen])
		rows := binary.BigEndian.Uint32(req.Payload[subsystemHeaderLen:windowChangeMinPayload])
		ptmxLock.Lock()
		*ptyCols = cols
		*ptyRows = rows
		if ptmx != nil {
			_ = pty.Setsize( //nolint:errcheck // best effort resize
				ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)},
			)
		}
		ptmxLock.Unlock()
	}
	if req.WantReply {
		_ = req.Reply(true, nil) //nolint:errcheck // best effort reply
	}
}

func handleSubsystem(req *ssh.Request, channel ssh.Channel, rootDir string) bool {
	if len(req.Payload) >= subsystemHeaderLen {
		subsys := string(req.Payload[subsystemHeaderLen:])
		if subsys == "sftp" {
			_ = req.Reply(true, nil) //nolint:errcheck // best effort reply
			sftpServer, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(rootDir))
			if err == nil {
				go func() {
					_ = sftpServer.Serve() //nolint:errcheck // sftp serve finished
					_ = channel.Close()    //nolint:errcheck // close sftp channel
				}()
			}
			return true
		}
	}
	_ = req.Reply(false, nil) //nolint:errcheck // reject unknown subsystem
	return false
}

func (s *DisposableSSHServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	var (
		ptyReq   bool
		ptyCols  uint32
		ptyRows  uint32
		ptmx     *os.File
		ptmxLock sync.Mutex
	)

	for req := range requests {
		switch req.Type {
		case "pty-req":
			ptyReq = true
			handlePTYReq(req, &ptyCols, &ptyRows)
		case "window-change":
			handleWindowChange(req, &ptmxLock, ptmx, &ptyCols, &ptyRows)
		case "subsystem":
			if handleSubsystem(req, channel, s.RootDir) {
				return
			}
		case "exec":
			if len(req.Payload) < execHeaderLen {
				_ = req.Reply(false, nil) //nolint:errcheck // best effort reply
				return
			}
			cmdText := string(req.Payload[execHeaderLen:])
			_ = req.Reply(true, nil) //nolint:errcheck // best effort reply
			s.runCommand(channel, cmdText, ptyReq, ptyCols, ptyRows, &ptmx, &ptmxLock)
			return
		case "shell":
			_ = req.Reply(true, nil) //nolint:errcheck // best effort reply
			s.runCommand(channel, "sh", ptyReq, ptyCols, ptyRows, &ptmx, &ptmxLock)
			return
		default:
			if req.WantReply {
				_ = req.Reply(false, nil) //nolint:errcheck // best effort reply
			}
		}
	}
}

func (s *DisposableSSHServer) runCommand(
	channel ssh.Channel,
	cmdText string,
	ptyReq bool,
	ptyCols, ptyRows uint32,
	ptmxRef **os.File,
	ptmxLock *sync.Mutex,
) {
	cmd := exec.Command("sh", "-c", cmdText)
	cmd.Dir = s.RootDir
	cmd.Env = s.TmuxEnv

	if ptyReq {
		ptmx, err := pty.Start(cmd)
		if err != nil {
			closeChannelWithStatus(channel, 1)
			return
		}
		ptmxLock.Lock()
		*ptmxRef = ptmx
		if ptyRows > 0 && ptyCols > 0 {
			_ = pty.Setsize( //nolint:errcheck // best effort resize
				ptmx, &pty.Winsize{Rows: uint16(ptyRows), Cols: uint16(ptyCols)},
			)
		}
		ptmxLock.Unlock()

		go func() {
			_, _ = io.Copy(ptmx, channel) //nolint:errcheck // copy pty
		}()
		go func() {
			_, _ = io.Copy(channel, ptmx) //nolint:errcheck // copy pty
		}()

		err = cmd.Wait()
		exitCode := uint32(0)
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = uint32(exitErr.ExitCode())
		}
		closeChannelWithStatus(channel, exitCode)
		_ = ptmx.Close() //nolint:errcheck // close ptmx
		return
	}

	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()

	err := cmd.Run()
	exitCode := uint32(0)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = uint32(exitErr.ExitCode())
	}
	closeChannelWithStatus(channel, exitCode)
}

func closeChannelWithStatus(channel ssh.Channel, status uint32) {
	statusReq := struct{ Status uint32 }{Status: status}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(statusReq)) //nolint:errcheck // exit status
	_ = channel.Close()                                                      //nolint:errcheck // close channel
}

// DropConnections closes all active incoming connections to simulate network drops.
func (s *DisposableSSHServer) DropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		_ = c.Close() //nolint:errcheck // dropping connection
	}
	s.conns = nil
}

// Close shuts down the SSH server, drops connections, kills the isolated tmux server, and deletes private files.
func (s *DisposableSSHServer) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	_ = s.Listener.Close() //nolint:errcheck // close listener
	for _, c := range s.conns {
		_ = c.Close() //nolint:errcheck // close connection
	}
	s.conns = nil
	s.mu.Unlock()

	// Kill isolated tmux server
	killCmd := exec.Command("tmux", "-L", s.TmuxSocket, "kill-server")
	killCmd.Env = s.TmuxEnv
	_ = killCmd.Run() //nolint:errcheck // best-effort kill throwaway tmux server

	_ = os.RemoveAll(s.RootDir) //nolint:errcheck // cleanup test temp root
}
