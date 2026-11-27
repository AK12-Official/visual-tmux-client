package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
)

// startWithLocalTmux runs the application against a disposable local tmux
// server, so that local sessions can be created through the authenticated API.
// It returns a client on the same socket and a way to call the API.
func startWithLocalTmux(
	t *testing.T,
) (*tmux.Client, *Controller, func(method, route, body string) (int, string)) {
	t.Helper()
	path, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TMUX_TMPDIR", testutil.SocketDir(t))
	local := tmux.NewClient(path, "d")
	bootstrapCtx, bootstrapCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer bootstrapCancel()
	_, _, code, err := local.Exec(bootstrapCtx, "-f", "/dev/null", "new-session", "-d", "-s", "fixture")
	if err != nil || code != 0 {
		t.Fatalf("isolated local fixture: code=%d err=%v", code, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, _, _, err := local.Exec(ctx, "kill-server"); err != nil {
			t.Logf("isolated server cleanup: %v", err)
		}
	})
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = true
	cfg.Tmux.Path = path
	cfg.Hosts.StateDir = t.TempDir()
	cfg.Hosts.MasterKeyFile = filepath.Join(cfg.Hosts.StateDir, "master.key")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	controller, err := Start(ctx, &cfg, config.Provenance{}, web.FS(), app.Options{Socket: "d"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := controller.Shutdown(); err != nil {
			t.Error(err)
		}
	})
	return local, controller, func(method, route, body string) (int, string) {
		status, response := desktopHTTP(t, controller, method, route, body, controller.Bridge.token)
		return status, response
	}
}

