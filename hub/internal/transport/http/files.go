package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
)

// FileService specifies the file operations the HTTP router needs.
type FileService interface {
	Enabled() bool
	StartDirectory(candidate string) (string, bool)
	List(ctx context.Context, path string) (files.ListResult, error)
	Read(ctx context.Context, path string) (files.ReadResult, error)
	Write(
		ctx context.Context, path string, body io.Reader, declaredSize int64, expected *files.ExpectedMtime,
	) (files.WriteResult, error)
	Create(ctx context.Context, path string, isDir bool) error
	Rename(ctx context.Context, path, newPath string) error
	Delete(ctx context.Context, path string, recursive bool) error
}

// fileContentType is what every read is served as. The browser classifies a file
// by its extension and reads the bytes through an explicit blob or text call, so
// claiming a type here would add nothing -- and claiming one derived from the
// contents would let a file the user opened be treated as a document.
const fileContentType = "application/octet-stream"

// mapFileError turns the service's error vocabulary into the wire codes the
// browser matches on. The browser needs the code rather than the status: the
// conflict flow in particular has to tell a lost race from any other failure.
func mapFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "invalid_path")
	case errors.Is(err, files.ErrInvalidBody):
		writeError(w, http.StatusBadRequest, "invalid_body")
	case errors.Is(err, files.ErrPathNotAllowed):
		writeError(w, http.StatusForbidden, "path_not_allowed")
	case errors.Is(err, files.ErrPermissionDenied):
		writeError(w, http.StatusForbidden, "permission_denied")
	case errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, files.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, files.ErrDirNotEmpty):
		writeError(w, http.StatusConflict, "dir_not_empty")
	case errors.Is(err, files.ErrFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large")
	default:
		writeError(w, http.StatusInternalServerError, "write_failed")
	}
}

// filesAvailable answers a request against a hub whose file manager is turned
// off, or one whose service was never wired. It answers with the same not-found
// an unknown host gets: the caller learns nothing about paths, and nothing
// touches the filesystem on behalf of a capability that is disabled.
func (s *handlerState) filesAvailable(w http.ResponseWriter) bool {
	if s.files == nil || !s.files.Enabled() {
		writeError(w, http.StatusNotFound, "not_found")
		return false
	}
	return true
}

// decodeFileRequest reads a JSON body under the global request-body limit, which
// is generous for these requests: none of them carries file content.
func (s *handlerState) decodeFileRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (s *handlerState) listFiles(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}
	result, err := s.files.List(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		mapFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *handlerState) readFile(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, false)
}

func (s *handlerState) downloadFile(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, true)
}

// serveFile streams a file. read and download differ only in disposition, but
// they stay separate routes so the browser's intent is legible in the request
// and the download path can be exercised on its own.
func (s *handlerState) serveFile(w http.ResponseWriter, r *http.Request, attachment bool) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}
	path := r.URL.Query().Get("path")
	result, err := s.files.Read(r.Context(), path)
	if err != nil {
		mapFileError(w, err)
		return
	}
	defer func() {
		_ = result.File.Close() //nolint:errcheck // the response has already been written
	}()

	// What this response will actually carry.
	//
	// The service measured the file and checked that measurement against the
	// limit; this reads the same descriptor again, which cannot be a different
	// file. The result can therefore be shorter than what was authorized -- a
	// file truncated after it was checked, which is what a rotating log does --
	// and can never be longer.
	//
	// Reporting the length that is really sent is what keeps Content-Length,
	// X-File-Size, and the body from disagreeing. A response whose headers
	// promise more bytes than it carries is a framing error, and the browser
	// rejects the whole fetch: the user is told the transfer failed, which says
	// nothing about the file. A short body reported as short is simply what the
	// file holds now.
	size := result.Size
	if info, statErr := result.File.Stat(); statErr == nil && info.Size() < size {
		size = info.Size()
	}

	w.Header().Set("Content-Type", fileContentType)
	w.Header().Set("X-File-Size", strconv.FormatInt(size, 10))
	w.Header().Set("X-File-Mtime", strconv.FormatInt(result.Mtime, 10))
	// The exact modification time, as a decimal string rather than a number.
	// Nanoseconds since the epoch exceed what a JavaScript number holds exactly,
	// so a client that parsed it would round it to a time that matches nothing;
	// the browser carries this one back unchanged.
	//
	// It stays the time the service observed even where the body is shorter than
	// what it measured. This header is what the next save is checked against, and
	// a client that observed a file which then changed must be told so rather
	// than handed the time of the change it did not see.
	w.Header().Set("X-File-Mtime-Nanos", strconv.FormatInt(result.MtimeNanos, 10))
	// Whether the contents are text is the hub's answer to give: the browser
	// would otherwise have to guess from the name, and a wrong guess either
	// renders binary bytes as text or refuses to open a file that is text.
	if result.Binary {
		w.Header().Set("X-File-Binary", "1")
	}
	w.Header().Set("Cache-Control", "no-store")
	if attachment {
		w.Header().Set("Content-Disposition",
			"attachment; filename="+strconv.Quote(filepath.Base(path)))
	}

	// ServeContent handles range requests and fills in Content-Length, and it
	// takes the length from the reader it is handed -- so it is handed exactly
	// the bytes that were checked against the limit, and no more even if the file
	// has grown since. Given the descriptor alone it would measure the file
	// again, and a file that grew after the check would be served at its new
	// size: past a limit it had already passed.
	http.ServeContent(w, r, filepath.Base(path), time.Unix(0, result.MtimeNanos),
		io.NewSectionReader(result.File, 0, size))
}

