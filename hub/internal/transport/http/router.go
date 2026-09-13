package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

const (
	defaultMaxRequestBodyBytes int64 = 64 * 1024 // 64 KiB
	localHostID                      = "local"
)

// SessionService specifies session operations required by the HTTP router.
type SessionService interface {
	ListSessions(ctx context.Context) ([]session.Session, error)
	CreateSession(ctx context.Context, name string) (*session.Session, error)
	RenameSession(ctx context.Context, oldName, newName string) error
	KillSession(ctx context.Context, name string) error
	GetSession(ctx context.Context, name string) (*session.Session, error)
	HasSession(ctx context.Context, name string) bool
}

// TicketIssuer specifies the contract to generate single-use terminal tickets.
type TicketIssuer interface {
	Issue(session string) (id string, expiresAt time.Time, err error)
}

// RouterConfig holds dependencies and configuration for constructing the HTTP router.
type RouterConfig struct {
	Token               string
	MaxRequestBodyBytes int64
	WebConfig           config.WebConfig
	StaticFS            fs.FS
}

type handlerState struct {
	cfg      RouterConfig
	sessions SessionService
	tickets  TicketIssuer
}

func requireLocalHost(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("hostId") != localHostID {
		writeError(w, http.StatusNotFound, "host_not_found")
		return false
	}
	return true
}

func mapSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, session.ErrNameInUse):
		writeError(w, http.StatusConflict, "name_in_use")
	case errors.Is(err, session.ErrNotFound):
		writeError(w, http.StatusNotFound, "session_not_found")
	case errors.Is(err, session.ErrTmuxNotFound):
		writeError(w, http.StatusServiceUnavailable, "tmux_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *handlerState) listSessions(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) {
		return
	}
	list, err := s.sessions.ListSessions(r.Context())
	if err != nil {
		mapSessionError(w, err)
		return
	}
	if list == nil {
		list = []session.Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *handlerState) createSession(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	sess, err := s.sessions.CreateSession(r.Context(), req.Name)
	if err != nil {
		mapSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *handlerState) renameSession(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) {
		return
	}
	oldName := r.PathValue("name")
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req renameSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.sessions.RenameSession(r.Context(), oldName, req.Name); err != nil {
		mapSessionError(w, err)
		return
	}
	sess, err := s.sessions.GetSession(r.Context(), req.Name)
	if err != nil {
		mapSessionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *handlerState) killSession(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := s.sessions.KillSession(r.Context(), name); err != nil {
		mapSessionError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *handlerState) issueTicket(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req wsTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.HostID != localHostID {
		writeError(w, http.StatusNotFound, "host_not_found")
		return
	}
	if !s.sessions.HasSession(r.Context(), req.Session) {
		writeError(w, http.StatusNotFound, "session_not_found")
		return
	}
	id, expiresAt, err := s.tickets.Issue(req.Session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ticket_generation_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":    id,
		"expiresAt": expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

func (s *handlerState) getClientConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	dto := NewPublicClientConfig(s.cfg.WebConfig)
	writeJSON(w, http.StatusOK, dto)
}

// NewRouter wires authentication, API endpoints, WebSocket handler, and static SPA serving.
func NewRouter(
	cfg RouterConfig,
	sessions SessionService,
	tickets TicketIssuer,
	wsHandler http.Handler,
) http.Handler {
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}

	state := &handlerState{
		cfg:      cfg,
		sessions: sessions,
		tickets:  tickets,
	}

	auth := RequireAuth(cfg.Token)
	mux := http.NewServeMux()

	// REST API (Authenticated)
	mux.Handle("GET /api/hosts/{hostId}/sessions", auth(http.HandlerFunc(state.listSessions)))
	mux.Handle("POST /api/hosts/{hostId}/sessions", auth(http.HandlerFunc(state.createSession)))
	mux.Handle("PATCH /api/hosts/{hostId}/sessions/{name}", auth(http.HandlerFunc(state.renameSession)))
	mux.Handle("DELETE /api/hosts/{hostId}/sessions/{name}", auth(http.HandlerFunc(state.killSession)))
	mux.Handle("POST /api/ws-ticket", auth(http.HandlerFunc(state.issueTicket)))

	// Client Config (Unauthenticated, no-store)
	mux.HandleFunc("GET /api/client-config", state.getClientConfig)

	// WebSocket handler
	if wsHandler != nil {
		mux.Handle("GET /ws/{hostId}/{session}", wsHandler)
	}

	// Static SPA handler
	if cfg.StaticFS != nil {
		mux.Handle("/", NewStaticSPAHandler(cfg.StaticFS))
	}

	return mux
}
