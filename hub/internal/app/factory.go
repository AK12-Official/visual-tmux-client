package app

import (
	"context"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"
)

func sessionBackendFactory(tmuxPath, socket string) *tmux.Client {
	return tmux.NewClient(tmuxPath, socket)
}

// localSessionEnvironment reads the proxy setting whenever a session is
// created, so a changed address applies to the next session. It yields no
// variables while no proxy is set.
func localSessionEnvironment(registry *host.Registry) tmux.SessionEnvironment {
	return func(ctx context.Context) (map[string]string, error) {
		settings, err := registry.GetSettings(ctx)
		if err != nil {
			return nil, err
		}
		return host.ProxyEnvironment(settings.ProxyAddress), nil
	}
}
