package host_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

func TestFailureCategoriesStayDistinct(t *testing.T) {
	categories := []error{
		host.ErrNotFound,
		session.ErrNotFound,
		host.ErrUnavailable,
		host.ErrUnsupported,
		host.ErrOutcomeUnknown,
		host.ErrStaleRevision,
	}
	for _, category := range categories {
		failure := category
		var code host.ErrorCode
		if errors.As(category, &code) {
			failure = &host.Error{Target: host.Target{HostID: "remote", Revision: 2}, Code: code}
		}
		failure = fmt.Errorf("operation: %w", failure)
		for _, other := range categories {
			if errors.Is(failure, other) != errors.Is(category, other) {
				t.Fatalf("%v classification as %v is incorrect", failure, other)
			}
		}
	}
}

func TestTransportCauseIsInspectableButNotPrinted(t *testing.T) {
	cause := errors.New("transport diagnostic containing a secret")
	target := host.Target{HostID: "remote", Revision: 3, Session: "dev"}
	err := &host.Error{Target: target, Code: host.ErrUnavailable, Cause: cause}
	if err.Error() != "host_unavailable" || !errors.Is(err, cause) {
		t.Fatal("error must preserve its cause without printing it")
	}
	var detail *host.Error
	if !errors.As(fmt.Errorf("lookup: %w", err), &detail) || detail.Target != target {
		t.Fatal("wrapped error lost its target")
	}
}
