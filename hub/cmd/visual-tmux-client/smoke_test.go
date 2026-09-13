package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const (
	smokeWaitTimeout  = 5 * time.Second
	smokePollInterval = 50 * time.Millisecond
)

func getFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen for free port: %v", err)
	}
	defer func() {
		_ = ln.Close() //nolint:errcheck // closing listener in test helper
	}()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("expected *net.TCPAddr, got %T", ln.Addr())
	}
	return tcpAddr.Port
}

type runningServer struct {
	cmd    *exec.Cmd
	addr   string
	token  string
	stderr *bufio.Reader
}

func startSmokeServer(
	t *testing.T,
	binPath string,
	args []string,
	env []string,
	dir string,
) *runningServer {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("failed to open stderr pipe: %v", err)
	}
	cmd.Stdout = os.Stdout

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start binary: %v", err)
	}

	reader := bufio.NewReader(stderrPipe)
	server := &runningServer{
		cmd:    cmd,
		stderr: reader,
	}

	// Read lines until URL and token are found or timeout
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			line, rErr := reader.ReadString('\n')
			if rErr != nil {
				return
			}
			if strings.Contains(line, "open http://") {
				parts := strings.Split(line, "open http://")
				if len(parts) > 1 {
					server.addr = strings.TrimSpace(parts[1])
				}
			}
			if strings.Contains(line, "token:") {
				parts := strings.Split(line, "token:")
				if len(parts) > 1 {
					sub := strings.Split(parts[1], "(")
					server.token = strings.TrimSpace(sub[0])
				}
			}
			if server.addr != "" && server.token != "" {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(smokeWaitTimeout):
		_ = cmd.Process.Kill() //nolint:errcheck // kill on test timeout
		t.Fatalf("timed out waiting for server startup")
	}

	// Poll until server is accepting connections
	deadline := time.Now().Add(smokeWaitTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", server.addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close() //nolint:errcheck // close polling probe socket
			return server
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start accepting connections on %s", server.addr)

	return server
}

func stopSmokeServer(t *testing.T, s *runningServer) {
	t.Helper()
	if s.cmd.Process == nil {
		return
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Logf("signal error: %v", err)
	}
	_ = s.cmd.Wait() //nolint:errcheck // wait completion in test helper
}

func ensureBinaryBuilt(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join("..", "..", "visual-tmux-client")
	if _, err := os.Stat(binPath); err != nil {
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		if out, bErr := cmd.CombinedOutput(); bErr != nil {
			t.Fatalf("failed to build binary: %v: %s", bErr, string(out))
		}
	}
	abs, err := filepath.Abs(binPath)
	if err != nil {
		t.Fatalf("failed to resolve abs path: %v", err)
	}
	return abs
}

func TestSmokeNoYAML(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	srv := startSmokeServer(t, bin, []string{"-addr", addr}, nil, dir)
	defer stopSmokeServer(t, srv)

	// Verify GET /api/client-config
	res, err := http.Get("http://" + srv.addr + "/api/client-config")
	if err != nil {
		t.Fatalf("failed to fetch client config: %v", err)
	}
	defer func() {
		_ = res.Body.Close() //nolint:errcheck // cleanup in test
	}()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var conf map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&conf); err != nil {
		t.Fatalf("failed to decode client config: %v", err)
	}
	ver, ok := conf["version"].(float64)
	if !ok || ver != 1 {
		t.Errorf("expected version 1, got %v", conf["version"])
	}

	// Verify GET / serves embedded HTML
	idxRes, err := http.Get("http://" + srv.addr + "/")
	if err != nil {
		t.Fatalf("failed to fetch index: %v", err)
	}
	defer func() {
		_ = idxRes.Body.Close() //nolint:errcheck // cleanup in test
	}()
	idxBytes, err := io.ReadAll(idxRes.Body)
	if err != nil {
		t.Fatalf("failed to read index: %v", err)
	}
	if !strings.Contains(string(idxBytes), "Visual Tmux Client") {
		t.Errorf("expected title in index HTML, got: %s", string(idxBytes))
	}
}

