package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// mockFileService stands in for the file service so the transport can be
// exercised without touching a filesystem.
type mockFileService struct {
	disabled bool
	startFn  func(candidate string) (string, bool)
	listFn   func(path string) (files.ListResult, error)
	readFn   func(path string) (files.ReadResult, error)
	writeFn  func(path string, body io.Reader, size int64, mtime *files.ExpectedMtime) (files.WriteResult, error)
	createFn func(path string, isDir bool) error
	renameFn func(path, newPath string) error
	deleteFn func(path string, recursive bool) error
}

func (m *mockFileService) Enabled() bool {
	return !m.disabled
}

func (m *mockFileService) StartDirectory(candidate string) (string, bool) {
	if m.startFn != nil {
		return m.startFn(candidate)
	}
	return candidate, false
}

func (m *mockFileService) List(ctx context.Context, path string) (files.ListResult, error) {
	if m.listFn != nil {
		return m.listFn(path)
	}
	return files.ListResult{Path: path}, nil
}

func (m *mockFileService) Read(ctx context.Context, path string) (files.ReadResult, error) {
	if m.readFn != nil {
		return m.readFn(path)
	}
	return files.ReadResult{}, files.ErrNotFound
}

func (m *mockFileService) Write(
	ctx context.Context, path string, body io.Reader, size int64, mtime *files.ExpectedMtime,
) (files.WriteResult, error) {
	if m.writeFn != nil {
		return m.writeFn(path, body, size, mtime)
	}
	return files.WriteResult{}, nil
}

func (m *mockFileService) Create(ctx context.Context, path string, isDir bool) error {
	if m.createFn != nil {
		return m.createFn(path, isDir)
	}
	return nil
}

func (m *mockFileService) Rename(ctx context.Context, path, newPath string) error {
	if m.renameFn != nil {
		return m.renameFn(path, newPath)
	}
	return nil
}

func (m *mockFileService) Delete(ctx context.Context, path string, recursive bool) error {
	if m.deleteFn != nil {
		return m.deleteFn(path, recursive)
	}
	return nil
}

func authedRequest(t *testing.T, router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func authedGet(t *testing.T, router http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	return authedRequest(t, router, http.MethodGet, target, "")
}

func authedPut(t *testing.T, router http.Handler, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	return authedRequest(t, router, http.MethodPut, target, body)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(target); err != nil {
		t.Fatalf("could not decode the response: %v", err)
	}
}

// openReadResult builds the open file a read is expected to hand back. The
// handler owns closing it, so this helper deliberately does not.
func openReadResult(t *testing.T, content string) files.ReadResult {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "read-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	return files.ReadResult{File: file, Size: info.Size(), Mtime: info.ModTime().UnixMilli()}
}

// fileRoutes is every file-manager route with the request each one needs. The
// working-directory route is here too: it exists only to serve the manager.
func fileRoutes() []struct {
	method string
	target string
	body   string
} {
	return []struct {
		method string
		target string
		body   string
	}{
		{http.MethodGet, "/api/hosts/local/files/list?path=/tmp", ""},
		{http.MethodGet, "/api/hosts/local/files/read?path=/tmp/a", ""},
		{http.MethodGet, "/api/hosts/local/files/download?path=/tmp/a", ""},
		{http.MethodPut, "/api/hosts/local/files/write?path=/tmp/a&size=1", "x"},
		{http.MethodPost, "/api/hosts/local/files/create", `{"path":"/tmp/a","kind":"file"}`},
		{http.MethodPost, "/api/hosts/local/files/rename", `{"path":"/tmp/a","new_path":"/tmp/b"}`},
		{http.MethodPost, "/api/hosts/local/files/delete", `{"path":"/tmp/a"}`},
		{http.MethodGet, "/api/hosts/local/sessions/work/working-directory", ""},
	}
}

// Every file route sits behind the same bearer check as the session routes: an
// unauthenticated caller learns nothing about the filesystem, and no operation
// is attempted on its behalf.
func TestFileRoutesRequireCredentials(t *testing.T) {
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, &mockFileService{},
		&mockTicketIssuer{}, nil)

	for _, route := range fileRoutes() {
		t.Run(route.method+" "+route.target, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.target, strings.NewReader(route.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 without credentials, got %d", rec.Code)
			}
		})
	}
}

