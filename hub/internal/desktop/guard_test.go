package desktop

import (
	"os"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.ParentGuard(m)) }