func TestSmokePartialYAML(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	port := getFreePort(t)
	yamlContent := fmt.Sprintf(`version: 1
server:
  addr: "127.0.0.1:%d"
web:
  session_poll_interval: "2500ms"
`, port)
	cfgFile := filepath.Join(dir, "partial.yaml")
	if err := os.WriteFile(cfgFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := startSmokeServer(t, bin, []string{"--config", cfgFile}, nil, dir)
	defer stopSmokeServer(t, srv)

	res, err := http.Get("http://" + srv.addr + "/api/client-config")
	if err != nil {
		t.Fatalf("failed to fetch client-config: %v", err)
	}
	defer func() {
		_ = res.Body.Close() //nolint:errcheck // cleanup in test
	}()

	var conf struct {
		Version int `json:"version"`
		Web     struct {
			SessionPollInterval int64 `json:"session_poll_interval"`
			ActivityDecay       int64 `json:"activity_decay"`
		} `json:"web"`
	}
	if err := json.NewDecoder(res.Body).Decode(&conf); err != nil {
		t.Fatal(err)
	}
	if conf.Web.SessionPollInterval != 2500 {
		t.Errorf("expected 2500 ms, got %d", conf.Web.SessionPollInterval)
	}
	if conf.Web.ActivityDecay != 2000 {
		t.Errorf("expected default 2000 ms fallback, got %d", conf.Web.ActivityDecay)
	}
}

func TestSmokeEnvAndCLIPrecedence(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	yamlPort := getFreePort(t)
	envPort := getFreePort(t)
	cliPort := getFreePort(t)

	yamlContent := fmt.Sprintf(`version: 1
server:
  addr: "127.0.0.1:%d"
`, yamlPort)
	cfgFile := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(cfgFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	env := []string{fmt.Sprintf("VISUAL_TMUX_CLIENT_ADDR=127.0.0.1:%d", envPort)}
	args := []string{"--config", cfgFile, "-addr", fmt.Sprintf("127.0.0.1:%d", cliPort)}

	srv := startSmokeServer(t, bin, args, env, dir)
	defer stopSmokeServer(t, srv)

	// 1. CLI flag wins over Env and YAML
	expectedAddr := fmt.Sprintf("127.0.0.1:%d", cliPort)
	if srv.addr != expectedAddr {
		t.Errorf("expected CLI to win (%s), got %s", expectedAddr, srv.addr)
	}

	// 2. Env variable wins over YAML when CLI flag is omitted
	argsNoCLI := []string{"--config", cfgFile}
	srvEnv := startSmokeServer(t, bin, argsNoCLI, env, dir)
	defer stopSmokeServer(t, srvEnv)

	expectedEnvAddr := fmt.Sprintf("127.0.0.1:%d", envPort)
	if srvEnv.addr != expectedEnvAddr {
		t.Errorf("expected Env to win over YAML (%s), got %s", expectedEnvAddr, srvEnv.addr)
	}
}

func TestSmokeEnvConfigFile(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	port := getFreePort(t)

	yamlContent := fmt.Sprintf(`version: 1
server:
  addr: "127.0.0.1:%d"
web:
  activity_decay: "4500ms"
`, port)
	cfgFile := filepath.Join(dir, "env-specified.yaml")
	if err := os.WriteFile(cfgFile, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	env := []string{"VISUAL_TMUX_CLIENT_CONFIG=" + cfgFile}
	srv := startSmokeServer(t, bin, nil, env, dir)
	defer stopSmokeServer(t, srv)

	res, err := http.Get("http://" + srv.addr + "/api/client-config")
	if err != nil {
		t.Fatalf("failed to fetch client-config: %v", err)
	}
	defer func() {
		_ = res.Body.Close() //nolint:errcheck // cleanup in test
	}()

	var conf struct {
		Web struct {
			ActivityDecay int64 `json:"activity_decay"`
		} `json:"web"`
	}
	if err := json.NewDecoder(res.Body).Decode(&conf); err != nil {
		t.Fatal(err)
	}
	if conf.Web.ActivityDecay != 4500 {
		t.Errorf("expected 4500 ms from env-specified config, got %d", conf.Web.ActivityDecay)
	}
}

func TestSmokeIllegalYAML(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(cfgFile, []byte("unknown_root_key: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--config", cfgFile)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit code on bad YAML, output: %s", string(out))
	}
	if !strings.Contains(string(out), "unknown configuration key") {
		t.Errorf("expected unknown configuration key error message, got: %s", string(out))
	}
}

func TestSmokeYAMLModificationAndClientRefresh(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	port := getFreePort(t)
	cfgFile := filepath.Join(dir, "config.yaml")

	writeCfg := func(dur string) {
		content := fmt.Sprintf(`version: 1
server:
  addr: "127.0.0.1:%d"
web:
  activity_decay: "%s"
`, port, dur)
		if err := os.WriteFile(cfgFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeCfg("3s")
	srv1 := startSmokeServer(t, bin, []string{"--config", cfgFile}, nil, dir)
	res1, err := http.Get("http://" + srv1.addr + "/api/client-config")
	if err != nil {
		t.Fatal(err)
	}
	var conf1 struct {
		Web struct {
			ActivityDecay int64 `json:"activity_decay"`
		} `json:"web"`
	}
	_ = json.NewDecoder(res1.Body).Decode(&conf1) //nolint:errcheck // test helper
	_ = res1.Body.Close()                         //nolint:errcheck // test helper
	stopSmokeServer(t, srv1)

	if conf1.Web.ActivityDecay != 3000 {
		t.Fatalf("expected 3000 ms, got %d", conf1.Web.ActivityDecay)
	}

	// Modify YAML to 4s and restart
	writeCfg("4s")
	srv2 := startSmokeServer(t, bin, []string{"--config", cfgFile}, nil, dir)
	defer stopSmokeServer(t, srv2)

	res2, err := http.Get("http://" + srv2.addr + "/api/client-config")
	if err != nil {
		t.Fatal(err)
	}
	var conf2 struct {
		Web struct {
			ActivityDecay int64 `json:"activity_decay"`
		} `json:"web"`
	}
	_ = json.NewDecoder(res2.Body).Decode(&conf2) //nolint:errcheck // test helper
	_ = res2.Body.Close()                         //nolint:errcheck // test helper

	if conf2.Web.ActivityDecay != 4000 {
		t.Fatalf("expected 4000 ms after restart, got %d", conf2.Web.ActivityDecay)
	}
}

func TestSmokeTerminalInputOutputAndReconnect(t *testing.T) {
	bin := ensureBinaryBuilt(t)
	dir := t.TempDir()
	port := getFreePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	tmuxDir := t.TempDir()
	env := []string{"TMUX_TMPDIR=" + tmuxDir}
	t.Cleanup(func() {
		tmux, err := exec.LookPath("tmux")
		if err == nil {
			cmd := exec.Command(tmux, "kill-server")
			cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+tmuxDir)
			_ = cmd.Run() //nolint:errcheck // cleanup isolated test tmux server
		}
	})
	srv := startSmokeServer(t, bin, []string{"-addr", addr}, env, dir)
	defer stopSmokeServer(t, srv)

	// Create session via REST API
	req, _ := http.NewRequest( //nolint:errcheck // standard test request
		http.MethodPost,
		"http://"+srv.addr+"/api/hosts/local/sessions",
		strings.NewReader(`{"name":"smoke-sess"}`),
	)
	req.Header.Set("Authorization", "Bearer "+srv.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if res.StatusCode != http.StatusCreated {
		body, bErr := io.ReadAll(res.Body)
		if bErr != nil {
			t.Fatalf("expected 201 on create session, got %d", res.StatusCode)
		}
		t.Fatalf("expected 201 on create session, got %d: %s", res.StatusCode, string(body))
	}
	_ = res.Body.Close() //nolint:errcheck // cleanup

	// Issue first ticket
	ticketReq, _ := http.NewRequest( //nolint:errcheck // standard test request
		http.MethodPost,
		"http://"+srv.addr+"/api/ws-ticket",
		strings.NewReader(`{"hostId":"local","session":"smoke-sess"}`),
	)
	ticketReq.Header.Set("Authorization", "Bearer "+srv.token)
	ticketReq.Header.Set("Content-Type", "application/json")
	tRes, err := http.DefaultClient.Do(ticketReq)
	if err != nil {
		t.Fatalf("failed to issue ticket: %v", err)
	}
	if tRes.StatusCode != http.StatusOK {
		tBody, bErr := io.ReadAll(tRes.Body)
		if bErr != nil {
			t.Fatalf("expected 200 on ticket issue, got %d", tRes.StatusCode)
		}
		t.Fatalf("expected 200 on ticket issue, got %d: %s", tRes.StatusCode, string(tBody))
	}
	var ticketPayload struct {
		Ticket string `json:"ticket"`
	}
	_ = json.NewDecoder(tRes.Body).Decode(&ticketPayload) //nolint:errcheck // helper
	_ = tRes.Body.Close()                                 //nolint:errcheck // helper

	// Connect WebSocket
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := fmt.Sprintf("ws://%s/ws/local/smoke-sess?ticket=%s&cols=80&rows=24", srv.addr, ticketPayload.Ticket)
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}

	// Send input
	if err := wsConn.Write(ctx, websocket.MessageBinary, []byte("echo SMOKE_RUNNING\n")); err != nil {
		t.Fatalf("ws write error: %v", err)
	}

	// Read until output contains SMOKE_RUNNING
	found := false
	for i := 0; i < 15; i++ {
		_, data, rErr := wsConn.Read(ctx)
		if rErr != nil {
			break
		}
		if strings.Contains(string(data), "SMOKE_RUNNING") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("did not find SMOKE_RUNNING in websocket output")
	}

	// Disconnect WebSocket
	_ = wsConn.Close(websocket.StatusNormalClosure, "disconnecting") //nolint:errcheck // disconnect

	// Issue second ticket to reconnect
	ticketReq2, _ := http.NewRequest( //nolint:errcheck // standard test request
		http.MethodPost,
		"http://"+srv.addr+"/api/ws-ticket",
		strings.NewReader(`{"hostId":"local","session":"smoke-sess"}`),
	)
	ticketReq2.Header.Set("Authorization", "Bearer "+srv.token)
	ticketReq2.Header.Set("Content-Type", "application/json")
	tRes2, err := http.DefaultClient.Do(ticketReq2)
	if err != nil {
		t.Fatalf("failed to issue second ticket: %v", err)
	}
	if tRes2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on second ticket issue, got %d", tRes2.StatusCode)
	}
	var ticketPayload2 struct {
		Ticket string `json:"ticket"`
	}
	_ = json.NewDecoder(tRes2.Body).Decode(&ticketPayload2) //nolint:errcheck // helper
	_ = tRes2.Body.Close()                                  //nolint:errcheck // helper

	// Reconnect WebSocket
	wsURL2 := fmt.Sprintf("ws://%s/ws/local/smoke-sess?ticket=%s&cols=80&rows=24", srv.addr, ticketPayload2.Ticket)
	wsConn2, _, err := websocket.Dial(ctx, wsURL2, nil)
	if err != nil {
		t.Fatalf("websocket reconnect dial failed: %v", err)
	}
	defer func() {
		_ = wsConn2.Close(websocket.StatusNormalClosure, "done") //nolint:errcheck // cleanup
	}()

	// Verify session is still running and responsive
	if err := wsConn2.Write(ctx, websocket.MessageBinary, []byte("echo RECONNECT_OK\n")); err != nil {
		t.Fatalf("ws write on reconnect failed: %v", err)
	}
	reconnectFound := false
	for i := 0; i < 15; i++ {
		_, data, rErr := wsConn2.Read(ctx)
		if rErr != nil {
			break
		}
		if strings.Contains(string(data), "RECONNECT_OK") {
			reconnectFound = true
			break
		}
	}
	if !reconnectFound {
		t.Errorf("did not find RECONNECT_OK after reconnect")
	}
}
