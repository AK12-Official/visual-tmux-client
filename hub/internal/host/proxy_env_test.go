package host_test

import (
	"reflect"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
)

func TestProxyEnvironmentNamesTheProxyForTerminalCommands(t *testing.T) {
	if got := host.ProxyEnvironment(""); got != nil {
		t.Fatalf("an empty proxy address added %d variables", len(got))
	}

	want := map[string]string{
		"http_proxy":  "socks5h://127.0.0.1:7890",
		"HTTP_PROXY":  "socks5h://127.0.0.1:7890",
		"https_proxy": "socks5h://127.0.0.1:7890",
		"HTTPS_PROXY": "socks5h://127.0.0.1:7890",
		"all_proxy":   "socks5h://127.0.0.1:7890",
		"ALL_PROXY":   "socks5h://127.0.0.1:7890",
		"no_proxy":    "localhost,127.0.0.1,::1,.local",
		"NO_PROXY":    "localhost,127.0.0.1,::1,.local",
	}
	if got := host.ProxyEnvironment("127.0.0.1:7890"); !reflect.DeepEqual(got, want) {
		t.Fatalf("proxy environment:\n got %v\nwant %v", got, want)
	}

	// The stored forms of an address are used as they are.
	for _, address := range []string{"[::1]:7890", "proxy.example:1080"} {
		got := host.ProxyEnvironment(address)
		if len(got) != len(want) || got["https_proxy"] != "socks5h://"+address || got["NO_PROXY"] != want["NO_PROXY"] {
			t.Fatalf("proxy environment for %q: %v", address, got)
		}
	}
}