func (s *handlerState) writeFile(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}

	query := r.URL.Query()
	declared, ok := parseRequiredInt64(query.Get("size"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}
	expected, ok := parseExpectedMtime(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_body")
		return
	}

	// This route carries file content, so it gets its own bound rather than the
	// global request limit -- raising that one would let every other endpoint be
	// fed megabytes. The service enforces the same limit independently and never
	// reads past the declared length, so this is a second line rather than the
	// only one.
	if s.cfg.MaxFileSize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxFileSize)
	}
	result, err := s.files.Write(r.Context(), query.Get("path"), r.Body, declared, expected)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			writeError(w, http.StatusRequestEntityTooLarge, "file_too_large")
			return
		}
		mapFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, writeFileResponse{
		Mtime:      result.Mtime,
		MtimeNanos: strconv.FormatInt(result.MtimeNanos, 10),
	})
}

func (s *handlerState) createEntry(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}
	var req createFileRequest
	if !s.decodeFileRequest(w, r, &req) {
		return
	}
	if err := s.files.Create(r.Context(), req.Path, req.Kind == fileKindDirectory); err != nil {
		mapFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *handlerState) renameEntry(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}
	var req renameFileRequest
	if !s.decodeFileRequest(w, r, &req) {
		return
	}
	if err := s.files.Rename(r.Context(), req.Path, req.NewPath); err != nil {
		mapFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *handlerState) deleteEntry(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) || !s.filesAvailable(w) {
		return
	}
	var req deleteFileRequest
	if !s.decodeFileRequest(w, r, &req) {
		return
	}
	if err := s.files.Delete(r.Context(), req.Path, req.Recursive); err != nil {
		mapFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sessionWorkingDirectory answers the directory the file manager should open at.
// It is a navigation seed, never an authorization input: the boundary is
// decided the same way wherever the browser goes afterwards.
//
// When the pane's directory falls outside a configured boundary, the hub answers
// with one that is inside it and flags the substitution. Only the hub can: a
// browser has no way to discover which directories the boundary permits.
func (s *handlerState) sessionWorkingDirectory(w http.ResponseWriter, r *http.Request) {
	if !requireLocalHost(w, r) {
		return
	}
	dir, err := s.sessions.PaneWorkingDirectory(r.Context(), r.PathValue("name"))
	if err != nil {
		mapSessionError(w, err)
		return
	}

	response := workingDirectoryResponse{Path: dir}
	if s.files != nil && s.files.Enabled() {
		response.Path, response.Substituted = s.files.StartDirectory(dir)
	}
	writeJSON(w, http.StatusOK, response)
}

// parseExpectedMtime reads the modification time the caller last observed,
// returning nil for a forced overwrite.
//
// Both precisions are read because a caller is free to report either. The
// millisecond value is what the JSON contract carries; the nanosecond value is
// what actually decides whether the file changed, and it travels as a decimal
// string because it exceeds what a JavaScript number holds exactly. A request
// that names neither forces the overwrite -- that is how the browser asks for
// one -- so an absent pair is not an error, where a malformed one is.
func parseExpectedMtime(query url.Values) (*files.ExpectedMtime, bool) {
	millis, ok := parseOptionalInt64(query.Get("expected_mtime"))
	if !ok {
		return nil, false
	}
	nanos, ok := parseOptionalInt64(query.Get("expected_mtime_nanos"))
	if !ok {
		return nil, false
	}
	if millis == nil && nanos == nil {
		return nil, true
	}
	expected := files.ExpectedMtime{Nanos: nanos}
	if millis != nil {
		expected.Millis = *millis
	}
	return &expected, true
}

// parseRequiredInt64 parses a query parameter the request cannot do without.
func parseRequiredInt64(value string) (int64, bool) {
	parsed, ok := parseOptionalInt64(value)
	if !ok || parsed == nil {
		return 0, false
	}
	return *parsed, true
}

// parseOptionalInt64 distinguishes an absent parameter, which is meaningful for
// expected_mtime, from a malformed one, which is an error either way.
func parseOptionalInt64(value string) (*int64, bool) {
	if value == "" {
		return nil, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil, false
	}
	return &parsed, true
}
