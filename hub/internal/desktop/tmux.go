package desktop

import (
	"os"
	"os/exec"
	"runtime"
)

// ResolveTmux only finds an executable. It does not invoke tmux or a shell.
// Missing tmux is left for existing local capability reporting, so SSH still works.
func ResolveTmux(configured string) string {
	return resolveTmux(configured, runtime.GOOS, exec.LookPath, executable)
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func resolveTmux(configured, platform string, lookup func(string) (string, error), exists func(string) bool) string {
	if configured != "" {
		return configured
	}
	if path, err := lookup("tmux"); err == nil {
		return path
	}
	if platform == "darwin" {
		for _, path := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux"} {
			if exists(path) {
				return path
			}
		}
	}
	return ""
}
