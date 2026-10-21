package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/files"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/host"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/session"
)

// mockFileService stands in for the file service so the transport can be
// exercised without touching a filesystem.
type mockFileService struct {
	disabled bool
	startFn  func(candidate string) (string, bool)
	listFn   func(path string) (files.ListResult, error)
	readFn   func(path string) (files.ReadResult, error)
	writeFn  func(
		path string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
	) (files.WriteResult, error)
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
	ctx context.Context, path string, body io.Reader, size int64,
	mtime *files.ExpectedMtime, allowOtherNames bool,
) (files.WriteResult, error) {
	if m.writeFn != nil {
		return m.writeFn(path, body, size, mtime, allowOtherNames)
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
		{"cross-root move", files.ErrCrossRoot, http.StatusForbidden, "cross_root_move"},
		{"permission denied", files.ErrPermissionDenied, http.StatusForbidden, "permission_denied"},
		{"not found", files.ErrNotFound, http.StatusNotFound, "not_found"},
		{"conflict", files.ErrConflict, http.StatusConflict, "conflict"},
		{"directory not empty", files.ErrDirNotEmpty, http.StatusConflict, "dir_not_empty"},
		{"target has other names", files.ErrHasOtherNames, http.StatusConflict, "other_names"},
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

// A disabled manager is answered without the service being consulted, on every
// route: the capability is off, so nothing looks at a path and nothing is
// attempted on the caller's behalf. Every route, rather than one of them,
// because this gate is what makes it safe to build a disabled service with no
// boundary at all -- see the composition root's newFileService. A route that
// lost the check would be an unrestricted file manager wearing a disabled one's
// configuration.
func TestFileRoutesAnswerADisabledManagerWithoutReachingTheService(t *testing.T) {
	touched := ""
	mark := func(what string) { touched = what }
	svc := &mockFileService{
		disabled: true,
		listFn: func(string) (files.ListResult, error) {
			mark("list")
			return files.ListResult{}, nil
		},
		readFn: func(string) (files.ReadResult, error) {
			mark("read")
			return files.ReadResult{}, nil
		},
		writeFn: func(string, io.Reader, int64, *files.ExpectedMtime, bool) (files.WriteResult, error) {
			mark("write")
			return files.WriteResult{}, nil
		},
		createFn: func(string, bool) error { mark("create"); return nil },
		renameFn: func(string, string) error { mark("rename"); return nil },
		deleteFn: func(string, bool) error { mark("delete"); return nil },
		startFn:  func(string) (string, bool) { mark("start directory"); return "", false },
	}
	sessions := &mockSessionService{
		sessions: []session.Session{{Name: "work"}},
		paneDir:  "/home/user/project",
	}
	router := NewRouter(testRouterConfig("tok", nil), sessions, svc, &mockTicketIssuer{}, nil)

	for _, route := range fileRoutes() {
		// The working-directory route is not refused and is not meant to be: it
		// reports the session's own directory, and asks the file manager only to
		// substitute a permitted one when a boundary exists, so a disabled manager
		// changes nothing about its answer (see
		// TestWorkingDirectoryRouteFlagsASubstitutedDirectory). What it shares with
		// the rest is the part under test here -- that the disabled manager is not
		// consulted -- so it stays in the loop for that and is not asserted to 404.
		refused := strings.Contains(route.target, "/files/")
		t.Run(route.method+" "+route.target, func(t *testing.T) {
			touched = ""
			rec := authedRequest(t, router, route.method, route.target, route.body)
			if refused && rec.Code != http.StatusNotFound {
				t.Errorf("expected a disabled manager to answer 404, got %d", rec.Code)
			}
			if touched != "" {
				t.Errorf("a disabled manager reached the service: %s", touched)
			}
		})
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
	write := func(_ string, body io.Reader, _ int64, _ *files.ExpectedMtime, _ bool) (files.WriteResult, error) {
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
	write := func(_ string, _ io.Reader, _ int64, mtime *files.ExpectedMtime, _ bool) (files.WriteResult, error) {
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
	write := func(_ string, _ io.Reader, _ int64, mtime *files.ExpectedMtime, _ bool) (files.WriteResult, error) {
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

// Replacing a file replaces the inode, so a target reachable under other names
// loses them: the caller has to agree to that, and this is where the agreement
// travels. A value that is neither of the two the flag may carry is refused
// rather than read as one of them -- a flag with three meanings is three
// callers disagreeing about the other two.
func TestWriteRouteCarriesTheAgreementAboutOtherNames(t *testing.T) {
	var seen []bool
	svc := &mockFileService{
		writeFn: func(
			_ string, body io.Reader, _ int64, _ *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			if _, err := io.ReadAll(body); err != nil {
				return files.WriteResult{}, err
			}
			seen = append(seen, allowOtherNames)
			return files.WriteResult{}, nil
		},
	}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	for _, tc := range []struct {
		query string
		want  bool
	}{
		{"", false},
		{"&allow_other_names=0", false},
		{"&allow_other_names=1", true},
	} {
		rec := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=1"+tc.query, "x")
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: expected 200, got %d", tc.query, rec.Code)
		}
		if got := seen[len(seen)-1]; got != tc.want {
			t.Errorf("%q: the service was told allowOtherNames=%v", tc.query, got)
		}
	}

	// An empty value is the absent flag, which is how every other optional
	// parameter on these routes reads it; anything that is not one of the two
	// values is refused.
	for _, bad := range []string{"true", "2", "-1", "yes"} {
		rec := authedPut(t, router, "/api/hosts/local/files/write?path=/tmp/a&size=1&allow_other_names="+bad, "x")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: expected a refusal, got %d", bad, rec.Code)
		}
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

// The create route carried out whatever the body said, including a body that did
// not say what to create: `kind` was compared against the name for a directory
// and everything else -- absent, misspelled, from another version -- created a
// file. A caller that asked for a directory and was handed a file at that path,
// with a success status, has been told something untrue about its own filesystem.
func TestCreateRouteRequiresAKindItKnows(t *testing.T) {
	created := 0
	var lastDir bool
	svc := &mockFileService{
		createFn: func(_ string, isDir bool) error {
			created++
			lastDir = isDir
			return nil
		},
	}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	for _, body := range []string{
		`{"path":"/tmp/x","kind":"directory"}`,
		`{"path":"/tmp/x","kind":"DIR"}`,
		`{"path":"/tmp/x","kind":""}`,
		`{"path":"/tmp/x"}`,
		`{}`,
	} {
		rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/create", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected a refusal, got %d", body, rec.Code)
		}
	}
	if created != 0 {
		t.Errorf("expected nothing to be created, got %d creations", created)
	}

	// Both kinds it does know still work, so the refusal above is about the value
	// and not about the field.
	for body, wantDir := range map[string]bool{
		`{"path":"/tmp/x","kind":"file"}`: false,
		`{"path":"/tmp/x","kind":"dir"}`:  true,
	} {
		rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/create", body)
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s: expected 204, got %d", body, rec.Code)
		}
		if lastDir != wantDir {
			t.Errorf("%s: expected isDir=%v, got %v", body, wantDir, lastDir)
		}
	}
}

// Every file request is one JSON object, and the routes act on what it says. A
// field this hub does not know, a second object after the first, and no body at
// all are refused rather than read for the parts that are understood: silently
// ignoring what it does not recognise is how a create, a move, or a delete turns
// into an action the caller did not ask for.
func TestFileRequestBodiesAreOneKnownObject(t *testing.T) {
	svc := &mockFileService{
		createFn: func(string, bool) error { return nil },
		renameFn: func(string, string) error { return nil },
		deleteFn: func(string, bool) error { return nil },
	}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	for _, route := range []string{"create", "rename", "delete"} {
		for _, body := range []string{
			``,
			`{"path":"/tmp/x","kind":"file","extra":1}`,
			`{"path":"/tmp/x","kind":"file"}{"path":"/tmp/y","kind":"file"}`,
			`{"path":"/tmp/x","kind":"file"} trailing`,
			`not json`,
		} {
			rec := authedRequest(t, router, http.MethodPost, "/api/hosts/local/files/"+route, body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s %s: expected a refusal, got %d", route, body, rec.Code)
			}
		}
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

// A file truncated between the size check and the transfer is served as what it
// holds now, reported at that length.
//
// The bug this pins: the section handed to ServeContent was bounded by the
// authorized length alone. A shortened file then produced a response whose
// Content-Length promised ten bytes and whose body carried four -- a framing
// error the browser reports as a failed transfer, which says nothing about the
// file. A short body reported as short is simply what the file holds now.
func TestReadRouteServesAShortenedFileAtItsNewLength(t *testing.T) {
	result := openReadResult(t, "0123456789")
	// The file loses six bytes after the service measured it.
	if err := result.File.Truncate(4); err != nil {
		t.Fatal(err)
	}
	svc := &mockFileService{readFn: func(string) (files.ReadResult, error) {
		return result, nil
	}}
	router := NewRouter(testRouterConfig("tok", nil), &mockSessionService{}, svc, &mockTicketIssuer{}, nil)

	rec := authedGet(t, router, "/api/hosts/local/files/read?path=/tmp/shrunk.log")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "0123" {
		t.Errorf("expected what the file holds now, got %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "4" {
		t.Errorf("expected Content-Length 4, got %q", got)
	}
	if got := rec.Header().Get("X-File-Size"); got != "4" {
		t.Errorf("expected X-File-Size 4, got %q", got)
	}
}

type mockResolver struct {
	resolveFn func(ctx context.Context, id host.ID) (host.Runtime, error)
	calls     []host.ID
}

func (m *mockResolver) Resolve(ctx context.Context, id host.ID) (host.Runtime, error) {
	m.calls = append(m.calls, id)
	if m.resolveFn != nil {
		return m.resolveFn(ctx, id)
	}
	return host.Runtime{}, host.ErrNotFound
}

func TestHostScopedRemoteFileRoutesAndGlobalDisablement(t *testing.T) {
	remoteFiles := &mockFileService{
		listFn: func(p string) (files.ListResult, error) {
			return files.ListResult{Path: p}, nil
		},
		readFn: func(p string) (files.ReadResult, error) {
			return openReadResult(t, "remote content"), nil
		},
		writeFn: func(
			p string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			if strings.Contains(p, "uncertain") {
				return files.WriteResult{}, &host.Error{Target: host.Target{HostID: "r1"}, Code: host.ErrOutcomeUnknown}
			}
			if strings.Contains(p, "offline") {
				return files.WriteResult{}, &host.Error{Target: host.Target{HostID: "r1"}, Code: host.ErrUnavailable}
			}
			return files.WriteResult{Mtime: 123456, MtimeNanos: 123456000000}, nil
		},
		createFn: func(p string, isDir bool) error { return nil },
		renameFn: func(p, np string) error { return nil },
		deleteFn: func(p string, rec bool) error { return nil },
	}

	remoteHostID := host.ID("remote-box")
	resolver := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			if id == remoteHostID {
				return host.Runtime{
					Target:   host.Target{HostID: remoteHostID, Revision: 1},
					Sessions: &mockSessionService{},
					Files:    remoteFiles,
				}, nil
			}
			return host.Runtime{}, host.ErrNotFound
		},
	}

	cfg := testRouterConfig("tok", nil)
	cfg.FilesEnabled = true
	router := NewRouterWithResolver(cfg, resolver, &mockTicketIssuer{}, nil)

	// 1. Verify remote file operations route to remoteFiles
	rec := authedGet(t, router, "/api/hosts/remote-box/files/list?path=/home/user")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for remote list, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = authedGet(t, router, "/api/hosts/remote-box/files/read?path=/home/user/file.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for remote read, got %d: %s", rec.Code, rec.Body.String())
	}

	writeURL := "/api/hosts/remote-box/files/write?path=/home/user/file.txt&size=4&allow_other_names=1"
	rec = authedPut(t, router, writeURL, "data")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for remote write, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Verify typed host errors mapped accurately
	// ErrOutcomeUnknown -> 502 Bad Gateway
	uncertainURL := "/api/hosts/remote-box/files/write?path=/home/user/uncertain.txt&size=4&allow_other_names=1"
	rec = authedPut(t, router, uncertainURL, "data")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for outcome_unknown, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "outcome_unknown") {
		t.Fatalf("expected outcome_unknown error envelope, got: %s", rec.Body.String())
	}

	// ErrUnavailable -> 503 Service Unavailable
	offlineURL := "/api/hosts/remote-box/files/write?path=/home/user/offline.txt&size=4&allow_other_names=1"
	rec = authedPut(t, router, offlineURL, "data")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for host_unavailable, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown host -> 404 host_not_found
	rec = authedGet(t, router, "/api/hosts/nonexistent/files/list?path=/home")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown host, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "host_not_found") {
		t.Fatalf("expected host_not_found error envelope, got: %s", rec.Body.String())
	}

	// 3. Global Disablement: FilesEnabled = false
	disabledCfg := testRouterConfig("tok", nil)
	disabledCfg.FilesEnabled = false

	noTouchResolver := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			t.Fatalf("Resolver.Resolve MUST NOT be called when files are globally disabled, called with id: %s", id)
			return host.Runtime{}, nil
		},
	}
	disabledRouter := NewRouterWithResolver(disabledCfg, noTouchResolver, &mockTicketIssuer{}, nil)

	routesToTest := []struct {
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
		{http.MethodGet, "/api/hosts/local/sessions/sess1/working-directory", ""},
		{http.MethodGet, "/api/hosts/remote-box/files/list?path=/remote", ""},
		{http.MethodGet, "/api/hosts/remote-box/files/read?path=/remote/a", ""},
		{http.MethodGet, "/api/hosts/remote-box/files/download?path=/remote/a", ""},
		{http.MethodPut, "/api/hosts/remote-box/files/write?path=/remote/a&size=1", "x"},
		{http.MethodPost, "/api/hosts/remote-box/files/create", `{"path":"/remote/a","kind":"file"}`},
		{http.MethodPost, "/api/hosts/remote-box/files/rename", `{"path":"/remote/a","new_path":"/remote/b"}`},
		{http.MethodPost, "/api/hosts/remote-box/files/delete", `{"path":"/remote/a"}`},
		{http.MethodGet, "/api/hosts/remote-box/sessions/sess1/working-directory", ""},
	}

	for _, route := range routesToTest {
		t.Run(route.method+"_"+route.target, func(t *testing.T) {
			resp := authedRequest(t, disabledRouter, route.method, route.target, route.body)
			if resp.Code != http.StatusNotFound {
				t.Fatalf("expected 404 for disabled files on %s %s, got %d: %s",
					route.method, route.target, resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "not_found") {
				t.Fatalf("expected not_found error envelope, got: %s", resp.Body.String())
			}
		})
	}

	// Verify no calls reached the resolver
	if len(noTouchResolver.calls) != 0 {
		t.Fatalf("expected 0 resolver calls when files are globally disabled, got: %v", noTouchResolver.calls)
	}
}

func TestFilesWriteExpectedConnectionRevisionConflict(t *testing.T) {
	fs := &mockFileService{}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Files:  fs,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// Matching revision succeeds
	matchURL := "/api/hosts/remote-box/files/write?path=/work/a.txt&size=4&expected_connection_revision=3"
	rec1 := authedRequest(t, router, http.MethodPut, matchURL, "data")
	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 for matching connection revision, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// Mismatched revision returns 409 Conflict
	mismatchURL := "/api/hosts/remote-box/files/write?path=/work/a.txt&size=4&expected_connection_revision=2"
	rec2 := authedRequest(t, router, http.MethodPut, mismatchURL, "data")
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected 409 for mismatched connection revision, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "host_revision_conflict") {
		t.Fatalf("expected host_revision_conflict in error response, got: %s", rec2.Body.String())
	}
}

