package desktop

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
)

func TestNativeDiagnosticsRedactConsumedLaunchAndBearer(t *testing.T) {
	launch, err := auth.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := auth.GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	NativeLogger(&output).Warn("navigation http://127.0.0.1:1234/#launch="+launch,
		slog.Group("request", slog.String("authorization", "Bearer "+bearer)),
		slog.Any("error", errors.New("failed "+launch)))
	logged := output.String()
	if strings.Contains(logged, launch) || strings.Contains(logged, bearer) ||
		!strings.Contains(logged, "navigation") || !strings.Contains(logged, "[redacted]") {
		t.Fatalf("unexpected redacted diagnostics: %q", logged)
	}
}
