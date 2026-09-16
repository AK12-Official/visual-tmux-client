package main

import (
	"os"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// The smoke and e2e tests in this package build the hub and drive real tmux
// servers, so they are the ones most able to reach a developer's own session.
// ParentGuard stands a disposable server in for it; see its doc comment.
func TestMain(m *testing.M) {
	os.Exit(testutil.ParentGuard(m))
}