func TestFilesWriteBodyCancellation(t *testing.T) {
	pipeR, pipeW := io.Pipe()

	fs := &mockFileService{
		writeFn: func(
			path string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			buf := make([]byte, 10)
			_, err := io.ReadFull(body, buf)
			if err != nil {
				return files.WriteResult{}, err
			}
			// Wait for cancel
			_, err = io.ReadAll(body)
			return files.WriteResult{}, err
		},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 1, ConnectionRevision: 1},
				Files:  fs,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	writeURL := "/api/hosts/remote-box/files/write?path=/work/a.txt&size=100"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, writeURL, pipeR)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer tok")

	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		router.ServeHTTP(rec, req)
		close(done)
	}()

	// Send partial 10 bytes then stop
	_, _ = pipeW.Write([]byte("0123456789")) //nolint:errcheck // partial test body write

	// Cancel context to simulate timeout / client abort
	cancel()

	select {
	case <-done:
		// Succeeded in returning promptly upon cancellation
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler hung ignoring body read cancellation")
	}
	_ = pipeW.Close() //nolint:errcheck // test cleanup
}

func TestFilesMutateExpectedConnectionRevisionConflict(t *testing.T) {
	fs := &mockFileService{}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 5, ConnectionRevision: 3},
				Files:  fs,
			}, nil
		},
	}
	router := NewRouterWithResolver(testRouterConfig("tok", nil), r, &mockTicketIssuer{}, nil)

	// 1. Create entry
	matchCreate := `{"path":"/work/file.txt","kind":"file","expected_connection_revision":3}`
	rec := authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/create", matchCreate)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for matching create, got %d: %s", rec.Code, rec.Body.String())
	}

	mismatchCreate := `{"path":"/work/file.txt","kind":"file","expected_connection_revision":2}`
	rec = authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/create", mismatchCreate)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "host_revision_conflict") {
		t.Fatalf("expected 409 host_revision_conflict for mismatched create, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Rename entry
	matchRename := `{"path":"/work/file.txt","new_path":"/work/file2.txt","expected_connection_revision":3}`
	rec = authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/rename", matchRename)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for matching rename, got %d: %s", rec.Code, rec.Body.String())
	}

	mismatchRename := `{"path":"/work/file.txt","new_path":"/work/file2.txt","expected_connection_revision":2}`
	rec = authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/rename", mismatchRename)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "host_revision_conflict") {
		t.Fatalf("expected 409 host_revision_conflict for mismatched rename, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Delete entry
	matchDelete := `{"path":"/work/file2.txt","recursive":false,"expected_connection_revision":3}`
	rec = authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/delete", matchDelete)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for matching delete, got %d: %s", rec.Code, rec.Body.String())
	}

	mismatchDelete := `{"path":"/work/file2.txt","recursive":false,"expected_connection_revision":2}`
	rec = authedRequest(t, router, http.MethodPost, "/api/hosts/remote-box/files/delete", mismatchDelete)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "host_revision_conflict") {
		t.Fatalf("expected 409 host_revision_conflict for mismatched delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFilesWriteSlowBodyTimeout(t *testing.T) {
	bodyReadBlocked := make(chan struct{})
	unblocked := make(chan struct{})

	fs := &mockFileService{
		writeFn: func(
			path string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			buf := make([]byte, 10)
			_, err := io.ReadFull(body, buf)
			if err != nil {
				return files.WriteResult{}, err
			}
			close(bodyReadBlocked)
			// Read remaining body (blocks until SetReadDeadline interrupts it)
			_, err = io.ReadAll(body)
			close(unblocked)
			return files.WriteResult{}, err
		},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 1, ConnectionRevision: 1},
				Files:  fs,
			}, nil
		},
	}

	cfg := testRouterConfig("tok", nil)
	cfg.FileOperationTimeout = 100 * time.Millisecond
	router := NewRouterWithResolver(cfg, r, &mockTicketIssuer{}, nil)

	server := httptest.NewServer(router)
	defer server.Close()

	// Connect with real TCP socket
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() //nolint:errcheck // cleanup

	reqHeaders := "PUT /api/hosts/remote-box/files/write?path=/work/slow.txt&size=1000 HTTP/1.1\r\n" +
		"Host: " + server.Listener.Addr().String() + "\r\n" +
		"Authorization: Bearer tok\r\n" +
		"Content-Length: 1000\r\n" +
		"Connection: close\r\n\r\n" +
		"0123456789" // only 10 of 1000 bytes sent!

	if _, err := conn.Write([]byte(reqHeaders)); err != nil {
		t.Fatal(err)
	}

	// Wait until handler reads the first 10 bytes and starts blocking on the rest
	select {
	case <-bodyReadBlocked:
	case <-time.After(time.Second):
		t.Fatal("handler did not start reading body")
	}

	// Now wait for FileOperationTimeout (100ms) to trigger SetReadDeadline and unblock io.ReadAll
	select {
	case <-unblocked:
		// Succeeded in unblocking!
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handler hung on stalled TCP body read exceeding FileOperationTimeout")
	}
}

