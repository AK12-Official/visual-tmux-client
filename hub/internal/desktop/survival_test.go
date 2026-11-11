package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/app"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
	"github.com/AK12-Official/visual-tmux-client/hub/web"
	"github.com/coder/websocket"
)

func TestQuitRelaunchPreservesAttachedLocalAndRemoteTmux(t *testing.T) {
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
	remote := testutil.NewDisposableSSHServer(t, testutil.DisposableSSHConfig{
		User: "desktop-test", Password: "fixture-only", TmuxSocketName: "r",
	})
	cfg := config.DefaultConfig()
	cfg.Files.Enabled = false
	cfg.Tmux.Path = path
	cfg.Hosts.StateDir = t.TempDir()
	cfg.Hosts.MasterKeyFile = filepath.Join(cfg.Hosts.StateDir, "master.key")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	registry, err := host.NewRegistry(ctx, cfg.Hosts.StateDir, cfg.Hosts.MasterKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	_, saveErr := registry.Save(ctx, host.HostInput{
		ID: "remote", Name: "Disposable SSH", Address: remote.Addr, Port: uint16(remote.Port), Username: remote.User,
		CredentialType: "password", Credentials: host.Credentials{Password: remote.Password},
		HostKey: remote.HostPubKey.Marshal(),
	}, 0)
	closeErr := registry.Close()
	if saveErr != nil || closeErr != nil {
		t.Fatalf("fixture enrollment: %v %v", saveErr, closeErr)
	}
	start := func(phase string) *Controller {
		c, err := Start(ctx, &cfg, config.Provenance{}, web.FS(), app.Options{Socket: "d"})
		if err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
		t.Cleanup(func() {
			if err := c.Shutdown(); err != nil {
				t.Error(err)
			}
		})
		return c
	}
	first := start("initial startup")
	var attachments []*websocket.Conn
	for _, id := range []string{"local", "remote"} {
		status, body := desktopHTTP(t, first, http.MethodPost, "/api/hosts/"+id+"/sessions",
			`{"name":"desktop-survival"}`, first.Bridge.token)
		if status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", id, status, body)
		}
		attachments = append(attachments, attachDesktopSession(t, first, id))
	}
	defer func() {
		for _, connection := range attachments {
			_ = connection.CloseNow() //nolint:errcheck // close only this test attachment
		}
	}()
	life := NewLifecycle(first.Shutdown, func(err error) {
		if err != nil {
			t.Error(err)
		}
	}, func(uint64) {})
	life.Stop()
	second := start("restart")
	for _, id := range []string{"local", "remote"} {
		status, body := desktopHTTP(t, second, http.MethodGet, "/api/hosts/"+id+"/sessions", "", second.Bridge.token)
		if status != http.StatusOK || !strings.Contains(body, "desktop-survival") {
			t.Fatalf("%s session did not survive: %d %s", id, status, body)
		}
		attachments = append(attachments, attachDesktopSession(t, second, id))
	}
}

func attachDesktopSession(t *testing.T, controller *Controller, hostID string) *websocket.Conn {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"hostId": hostID, "session": "desktop-survival"})
	if err != nil {
		t.Fatal(err)
	}
	status, body := desktopHTTP(t, controller, http.MethodPost, "/api/ws-ticket",
		string(payload), controller.Bridge.token)
	var issued struct {
		Ticket string `json:"ticket"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &issued) != nil {
		t.Fatal("ticket failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	address := "ws" + strings.TrimPrefix(controller.Bridge.origin, "http") +
		"/ws/" + hostID + "/desktop-survival?ticket=" + issued.Ticket
	connection, _, err := websocket.Dial(ctx, address, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {controller.Bridge.origin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, frame, err := connection.Read(ctx)
	if err != nil || !strings.Contains(string(frame), `"ready"`) {
		_ = connection.CloseNow() //nolint:errcheck // preserve primary test failure
		t.Fatalf("%s attachment failed: %v %s", hostID, err, frame)
	}
	return connection
}
