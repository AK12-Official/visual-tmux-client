package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/config"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

const (
	defaultMaxRequestBodyBytes int64         = 64 * 1024 // 64 KiB
	localHostID                              = "local"
	defaultProbeTimeout        time.Duration = 10 * time.Second
	defaultTrustStoreTTL       time.Duration = 5 * time.Minute
)

// SessionService specifies session operations required by the HTTP router.
type SessionService = host.Sessions

// TicketIssuer specifies the contract to generate single-use terminal tickets.
type TicketIssuer interface {
	Issue(session string) (id string, expiresAt time.Time, err error)
}

// HostRegistry defines host administration persistence.
type HostRegistry interface {
	List(ctx context.Context) ([]host.Host, error)
	Get(ctx context.Context, id host.ID) (host.Host, error)
	Save(ctx context.Context, input host.HostInput, expected host.Revision) (host.Host, error)
	Delete(ctx context.Context, id host.ID, expected host.Revision) error
}

// RouterConfig holds dependencies and configuration for constructing the HTTP router.
type RouterConfig struct {
	Token               string
	MaxRequestBodyBytes int64
	// MaxFileSize bounds a file write request body. It is separate from
	// MaxRequestBodyBytes because a write is the one request that legitimately
	// carries megabytes.
	MaxFileSize int64
	// FilesEnabled is published to the browser so it can leave the file manager's
	// entry point out. It grants nothing: every operation is gated on its own.
	FilesEnabled  bool
	WebConfig     config.WebConfig
	StaticFS      fs.FS
	Hosts         HostRegistry
	OnHostChanged func(id host.ID)
	TrustStore    *host.TrustStore
	Capabilities  host.CapabilityChecker
	ProbeDialer   func(ctx context.Context, address string, port uint16, timeout time.Duration) (ssh.PublicKey, error)
}

type handlerState struct {
	cfg           RouterConfig
	sessions      SessionService
	files         FileService
	tickets       TicketIssuer
	resolver      host.Resolver
	runtime       host.Runtime
	hosts         HostRegistry
	onHostChanged func(id host.ID)
	trustStore    *host.TrustStore
	capabilities  host.CapabilityChecker
	probeDialer   func(ctx context.Context, address string, port uint16, timeout time.Duration) (ssh.PublicKey, error)
}

// resolve copies handler state for this request; shared routing state is immutable.
func (s *handlerState) resolve(w http.ResponseWriter, r *http.Request, id string) (*handlerState, bool) {
	runtime, err := s.resolver.Resolve(r.Context(), host.ID(id))
	if err != nil {
		mapSessionError(w, err)
		return nil, false
	}
	scoped := *s
	scoped.runtime = runtime
	scoped.sessions, scoped.files = runtime.Sessions, runtime.Files
	return &scoped, true
}

func mapSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, host.ErrNotFound):
		writeError(w, http.StatusNotFound, "host_not_found")
	case errors.Is(err, host.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "host_unavailable")
	case errors.Is(err, host.ErrUnsupported):
		writeError(w, http.StatusNotImplemented, "host_unsupported")
	case errors.Is(err, host.ErrOutcomeUnknown):
		writeError(w, http.StatusBadGateway, "outcome_unknown")
	case errors.Is(err, host.ErrStaleRevision):
		writeError(w, http.StatusConflict, "host_revision_conflict")
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
	var ok bool
	s, ok = s.resolve(w, r, r.PathValue("hostId"))
	if !ok {
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
	var ok bool
	s, ok = s.resolve(w, r, r.PathValue("hostId"))
	if !ok {
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
	var ok bool
	s, ok = s.resolve(w, r, r.PathValue("hostId"))
	if !ok {
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
	var ok bool
	s, ok = s.resolve(w, r, r.PathValue("hostId"))
	if !ok {
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
	var ok bool
	s, ok = s.resolve(w, r, req.HostID)
	if !ok {
		return
	}
	if _, err := s.sessions.GetSession(r.Context(), req.Session); err != nil {
		mapSessionError(w, err)
		return
	}
	var (
		id        string
		expiresAt time.Time
		err       error
	)
	if ti, ok := s.tickets.(interface {
		IssueForTarget(string, uint64, string) (string, time.Time, error)
	}); ok {
		hID := string(s.runtime.Target.HostID)
		connRev := uint64(s.runtime.Target.ConnectionRevision)
		id, expiresAt, err = ti.IssueForTarget(hID, connRev, req.Session)
	} else {
		id, expiresAt, err = s.tickets.Issue(req.Session)
	}
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
	dto := NewPublicClientConfig(s.cfg.WebConfig, s.cfg.FilesEnabled, s.cfg.MaxFileSize)
	writeJSON(w, http.StatusOK, dto)
}

func defaultLocalHost() host.Host {
	return host.Host{
		ID:                 host.LocalID,
		Revision:           host.LocalRevision,
		ConnectionRevision: host.LocalRevision,
		Name:               "Local",
		Trusted:            true,
	}
}

func (s *handlerState) listHosts(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"hosts": []host.Host{defaultLocalHost()},
		})
		return
	}
	hosts, err := s.hosts.List(r.Context())
	if err != nil {
		mapHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

func (s *handlerState) getHost(w http.ResponseWriter, r *http.Request) {
	hostID := host.ID(r.PathValue("hostId"))
	if s.hosts == nil {
		if hostID == host.LocalID {
			writeJSON(w, http.StatusOK, defaultLocalHost())
			return
		}
		writeError(w, http.StatusNotFound, "host_not_found")
		return
	}
	h, err := s.hosts.Get(r.Context(), hostID)
	if err != nil {
		mapHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

type createHostRequest struct {
	ID             host.ID          `json:"id"`
	Name           string           `json:"name"`
	Address        string           `json:"address"`
	Port           uint16           `json:"port"`
	Username       string           `json:"username"`
	CredentialType string           `json:"credentialType"`
	Credentials    host.Credentials `json:"credentials"`
	HostKey        []byte           `json:"hostKey"`
	ChallengeToken string           `json:"challengeToken"`
}

func (s *handlerState) createHost(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeError(w, http.StatusNotImplemented, "host_management_disabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req createHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.ID == host.LocalID {
		writeError(w, http.StatusBadRequest, "cannot modify local host")
		return
	}
	hostKey := req.HostKey
	if req.ChallengeToken != "" && len(hostKey) == 0 && s.trustStore != nil {
		port := req.Port
		if port == 0 {
			port = 22
		}
		endpoint := net.JoinHostPort(req.Address, fmt.Sprintf("%d", port))
		pinned, err := s.trustStore.Confirm(req.ChallengeToken, "", 0, endpoint)
		if err != nil {
			if errors.Is(err, host.ErrChallengeExpired) {
				writeError(w, http.StatusConflict, "challenge_expired")
			} else {
				writeError(w, http.StatusConflict, "challenge_mismatch")
			}
			return
		}
		hostKey = pinned
	}
	input := host.HostInput{
		ID:             req.ID,
		Name:           req.Name,
		Address:        req.Address,
		Port:           req.Port,
		Username:       req.Username,
		CredentialType: req.CredentialType,
		Credentials:    req.Credentials,
		HostKey:        hostKey,
	}
	created, err := s.hosts.Save(r.Context(), input, 0)
	if err != nil {
		mapHostError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type updateHostRequest struct {
	Name           string           `json:"name"`
	Address        string           `json:"address"`
	Port           uint16           `json:"port"`
	Username       string           `json:"username"`
	CredentialType string           `json:"credentialType"`
	Credentials    host.Credentials `json:"credentials"`
	HostKey        []byte           `json:"hostKey"`
	Revision       host.Revision    `json:"revision"`
}

func (s *handlerState) updateHost(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeError(w, http.StatusNotImplemented, "host_management_disabled")
		return
	}
	hostID := host.ID(r.PathValue("hostId"))
	if hostID == host.LocalID {
		writeError(w, http.StatusBadRequest, "cannot modify local host")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req updateHostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	expectedRev := req.Revision
	if expectedRev == 0 {
		if revStr := r.URL.Query().Get("revision"); revStr != "" {
			if v, err := strconv.ParseUint(revStr, 10, 64); err == nil {
				expectedRev = host.Revision(v)
			}
		}
	}
	if expectedRev == 0 {
		writeError(w, http.StatusBadRequest, "revision required")
		return
	}
	input := host.HostInput{
		ID:             hostID,
		Name:           req.Name,
		Address:        req.Address,
		Port:           req.Port,
		Username:       req.Username,
		CredentialType: req.CredentialType,
		Credentials:    req.Credentials,
		HostKey:        req.HostKey,
	}
	updated, err := s.hosts.Save(r.Context(), input, expectedRev)
	if err != nil {
		mapHostError(w, err)
		return
	}
	if s.onHostChanged != nil {
		s.onHostChanged(hostID)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *handlerState) deleteHost(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeError(w, http.StatusNotImplemented, "host_management_disabled")
		return
	}
	hostID := host.ID(r.PathValue("hostId"))
	if hostID == host.LocalID {
		writeError(w, http.StatusBadRequest, "cannot modify local host")
		return
	}
	var expectedRev host.Revision
	if revStr := r.URL.Query().Get("revision"); revStr != "" {
		if v, err := strconv.ParseUint(revStr, 10, 64); err == nil {
			expectedRev = host.Revision(v)
		}
	}
	if expectedRev == 0 && r.Body != nil {
		var req struct {
			Revision host.Revision `json:"revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			expectedRev = req.Revision
		}
	}
	err := s.hosts.Delete(r.Context(), hostID, expectedRev)
	if err != nil {
		mapHostError(w, err)
		return
	}
	if s.onHostChanged != nil {
		s.onHostChanged(hostID)
	}
	w.WriteHeader(http.StatusNoContent)
}

type probeEndpointRequest struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

func (s *handlerState) probeDraftEndpoint(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req probeEndpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	trimmedAddr := strings.TrimSpace(req.Address)
	if trimmedAddr == "" {
		writeError(w, http.StatusBadRequest, "address required")
		return
	}
	port := req.Port
	if port == 0 {
		port = 22
	}
	if s.probeDialer == nil {
		writeError(w, http.StatusNotImplemented, "probing_disabled")
		return
	}
	key, err := s.probeDialer(r.Context(), trimmedAddr, port, defaultProbeTimeout)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "probe_failed")
		return
	}
	endpoint := net.JoinHostPort(trimmedAddr, fmt.Sprintf("%d", port))
	res, err := s.trustStore.CreateChallenge("", 0, endpoint, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "challenge_creation_failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *handlerState) probeHostEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeError(w, http.StatusNotImplemented, "host_management_disabled")
		return
	}
	hostID := host.ID(r.PathValue("hostId"))
	if hostID == host.LocalID {
		writeError(w, http.StatusBadRequest, "cannot probe local host")
		return
	}
	h, err := s.hosts.Get(r.Context(), hostID)
	if err != nil {
		mapHostError(w, err)
		return
	}
	port := h.Port
	if port == 0 {
		port = 22
	}
	if s.probeDialer == nil {
		writeError(w, http.StatusNotImplemented, "probing_disabled")
		return
	}
	key, err := s.probeDialer(r.Context(), h.Address, port, defaultProbeTimeout)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "probe_failed")
		return
	}
	endpoint := net.JoinHostPort(h.Address, fmt.Sprintf("%d", port))
	res, err := s.trustStore.CreateChallenge(hostID, h.Revision, endpoint, key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "challenge_creation_failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type confirmTrustRequest struct {
	ChallengeToken string `json:"challengeToken"`
}

func (s *handlerState) confirmHostTrust(w http.ResponseWriter, r *http.Request) {
	if s.hosts == nil {
		writeError(w, http.StatusNotImplemented, "host_management_disabled")
		return
	}
	hostID := host.ID(r.PathValue("hostId"))
	if hostID == host.LocalID {
		writeError(w, http.StatusBadRequest, "cannot modify local host")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	var req confirmTrustRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.ChallengeToken == "" {
		writeError(w, http.StatusBadRequest, "challenge token required")
		return
	}
	h, err := s.hosts.Get(r.Context(), hostID)
	if err != nil {
		mapHostError(w, err)
		return
	}
	port := h.Port
	if port == 0 {
		port = 22
	}
	endpoint := net.JoinHostPort(h.Address, fmt.Sprintf("%d", port))
	pinnedKey, err := s.trustStore.Confirm(req.ChallengeToken, hostID, h.Revision, endpoint)
	if err != nil {
		if errors.Is(err, host.ErrChallengeExpired) {
			writeError(w, http.StatusConflict, "challenge_expired")
		} else if errors.Is(err, host.ErrChallengeMismatch) {
			writeError(w, http.StatusConflict, "challenge_mismatch")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_challenge")
		}
		return
	}
	input := host.HostInput{
		ID:             hostID,
		Name:           h.Name,
		Address:        h.Address,
		Port:           h.Port,
		Username:       h.Username,
		CredentialType: h.CredentialType,
		HostKey:        pinnedKey,
	}
	updated, err := s.hosts.Save(r.Context(), input, h.Revision)
	if err != nil {
		mapHostError(w, err)
		return
	}
	if s.onHostChanged != nil {
		s.onHostChanged(hostID)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *handlerState) getHostCapabilities(w http.ResponseWriter, r *http.Request) {
	hostID := host.ID(r.PathValue("hostId"))
	if hostID == host.LocalID {
		writeJSON(w, http.StatusOK, host.HostCapabilities{
			HostID:   host.LocalID,
			Revision: host.LocalRevision,
			Online:   true,
			SSH:      host.CapabilityStatus{Available: true},
			Tmux:     host.CapabilityStatus{Available: true},
			SFTP:     host.CapabilityStatus{Available: false, Error: "not_applicable"},
		})
		return
	}
	if s.capabilities != nil {
		caps, err := s.capabilities.CheckCapabilities(r.Context(), hostID)
		if err != nil {
			mapHostError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, caps)
		return
	}
	if s.hosts == nil {
		writeError(w, http.StatusNotFound, "host_not_found")
		return
	}
	h, err := s.hosts.Get(r.Context(), hostID)
	if err != nil {
		mapHostError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host.HostCapabilities{
		HostID:   hostID,
		Revision: h.Revision,
		Online:   h.Trusted,
		SSH:      host.CapabilityStatus{Available: h.Trusted},
		Tmux:     host.CapabilityStatus{Available: h.Trusted},
		SFTP:     host.CapabilityStatus{Available: false, Error: "untested"},
	})
}

func mapHostError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, host.ErrNotFound):
		writeError(w, http.StatusNotFound, "host_not_found")
	case errors.Is(err, host.ErrStaleRevision):
		writeError(w, http.StatusConflict, "host_revision_conflict")
	case errors.Is(err, host.ErrUnsupported):
		writeError(w, http.StatusBadRequest, "unsupported_operation")
	case errors.Is(err, host.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "host_unavailable")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// NewRouter wires authentication, API endpoints, WebSocket handler, and static SPA serving.
func NewRouter(
	cfg RouterConfig,
	sessions SessionService,
	files FileService,
	tickets TicketIssuer,
	wsHandler http.Handler,
) http.Handler {
	return NewRouterWithResolver(cfg, host.NewLocalResolver(sessions, files, nil), tickets, wsHandler)
}

// NewRouterWithResolver constructs host-aware routing.
func NewRouterWithResolver(
	cfg RouterConfig,
	resolver host.Resolver,
	tickets TicketIssuer,
	wsHandler http.Handler,
) http.Handler {
	if cfg.MaxRequestBodyBytes <= 0 {
		cfg.MaxRequestBodyBytes = defaultMaxRequestBodyBytes
	}

	trustStore := cfg.TrustStore
	if trustStore == nil {
		trustStore = host.NewTrustStore(defaultTrustStoreTTL)
	}
	probeDialer := cfg.ProbeDialer
	if probeDialer == nil {
		probeDialer = host.ProbeEndpoint
	}

	state := &handlerState{
		cfg:           cfg,
		resolver:      resolver,
		tickets:       tickets,
		hosts:         cfg.Hosts,
		onHostChanged: cfg.OnHostChanged,
		trustStore:    trustStore,
		capabilities:  cfg.Capabilities,
		probeDialer:   probeDialer,
	}

	auth := RequireAuth(cfg.Token)
	mux := http.NewServeMux()

	// Host Administration API (Authenticated)
	mux.Handle("GET /api/hosts", auth(http.HandlerFunc(state.listHosts)))
	mux.Handle("POST /api/hosts", auth(http.HandlerFunc(state.createHost)))
	mux.Handle("POST /api/hosts/probe", auth(http.HandlerFunc(state.probeDraftEndpoint)))
	mux.Handle("GET /api/hosts/{hostId}", auth(http.HandlerFunc(state.getHost)))
	mux.Handle("PUT /api/hosts/{hostId}", auth(http.HandlerFunc(state.updateHost)))
	mux.Handle("PATCH /api/hosts/{hostId}", auth(http.HandlerFunc(state.updateHost)))
	mux.Handle("DELETE /api/hosts/{hostId}", auth(http.HandlerFunc(state.deleteHost)))
	mux.Handle("POST /api/hosts/{hostId}/probe", auth(http.HandlerFunc(state.probeHostEndpoint)))
	mux.Handle("POST /api/hosts/{hostId}/trust", auth(http.HandlerFunc(state.confirmHostTrust)))
	mux.Handle("GET /api/hosts/{hostId}/capabilities", auth(http.HandlerFunc(state.getHostCapabilities)))

	// REST API (Authenticated)
	mux.Handle("GET /api/hosts/{hostId}/sessions", auth(http.HandlerFunc(state.listSessions)))
	mux.Handle("POST /api/hosts/{hostId}/sessions", auth(http.HandlerFunc(state.createSession)))
	mux.Handle("PATCH /api/hosts/{hostId}/sessions/{name}", auth(http.HandlerFunc(state.renameSession)))
	mux.Handle("DELETE /api/hosts/{hostId}/sessions/{name}", auth(http.HandlerFunc(state.killSession)))
	mux.Handle("GET /api/hosts/{hostId}/sessions/{name}/working-directory",
		auth(http.HandlerFunc(state.sessionWorkingDirectory)))
	mux.Handle("POST /api/ws-ticket", auth(http.HandlerFunc(state.issueTicket)))

	// File API (Authenticated). read and download share a handler but stay
	// separate routes, so the browser's intent is legible in the request.
	mux.Handle("GET /api/hosts/{hostId}/files/list", auth(http.HandlerFunc(state.listFiles)))
	mux.Handle("GET /api/hosts/{hostId}/files/read", auth(http.HandlerFunc(state.readFile)))
	mux.Handle("GET /api/hosts/{hostId}/files/download", auth(http.HandlerFunc(state.downloadFile)))
	mux.Handle("PUT /api/hosts/{hostId}/files/write", auth(http.HandlerFunc(state.writeFile)))
	mux.Handle("POST /api/hosts/{hostId}/files/create", auth(http.HandlerFunc(state.createEntry)))
	mux.Handle("POST /api/hosts/{hostId}/files/rename", auth(http.HandlerFunc(state.renameEntry)))
	mux.Handle("POST /api/hosts/{hostId}/files/delete", auth(http.HandlerFunc(state.deleteEntry)))

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