func TestMapFileErrorTranslatesTheWholeVocabulary(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid path", files.ErrInvalidPath, http.StatusBadRequest, "invalid_path"},
		{"invalid body", files.ErrInvalidBody, http.StatusBadRequest, "invalid_body"},
		{"path not allowed", files.ErrPathNotAllowed, http.StatusForbidden, "path_not_allowed"},
		{"permission denied", files.ErrPermissionDenied, http.StatusForbidden, "permission_denied"},
		{"not found", files.ErrNotFound, http.StatusNotFound, "not_found"},
		{"conflict", files.ErrConflict, http.StatusConflict, "conflict"},
		{"directory not empty", files.ErrDirNotEmpty, http.StatusConflict, "dir_not_empty"},
		{"file too large", files.ErrFileTooLarge, http.StatusRequestEntityTooLarge, "file_too_large"},
		{"write failed", files.ErrWriteFailed, http.StatusInternalServerError, "write_failed"},
		{"unrecognised", fmt.Errorf("something else"), http.StatusInternalServerError, "write_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapFileError(rec, tc.err)
			if rec.Code != tc.status {
				t.Errorf("expected status %d, got %d", tc.status, rec.Code)
			}
			if body := strings.TrimSpace(rec.Body.String()); body != `{"error":"`+tc.code+`"}` {
				t.Errorf("expected the %s envelope, got %s", tc.code, body)
			}
		})
	}
}

// The browser matches on the code, so a sentinel wrapped by the service still
// has to reach it.
func TestMapFileErrorSeesThroughWrapping(t *testing.T) {
	rec := httptest.NewRecorder()
	mapFileError(rec, fmt.Errorf("write %s: %w", "/tmp/a", files.ErrConflict))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "conflict") {
		t.Errorf("expected a wrapped conflict to survive, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestFileRoutesAnswerADisabledManagerWithoutReachingTheService(t *testing.T) {
	svc := &mockFileService{disabled: true, listFn: func(string) (files.ListResult, error) {
		t.Error("a disabled manager must not reach the service")
		return files.ListResult{}, nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/files/list?path=/tmp")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected a disabled manager to answer 404, got %d", rec.Code)
	}
}

func TestListRouteReturnsEntriesAndTruncation(t *testing.T) {
	svc := &mockFileService{listFn: func(path string) (files.ListResult, error) {
		if path != "/tmp/dir" {
			t.Errorf("the service saw path %q", path)
		}
		return files.ListResult{
			Path:      path,
			Entries:   []files.Entry{{Name: "sub", IsDir: true}, {Name: "a.txt", Size: 3, Mtime: 5}},
			Truncated: true,
		}, nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/files/list?path=/tmp/dir")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got files.ListResult
	decodeBody(t, rec, &got)
	if len(got.Entries) != 2 || !got.Entries[0].IsDir || !got.Truncated {
		t.Errorf("the listing did not survive the round trip: %+v", got)
	}
}

// read and download are the same bytes with different intent, and both carry the
// metadata the browser needs to decide how to present the file.
func TestReadAndDownloadSetTheirHeaders(t *testing.T) {
	const content = "file contents"
	svc := &mockFileService{readFn: func(string) (files.ReadResult, error) {
		return openReadResult(t, content), nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	tests := []struct {
		target     string
		attachment bool
	}{
		{"/api/hosts/local/files/read?path=/tmp/a.txt", false},
		{"/api/hosts/local/files/download?path=/tmp/a.txt", true},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			rec := authedGet(t, router, tc.target)
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(content)) {
				t.Errorf("expected Content-Length %d, got %q", len(content), got)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("expected no-store, got %q", rec.Header().Get("Cache-Control"))
			}
			if rec.Header().Get("X-File-Mtime") == "" {
				t.Error("expected an X-File-Mtime header")
			}
			if got := rec.Header().Get("X-File-Size"); got != strconv.Itoa(len(content)) {
				t.Errorf("expected X-File-Size %d, got %q", len(content), got)
			}
			hasDisposition := rec.Header().Get("Content-Disposition") != ""
			if hasDisposition != tc.attachment {
				t.Errorf("Content-Disposition present=%v, want %v", hasDisposition, tc.attachment)
			}
			if rec.Body.String() != content {
				t.Errorf("expected the exact bytes back, got %q", rec.Body.String())
			}
		})
	}
}

// The browser decides whether it may show a file as editable text from this
// header rather than from the file's name, so it is present exactly when the hub
// classified the contents as binary.
func TestReadReportsWhetherTheContentsAreBinary(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(strconv.FormatBool(binary), func(t *testing.T) {
			svc := &mockFileService{readFn: func(string) (files.ReadResult, error) {
				result := openReadResult(t, "contents")
				result.Binary = binary
				return result, nil
			}}
			router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc,
				&mockTicketIssuer{}, nil)

			rec := authedGet(t, router, "/api/hosts/local/files/read?path=/tmp/a.bin")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			want := ""
			if binary {
				want = "1"
			}
			if got := rec.Header().Get("X-File-Binary"); got != want {
				t.Errorf("X-File-Binary = %q, want %q", got, want)
			}
		})
	}
}

