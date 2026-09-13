package http

import (
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

const markdownContentType = "text/markdown; charset=utf-8"

// ContentTypeFor maps a filename to its MIME Content-Type header.
func ContentTypeFor(name string) string {
	if ext := filepath.Ext(name); strings.EqualFold(ext, ".md") {
		return markdownContentType
	} else if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// NewStaticSPAHandler serves the embedded frontend single page application.
func NewStaticSPAHandler(staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
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
		if name := strings.TrimPrefix(path, "/"); name != "" && name != "index.html" {
			if data, err := fs.ReadFile(staticFS, name); err == nil {
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Content-Type", ContentTypeFor(name))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(data) //nolint:errcheck // best effort write
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		data, err := fs.ReadFile(staticFS, "index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data) //nolint:errcheck // best effort write
	})
}
