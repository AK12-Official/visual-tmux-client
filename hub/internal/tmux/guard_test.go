package tmux

import (
	"os"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// These tests start real tmux servers through Client, which is the same code
// path the hub uses, so a change to the environment it builds would reach a
// developer's own session. ParentGuard stands a disposable server in for it;
// see its doc comment.
func TestMain(m *testing.M) {
	os.Exit(testutil.ParentGuard(m))
}