// The write route carries file content, so its body bound comes from the file
// limit rather than the 64 KiB limit every other route uses.
func TestWriteRouteBoundsTheBodyByTheFileLimit(t *testing.T) {
	const limit = 16
	write := func(_ string, body io.Reader, _ int64, _ *files.ExpectedMtime) (files.WriteResult, error) {
		if _, err := io.ReadAll(body); err != nil {
			return files.WriteResult{}, err
		}
		return files.WriteResult{Mtime: 4242, MtimeNanos: 4242000000}, nil
	}
	svc := &mockFileService{writeFn: write}

	cfg := testRouterConfig("tok", nil)
	cfg.MaxFileSize = limit
	router := NewRouter(cfg, &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	over := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=16",
		strings.Repeat("x", limit+1))
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected a body over the limit to be refused, got %d", over.Code)
	}
	if !strings.Contains(over.Body.String(), "file_too_large") {
		t.Errorf("expected the file_too_large code, got %s", over.Body.String())
	}

	under := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=16",
		strings.Repeat("x", limit))
	if under.Code != http.StatusOK {
		t.Fatalf("expected a body at the limit to succeed, got %d", under.Code)
	}
	var got writeFileResponse
	decodeBody(t, under, &got)
	if got.Mtime != 4242 {
		t.Errorf("expected the new modification time back, got %d", got.Mtime)
	}
	// The nanosecond value travels as a string, because as a JSON number it
	// would be rounded by the client's own arithmetic before it was ever
	// compared against a file.
	if got.MtimeNanos != "4242000000" {
		t.Errorf("expected the exact modification time back as a string, got %q", got.MtimeNanos)
	}
}

func TestWriteRouteRequiresAWellFormedDeclaredSize(t *testing.T) {
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, &mockFileService{},
		&mockTicketIssuer{}, nil)

	absent := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a", "body")
	if absent.Code != http.StatusBadRequest || !strings.Contains(absent.Body.String(), "invalid_body") {
		t.Errorf("expected invalid_body without a size, got %d %s", absent.Code, absent.Body.String())
	}

	malformed := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=abc", "body")
	if malformed.Code != http.StatusBadRequest {
		t.Errorf("expected a malformed size to be refused, got %d", malformed.Code)
	}

	badMtime := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=4&expected_mtime=x", "body")
	if badMtime.Code != http.StatusBadRequest {
		t.Errorf("expected a malformed expected_mtime to be refused, got %d", badMtime.Code)
	}
}

// expected_mtime is optional, and its absence is what asks for a forced
// overwrite, so it has to reach the service as nil rather than as zero.
func TestWriteRoutePassesAnAbsentExpectedMtimeThrough(t *testing.T) {
	var seen *files.ExpectedMtime
	write := func(_ string, _ io.Reader, _ int64, mtime *files.ExpectedMtime) (files.WriteResult, error) {
		seen = mtime
		return files.WriteResult{}, nil
	}
	svc := &mockFileService{writeFn: write}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	if rec := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=1", "x"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen != nil {
		t.Errorf("expected no observed mtime, got %d", seen.Millis)
	}

	if rec := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=1&expected_mtime=7",
		"x"); rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen == nil || seen.Millis != 7 {
		t.Errorf("expected the observed mtime 7 to arrive, got %v", seen)
	}
}

