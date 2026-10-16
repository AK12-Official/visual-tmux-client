package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

type failedLookup struct {
	mockSessionService
	err error
}

func (s *failedLookup) GetSession(context.Context, string) (*session.Session, error) {
	return nil, s.err
}

func TestTicketLookupPreservesFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"absent", session.ErrNotFound, http.StatusNotFound, "session_not_found"},
		{"unreachable", &host.Error{Code: host.ErrUnavailable, Cause: errors.New("secret diagnostic")}, http.StatusServiceUnavailable, "host_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tickets := auth.NewTicketStore(time.Minute, nil)
			router := NewRouter(RouterConfig{Token: "token"}, &failedLookup{err: tc.err}, nil, tickets, nil)
			req := httptest.NewRequest(http.MethodPost, "/api/ws-ticket", strings.NewReader(`{"hostId":"local","session":"dev"}`))
			req.Header.Set("Authorization", "Bearer token")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "secret") || tickets.Len() != 0 {
				t.Fatalf("ticket lookup: status=%d body=%s tickets=%d", w.Code, w.Body.String(), tickets.Len())
			}
		})
	}
}
