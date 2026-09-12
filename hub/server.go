package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// webFS embeds the built frontend. `all:` includes files that would otherwise
// be skipped by the embed tool.
//
//go:embed all:web/dist
var webFS embed.FS

// server holds the hub's runtime state: configuration, the tmux client, the
// ticket store, and the set of active attachments (for graceful shutdown).
type server struct {
	cfg     *config
	token   string
	origin  string
	tmux    *tmuxClient
	tickets *ticketStore

	mu          sync.Mutex
	attachments map[*attachment]struct{}

	// Guards the one-time window-size policy pin; see pinWindowSizePolicy.
	sizePolicyOnce sync.Once
}

func newServer(cfg *config, token string) *server {
	return &server{
		cfg:         cfg,
		token:       token,
		origin:      cfg.origin,
		tmux:        newTmuxClient(""),
		tickets:     newTicketStore(),
		attachments: make(map[*attachment]struct{}),
	}
}

// pinWindowSizePolicy sets tmux's global window-size policy to "latest" once
// per hub run, on the first attachment. Older tmux defaults differ, and the
// policy governs how the sessions we attach resize. It must NOT run on every
// attachment: setting a global tmux option repaints every client on the
// server, which our background attachments would report as activity.
func (s *server) pinWindowSizePolicy() {
	s.sizePolicyOnce.Do(func() {
		_, _, _, _ = s.tmux.exec("set-option", "-g", "window-size", "latest")
	})
}

// auth wraps a handler with the bearer-token middleware.
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(s.token, next)
}

// handler builds the HTTP router.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/hosts/{hostId}/sessions", s.auth(s.listSessions))
	mux.HandleFunc("POST /api/hosts/{hostId}/sessions", s.auth(s.createSession))
	mux.HandleFunc("PATCH /api/hosts/{hostId}/sessions/{name}", s.auth(s.renameSession))
	mux.HandleFunc("DELETE /api/hosts/{hostId}/sessions/{name}", s.auth(s.killSession))
	mux.HandleFunc("POST /api/ws-ticket", s.auth(s.issueTicket))
	mux.HandleFunc("GET /ws/{hostId}/{session}", s.attach)
	mux.Handle("/", s.spaHandler())
	return mux
}

// contentTypeFor maps a dist file name to its Content-Type. Only root-level
// public assets (the embedded guide) rely on this; `.md` is missing from some
// systems' MIME databases, so it is mapped explicitly before the generic
// lookup.
func contentTypeFor(name string) string {
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".md") {
		return "text/markdown; charset=utf-8"
	} else if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// track registers an active attachment so shutdown can close it.
func (s *server) track(a *attachment) {
	s.mu.Lock()
	s.attachments[a] = struct{}{}
	s.mu.Unlock()
}

// untrack removes an attachment from the active set.
func (s *server) untrack(a *attachment) {
	s.mu.Lock()
	delete(s.attachments, a)
	s.mu.Unlock()
}

// shutdownAll closes every active attachment concurrently, killing its pty process.
func (s *server) shutdownAll() {
	s.mu.Lock()
	list := make([]*attachment, 0, len(s.attachments))
	for a := range s.attachments {
		list = append(list, a)
	}
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, a := range list {
		wg.Add(1)
		go func(att *attachment) {
			defer wg.Done()
			att.shutdown()
		}(a)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		for _, a := range list {
			_ = a.c.CloseNow()
		}
	}
}

// spaHandler serves the embedded frontend. Content-hashed assets under /assets/
// are served with a long immutable cache lifetime; other real files present in
// dist (e.g. the embedded tmux guide that vite copies from public/) are served
// as themselves; every remaining non-API, non-WS path falls back to index.html
// (SPA fallback). Unknown /api/ and /ws/ paths are 404 rather than falling
// through to index.html.
func (s *server) spaHandler() http.Handler {
	dist, _ := fs.Sub(webFS, "web/dist")
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/") {
			http.NotFound(w, r)
			return
		}
		// Serve real root-level files before the SPA fallback, so e.g. the
		// guide at /tmux-guide.zh-CN.md is not swallowed by index.html.
		// http.ServeMux has already cleaned the path, and embed.FS rejects
		// anything outside its tree, so this cannot escape dist.
		if name := strings.TrimPrefix(path, "/"); name != "" && name != "index.html" {
			if data, err := fs.ReadFile(dist, name); err == nil {
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Content-Type", contentTypeFor(name))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(data)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		data, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
}