// The exact modification time is what a save is actually checked against, and
// it travels separately from the millisecond value because the two have
// different jobs: one is displayable, the other is comparable.
func TestWriteRouteCarriesTheExactModificationTime(t *testing.T) {
	var seen *files.ExpectedMtime
	write := func(_ string, _ io.Reader, _ int64, mtime *files.ExpectedMtime) (files.WriteResult, error) {
		seen = mtime
		return files.WriteResult{}, nil
	}
	svc := &mockFileService{writeFn: write}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	// A value well past JavaScript's safe integer range, which is the whole
	// reason this one is not a JSON number.
	rec := authedPut(t, router,
		"/api/hosts/local/files/write?path=/tmp/a&size=1&expected_mtime=1760000000123"+
			"&expected_mtime_nanos=1760000000123456789", "x")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if seen == nil || seen.Nanos == nil {
		t.Fatalf("expected an exact modification time, got %v", seen)
	}
	if *seen.Nanos != 1760000000123456789 {
		t.Errorf("expected the exact modification time through unchanged, got %d", *seen.Nanos)
	}
	if seen.Millis != 1760000000123 {
		t.Errorf("expected the millisecond value alongside it, got %d", seen.Millis)
	}

	malformed := authedPut(t, router,
		"/api/hosts/local/files/write?path=/tmp/a&size=1&expected_mtime_nanos=abc", "x")
	if malformed.Code != http.StatusBadRequest {
		t.Errorf("expected a malformed exact time to be refused, got %d", malformed.Code)
	}
}

