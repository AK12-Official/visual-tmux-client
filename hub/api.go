package main

import (
	"encoding/json"
	"errors"
	"net/http"
)

// apiError is the uniform JSON error envelope returned by every /api route.
type apiError struct {
	Error string `json:"error"`
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the uniform JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

// requireLocalHost enforces that the host-addressed route targets the only
// host the hub manages ("local"), returning 404 host_not_found otherwise.
func (s *server) requireLocalHost(w http.ResponseWriter, r *http.Request) bool {
	if r.PathValue("hostId") != "local" {
		writeError(w, http.StatusNotFound, "host_not_found")
		return false
	}
	return true
}

// listSessions handles GET /api/hosts/{hostId}/sessions.
func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	if !s.requireLocalHost(w, r) {
		return
	}
	sessions, err := s.tmux.ListSessions()
	if err != nil {
		if errors.Is(err, ErrTmuxNotFound) {
			writeError(w, http.StatusServiceUnavailable, "tmux_unavailable")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Guarantee the empty list serializes as [] rather than null.
	if sessions == nil {
		sessions = []Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type createSessionRequest struct {
	Name string `json:"name"`
}

// createSession handles POST /api/hosts/{hostId}/sessions.
func (s *server) createSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireLocalHost(w, r) {
		return
	}
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	sess, err := s.tmux.CreateSession(req.Name)
	if err != nil {
		s.writeTmuxError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

type renameSessionRequest struct {
	Name string `json:"name"`
}

// renameSession handles PATCH /api/hosts/{hostId}/sessions/{name}.
func (s *server) renameSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireLocalHost(w, r) {
		return
	}
	oldName := r.PathValue("name")
	var req renameSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.tmux.RenameSession(oldName, req.Name); err != nil {
		s.writeTmuxError(w, err)
		return
	}
	sess, err := s.tmux.getSession(req.Name)
	if err != nil {
		s.writeTmuxError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// killSession handles DELETE /api/hosts/{hostId}/sessions/{name}.
func (s *server) killSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireLocalHost(w, r) {
		return
	}
	name := r.PathValue("name")
	if err := s.tmux.KillSession(name); err != nil {
		s.writeTmuxError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type wsTicketRequest struct {
	HostID  string `json:"hostId"`
	Session string `json:"session"`
}

// issueTicket handles POST /api/ws-ticket. Requires bearer auth (enforced by
// the router) and rejects a ticket request for a nonexistent session.
func (s *server) issueTicket(w http.ResponseWriter, r *http.Request) {
	var req wsTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.HostID != "local" {
		writeError(w, http.StatusNotFound, "host_not_found")
		return
	}
	if !s.tmux.hasSession(req.Session) {
		writeError(w, http.StatusNotFound, "session_not_found")
		return
	}
	id, expiresAt := s.tickets.issue(req.Session)
	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":    id,
		"expiresAt": expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// writeTmuxError maps tmux sentinel errors to HTTP status codes.
func (s *server) writeTmuxError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidName):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrNameInUse):
		writeError(w, http.StatusConflict, "name_in_use")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "session_not_found")
	case errors.Is(err, ErrTmuxNotFound):
		writeError(w, http.StatusServiceUnavailable, "tmux_unavailable")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