type spyDeadlineWriter struct {
	http.ResponseWriter
	mu            sync.Mutex
	deadlineCalls int
}

func (s *spyDeadlineWriter) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deadlineCalls++
	return nil
}

func (s *spyDeadlineWriter) getCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadlineCalls
}

func TestFilesWriteWatcherNeverUsesWriterAfterReturn(t *testing.T) {
	fs := &mockFileService{
		writeFn: func(
			path string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			_, err := io.Copy(io.Discard, body)
			return files.WriteResult{Mtime: 123456, MtimeNanos: 123456000}, err
		},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 1, ConnectionRevision: 1},
				Files:  fs,
			}, nil
		},
	}

	cfg := testRouterConfig("tok", nil)
	router := NewRouterWithResolver(cfg, r, &mockTicketIssuer{}, nil)

	for i := range 100 {
		rec := httptest.NewRecorder()
		spy := &spyDeadlineWriter{ResponseWriter: rec}
		req := httptest.NewRequest(
			http.MethodPut,
			"/api/hosts/remote-box/files/write?path=/work/f.txt&size=5",
			strings.NewReader("hello"),
		)
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/octet-stream")

		router.ServeHTTP(spy, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("run %d: status = %d, want 200", i, rec.Code)
		}

		time.Sleep(time.Millisecond)

		if calls := spy.getCalls(); calls != 0 {
			t.Fatalf("run %d: SetReadDeadline was invoked %d times on normal write completion", i, calls)
		}
	}
}

