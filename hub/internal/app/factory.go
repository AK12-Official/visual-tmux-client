package app

import "github.com/AK12-Official/visual-tmux-client/hub/internal/tmux"

func sessionBackendFactory(tmuxPath, socket string) *tmux.Client {
	return tmux.NewClient(tmuxPath, socket)
}