// The whole path from the authenticated API to a remote host, through a
// configured proxy: enrollment, attachment, files and reconnection all use it,
// and nothing falls back to a direct connection once it is unreachable.
func TestRemoteHostsUseTheConfiguredProxyEndToEnd(t *testing.T) {
	_, controller, call := startWithLocalTmux(t)
	proxy := testutil.NewFakeSOCKS5Proxy(t)
	remote := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User: "proxy-test", Password: "fixture-only", TmuxSocketName: "p",
	})
	expect := func(step string, want int, status int, body string) string {
		t.Helper()
		if status != want {
			t.Fatalf("%s: %d %s", step, status, body)
		}
		return body
	}
	// Every request the proxy receives must name the disposable SSH server.
	assertProxyTargets := func(step string, atLeast int) int {
		t.Helper()
		requests := proxy.Requests()
		if len(requests) < atLeast {
			t.Fatalf("%s: %d proxy requests, want at least %d", step, len(requests), atLeast)
		}
		for _, request := range requests {
			if request.Host != remote.Addr || int(request.Port) != remote.Port {
				t.Fatalf("%s: proxy asked for %s:%d, want the SSH fixture only", step, request.Host, request.Port)
			}
		}
		return len(requests)
	}

	// 1. The setting is stored and returned by the API.
	status, body := call(http.MethodGet, "/api/host-settings", "")
	var settings struct {
		Revision int `json:"revision"`
	}
	expect("read settings", http.StatusOK, status, body)
	if err := json.Unmarshal([]byte(body), &settings); err != nil || settings.Revision == 0 {
		t.Fatalf("settings revision: %v %s", err, body)
	}
	status, body = call(http.MethodPut, "/api/host-settings",
		`{"proxyAddress":`+jsonString(proxy.Addr)+`,"revision":`+itoa(settings.Revision)+`}`)
	expect("save proxy", http.StatusOK, status, body)
	if !strings.Contains(body, `"proxyAddress":`+jsonString(proxy.Addr)) {
		t.Fatalf("saved proxy address: %s", body)
	}

	// 2. Probe and enrollment go through the proxy.
	status, body = call(http.MethodPost, "/api/hosts/probe",
		`{"address":`+jsonString(remote.Addr)+`,"port":`+itoa(remote.Port)+`}`)
	expect("probe", http.StatusOK, status, body)
	var probe struct {
		Fingerprint    string `json:"fingerprint"`
		ChallengeToken string `json:"challengeToken"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil ||
		probe.Fingerprint == "" || probe.ChallengeToken == "" {
		t.Fatalf("probe result: %v %s", err, body)
	}
	assertProxyTargets("probe", 1)
	status, body = call(http.MethodPost, "/api/hosts", `{"id":"remote","name":"Disposable SSH","address":`+
		jsonString(remote.Addr)+`,"port":`+itoa(remote.Port)+`,"username":`+jsonString(remote.User)+
		`,"credentialType":"password","credentials":{"password":`+jsonString(remote.Password)+
		`},"challengeToken":`+jsonString(probe.ChallengeToken)+`}`)
	expect("enroll", http.StatusCreated, status, body)

	// 3. Sessions, attachment, files and reconnection.
	status, body = call(http.MethodPost, "/api/hosts/remote/sessions", `{"name":"proxy-e2e"}`)
	expect("create session", http.StatusCreated, status, body)
	seen := assertProxyTargets("session", 2)
	first := attachDesktopSession(t, controller, "remote", "proxy-e2e")
	seen = assertProxyTargets("attach", seen+1)
	status, body = call(http.MethodGet, "/api/hosts/remote/files/list?path="+url.PathEscape("/"), "")
	expect("list files", http.StatusOK, status, body)
	seen = assertProxyTargets("files", seen+1)
	if err := first.CloseNow(); err != nil {
		t.Fatal(err)
	}
	second := attachDesktopSession(t, controller, "remote", "proxy-e2e")
	defer func() { _ = second.CloseNow() }() //nolint:errcheck // close only this test attachment
	assertProxyTargets("reconnect", seen+1)

	// 4. Without the proxy, the remote host is reported as unreachable
	// through it, and the local host keeps working.
	proxy.Close()
	status, body = call(http.MethodGet, "/api/hosts/remote/sessions", "")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "proxy_unreachable: proxy "+proxy.Addr) {
		t.Fatalf("remote sessions without the proxy: %d %s", status, body)
	}
	status, body = call(http.MethodGet, "/api/hosts/local/sessions", "")
	expect("local sessions without the proxy", http.StatusOK, status, body)
}

// A proxy saved through the API reaches the local sessions created afterwards,
// as environment variables for the commands run in them, and nothing else.
func TestNewLocalSessionsCarryTheProxyEndToEnd(t *testing.T) {
	local, _, call := startWithLocalTmux(t)
	ctx := context.Background()

	sessionEnvironment := func(name string) string {
		t.Helper()
		out, stderr, code, err := local.Exec(ctx, "show-environment", "-t", name)
		if err != nil || code != 0 {
			t.Fatalf("environment of %s: code=%d %v %s", name, code, err, stderr)
		}
		return out
	}
	createLocal := func(name string) {
		t.Helper()
		status, body := call(http.MethodPost, "/api/hosts/local/sessions", `{"name":`+jsonString(name)+`}`)
		if status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, status, body)
		}
	}
	saveProxy := func(address string) {
		t.Helper()
		status, body := call(http.MethodGet, "/api/host-settings", "")
		var settings struct {
			Revision int `json:"revision"`
		}
		if err := json.Unmarshal([]byte(body), &settings); err != nil || settings.Revision == 0 {
			t.Fatalf("settings: %d %v %s", status, err, body)
		}
		status, body = call(http.MethodPut, "/api/host-settings",
			`{"proxyAddress":`+jsonString(address)+`,"revision":`+itoa(settings.Revision)+`}`)
		if status != http.StatusOK {
			t.Fatalf("save proxy %q: %d %s", address, status, body)
		}
	}
	// The application's scheme tells its variables apart from any that the
	// machine or the user's shell configuration put into the tmux environment.
	const injected = "socks5h://127.0.0.1:7890"
	holds := func(environment, line string) bool {
		return strings.Contains("\n"+environment, "\n"+line+"\n")
	}

	// 1. Without a proxy the application adds nothing.
	createLocal("before")
	if holds(sessionEnvironment("before"), "https_proxy="+injected) {
		t.Fatal("a session created without a proxy carries one")
	}

	// 2. A proxy saved through the API reaches the next session, in every
	// variable terminal programs read.
	saveProxy("127.0.0.1:7890")
	createLocal("with-proxy")
	withProxy := sessionEnvironment("with-proxy")
	for _, name := range []string{"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "all_proxy", "ALL_PROXY"} {
		if !holds(withProxy, name+"="+injected) {
			t.Fatalf("the session with the proxy lacks %s:\n%s", name, withProxy)
		}
	}
	for _, name := range []string{"no_proxy", "NO_PROXY"} {
		if !holds(withProxy, name+"=localhost,127.0.0.1,::1,.local") {
			t.Fatalf("the session with the proxy lacks %s:\n%s", name, withProxy)
		}
	}

	// 3. Clearing the proxy leaves existing sessions as they were created.
	saveProxy("")
	createLocal("after")
	if holds(sessionEnvironment("after"), "https_proxy="+injected) {
		t.Fatal("a session created after the proxy was cleared carries one")
	}
	if sessionEnvironment("with-proxy") != withProxy {
		t.Fatal("the session created with the proxy changed after it was cleared")
	}
	if holds(sessionEnvironment("before"), "https_proxy="+injected) {
		t.Fatal("the session created without a proxy changed")
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value) //nolint:errcheck // a string always marshals
	return string(encoded)
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