func TestFilesWriteKeepAliveSequentialRequests(t *testing.T) {
	fs := &mockFileService{
		writeFn: func(
			path string, body io.Reader, size int64, mtime *files.ExpectedMtime, allowOtherNames bool,
		) (files.WriteResult, error) {
			_, err := io.Copy(io.Discard, body)
			return files.WriteResult{Mtime: 123456, MtimeNanos: 123456000}, err
		},
	}
	r := &mockResolver{
		resolveFn: func(ctx context.Context, id host.ID) (host.Runtime, error) {
			return host.Runtime{
				Target: host.Target{HostID: "remote-box", Revision: 1, ConnectionRevision: 1},
				Files:  fs,
			}, nil
		},
	}

	cfg := testRouterConfig("tok", nil)
	router := NewRouterWithResolver(cfg, r, &mockTicketIssuer{}, nil)
	server := httptest.NewServer(router)
	defer server.Close()

	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives:   false,
			MaxIdleConnsPerHost: 1,
		},
	}
	defer client.CloseIdleConnections()

	for i := range 10 {
		req, err := http.NewRequest(
			http.MethodPut,
			server.URL+"/api/hosts/remote-box/files/write?path=/work/f.txt&size=5",
			strings.NewReader("hello"),
		)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/octet-stream")

		var reused bool
		trace := &httptrace.ClientTrace{
			GotConn: func(connInfo httptrace.GotConnInfo) {
				reused = connInfo.Reused
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d failed on keep-alive connection: %v", i, err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			_ = resp.Body.Close() //nolint:errcheck // test cleanup
			t.Fatalf("request %d: failed to drain response body: %v", i, err)
		}
		_ = resp.Body.Close() //nolint:errcheck // test cleanup
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: got status %d, want 200", i, resp.StatusCode)
		}
		if i > 0 && !reused {
			t.Fatalf("request %d: connection was not reused across keep-alive requests", i)
		}
	}
}
