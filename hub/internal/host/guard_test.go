package host_test

import (
	"os"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

// ParentGuard detects server/session-list changes after tests; see AGENTS.md.
func TestMain(m *testing.M) {
	os.Exit(testutil.ParentGuard(m))
}