func TestFileOperationRoutesCarryTheirBodies(t *testing.T) {
	var created struct {
		path  string
		isDir bool
	}
	var renamed struct{ from, to string }
	var deleted struct {
		path      string
		recursive bool
	}

	svc := &mockFileService{
		createFn: func(path string, isDir bool) error {
			created.path, created.isDir = path, isDir
			return nil
		},
		renameFn: func(path, newPath string) error {
			renamed.from, renamed.to = path, newPath
			return nil
		},
		deleteFn: func(path string, recursive bool) error {
			deleted.path, deleted.recursive = path, recursive
			return nil
		},
	}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	if rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/create",
		`{"path":"/tmp/new","kind":"dir"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("create: expected 204, got %d", rec.Code)
	}
	if created.path != "/tmp/new" || !created.isDir {
		t.Errorf("create decoded %+v", created)
	}

	if rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/rename",
		`{"path":"/tmp/a","new_path":"/tmp/b"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("rename: expected 204, got %d", rec.Code)
	}
	if renamed.from != "/tmp/a" || renamed.to != "/tmp/b" {
		t.Errorf("rename decoded %+v", renamed)
	}

	if rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/delete",
		`{"path":"/tmp/a","recursive":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", rec.Code)
	}
	if deleted.path != "/tmp/a" || !deleted.recursive {
		t.Errorf("delete decoded %+v", deleted)
	}
}

func TestWorkingDirectoryRouteAnswersAndReportsAnAbsentSession(t *testing.T) {
	svc := &mockSessionService{
		sessions: []session.Session{{Name: "work"}},
		paneDir:  "/home/user/project",
	}
	router := NewRouter(testRouterConfig("tok", nil), svc, &mockFileService{}, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/sessions/work/working-directory")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var got workingDirectoryResponse
	decodeBody(t, rec, &got)
	if got.Path != "/home/user/project" {
		t.Errorf("expected the pane directory, got %q", got.Path)
	}

	absent := authedGet(t, router, "/api/hosts/local/sessions/absent/working-directory")
	if absent.Code != http.StatusNotFound {
		t.Errorf("expected an absent session to map to 404, got %d", absent.Code)
	}
}

// The seed the browser opens at has to be usable: when the session's directory
// is outside the boundary, the hub answers with one that is and says so, because
// the browser has no way to work that out for itself.
func TestWorkingDirectoryRouteFlagsASubstitutedDirectory(t *testing.T) {
	sessions := &mockSessionService{
		sessions: []session.Session{{Name: "work"}},
		paneDir:  "/home/user/project",
	}

	tests := []struct {
		name            string
		files           *mockFileService
		wantPath        string
		wantSubstituted bool
	}{
		{"permitted stays where the pane is", &mockFileService{}, "/home/user/project", false},
		{"outside the boundary is replaced", &mockFileService{
			startFn: func(string) (string, bool) { return "/srv/projects", true },
		}, "/srv/projects", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := NewRouter(testRouterConfig("tok", nil), sessions, tc.files, &mockTicketIssuer{}, nil)

			rec := authedGet(t, router, "/api/hosts/local/sessions/work/working-directory")
			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			var got workingDirectoryResponse
			decodeBody(t, rec, &got)
			if got.Path != tc.wantPath || got.Substituted != tc.wantSubstituted {
				t.Errorf("expected (%q, %v), got (%q, %v)",
					tc.wantPath, tc.wantSubstituted, got.Path, got.Substituted)
			}
		})
	}
}

// A hub with the file manager turned off answers exactly as it did before the
// substitution existed, and never consults the service to do it.
func TestWorkingDirectoryIsNotSubstitutedWhenTheManagerIsOff(t *testing.T) {
	sessions := &mockSessionService{
		sessions: []session.Session{{Name: "work"}},
		paneDir:  "/home/user/project",
	}
	files := &mockFileService{disabled: true, startFn: func(string) (string, bool) {
		t.Error("a disabled manager must not be consulted")
		return "", false
	}}

	router := NewRouter(testRouterConfig("tok", nil), sessions, files, &mockTicketIssuer{}, nil)
	rec := authedGet(t, router, "/api/hosts/local/sessions/work/working-directory")

	var got workingDirectoryResponse
	decodeBody(t, rec, &got)
	if got.Path != "/home/user/project" || got.Substituted {
		t.Errorf("a disabled manager changed the answer: %+v", got)
	}
}

func TestFileRoutesRejectANonLocalHost(t *testing.T) {
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, &mockFileService{},
		&mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/remote/files/list?path=/tmp")
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected a non-local host to map to 404, got %d", rec.Code)
	}
}

// The length that was checked against the limit is the length that is served.
//
// The bug this pins: the service measured the descriptor and the transport then
// measured the file again, so a file that grew in between was sent at its new
// size -- past a limit that had already passed, with X-File-Size describing a
// length the body did not have.
func TestReadRouteServesExactlyTheLengthItAuthorized(t *testing.T) {
	result := openReadResult(t, "0123456789")
	// The descriptor holds ten bytes; four is what was authorized.
	result.Size = 4
	svc := &mockFileService{readFn: func(string) (files.ReadResult, error) {
		return result, nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/files/read?path=/tmp/grew.log")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "0123" {
		t.Errorf("expected the authorized bytes alone, got %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "4" {
		t.Errorf("expected Content-Length 4, got %q", got)
	}
	if got := rec.Header().Get("X-File-Size"); got != "4" {
		t.Errorf("expected X-File-Size 4, got %q", got)
	}
}

// The exact modification time is exposed for the client to carry back, and it is
// a decimal string because the number would not survive the client's arithmetic.
func TestReadRouteExposesTheExactModificationTime(t *testing.T) {
	result := openReadResult(t, "contents")
	result.Mtime = 1760000000123
	result.MtimeNanos = 1760000000123456789
	svc := &mockFileService{readFn: func(string) (files.ReadResult, error) {
		return result, nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/files/read?path=/tmp/a.txt")
	if got := rec.Header().Get("X-File-Mtime"); got != "1760000000123" {
		t.Errorf("expected the millisecond value, got %q", got)
	}
	if got := rec.Header().Get("X-File-Mtime-Nanos"); got != "1760000000123456789" {
		t.Errorf("expected the exact value as a string, got %q", got)
	}
}
