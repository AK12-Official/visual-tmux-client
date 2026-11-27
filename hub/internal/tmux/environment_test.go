package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCreateArgsPassesTheEnvironmentLiterallyInNameOrder(t *testing.T) {
	environment := map[string]string{
		"no_proxy":    "localhost,127.0.0.1,::1,.local",
		"https_proxy": "socks5h://[::1]:7890",
		"HTTP_PROXY":  "value with spaces=and=signs",
	}
	queue := CreateArgs("work", "/home/user", environment)
	args := queue[slices.Index(queue, "new-session"):]
	want := []string{
		"new-session", "-d", "-s", "work", "-c", "/home/user",
		"-e", "HTTP_PROXY=value with spaces=and=signs",
		"-e", "https_proxy=socks5h://[::1]:7890",
		"-e", "no_proxy=localhost,127.0.0.1,::1,.local",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("new-session arguments:\n got %q\nwant %q", args, want)
	}

	without := CreateArgs("work", ".", nil)
	if strings.Contains(strings.Join(without, "\x00"), "\x00-e\x00") {
		t.Fatalf("a session without an environment still sets variables: %q", without)
	}
}

// A proxy is passed to the sessions created while it is set, and to the
// windows they open later; sessions created before or after it are not touched.
// The tmux server inherits whatever proxy variables the machine's own
// environment holds, so only the application's scheme is asserted.
func TestCreatedSessionCarriesTheSessionEnvironment(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	if _, err := c.Create(ctx, "before"); err != nil {
		t.Fatalf("create session before the proxy: %v", err)
	}
	environment := func(name string) string {
		t.Helper()
		// A new window proves the variables reach the shell of the session,
		// not only tmux's own record of it. printenv takes one name at a time;
		// END marks the command as finished.
		out := filepath.Join(t.TempDir(), "window-environment")
		command := "printenv https_proxy > " + out +
			" ; printenv no_proxy >> " + out + " ; echo END >> " + out
		_, stderr, code, err := c.Exec(ctx, "new-window", "-d", "-t", name, command)
		if err != nil || code != 0 {
			t.Fatalf("open a window in %s: code=%d %v %s", name, code, err, stderr)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			content, err := os.ReadFile(out)
			if err == nil && strings.HasSuffix(string(content), "END\n") {
				return strings.TrimSuffix(string(content), "END\n")
			}
			if time.Now().After(deadline) {
				t.Fatalf("window environment of %s: %q %v", name, content, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if got := environment("before"); strings.Contains(got, "socks5h://") {
		t.Fatalf("session created without a proxy carries %q", got)
	}

	c.SetSessionEnvironment(func(context.Context) (map[string]string, error) {
		return map[string]string{
			"https_proxy": "socks5h://127.0.0.1:7890",
			"no_proxy":    "localhost,127.0.0.1,::1,.local",
		}, nil
	})
	if _, err := c.Create(ctx, "with-proxy"); err != nil {
		t.Fatalf("create session with the proxy: %v", err)
	}
	want := "socks5h://127.0.0.1:7890\nlocalhost,127.0.0.1,::1,.local\n"
	if got := environment("with-proxy"); got != want {
		t.Fatalf("window environment with the proxy:\n got %q\nwant %q", got, want)
	}
	shown, stderr, code, err := c.Exec(ctx, "show-environment", "-t", "with-proxy", "https_proxy")
	if err != nil || code != 0 || strings.TrimSpace(shown) != "https_proxy=socks5h://127.0.0.1:7890" {
		t.Fatalf("session environment record: code=%d %v %q %s", code, err, shown, stderr)
	}

	c.SetSessionEnvironment(func(context.Context) (map[string]string, error) { return nil, nil })
	if _, err := c.Create(ctx, "after"); err != nil {
		t.Fatalf("create session after the proxy was cleared: %v", err)
	}
	if got := environment("after"); strings.Contains(got, "socks5h://") {
		t.Fatalf("session created after the proxy was cleared carries %q", got)
	}
	if got := environment("with-proxy"); got != want {
		t.Fatalf("session with the proxy changed after the setting was cleared: %q", got)
	}
	if got := environment("before"); strings.Contains(got, "socks5h://") {
		t.Fatalf("earlier session changed: %q", got)
	}
}

// The environment is read for each session, so a changed setting applies to
// the next one, and a session is not created without variables it should have.
func TestCreateReadsTheSessionEnvironmentForEachSession(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	reads := 0
	c.SetSessionEnvironment(func(context.Context) (map[string]string, error) {
		reads++
		return nil, nil
	})
	for _, name := range []string{"first", "second"} {
		if _, err := c.Create(ctx, name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if reads != 2 {
		t.Fatalf("the environment was read %d times for 2 sessions", reads)
	}

	c.SetSessionEnvironment(func(context.Context) (map[string]string, error) {
		return nil, errors.New("settings unavailable")
	})
	_, err := c.Create(ctx, "half-configured")
	if err == nil || !strings.Contains(err.Error(), "settings unavailable") {
		t.Fatalf("expected the unreadable setting to be reported, got %v", err)
	}
	if c.HasSession(ctx, "half-configured") {
		t.Fatal("a session was created without the environment it should have")
	}
}
