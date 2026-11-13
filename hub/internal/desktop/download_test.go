package desktop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadRequiresExplicitOverwriteEvenWhenDestinationAppearsDuringTransfer(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "selected.bin")
	reader := &destinationRaceReader{destination: destination}
	err := atomicDownload(context.Background(), Destination{Path: destination}, reader, 1024)
	if err == nil {
		t.Fatal("download silently overwrote a destination created during transfer")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "other application" {
		t.Fatal("unconfirmed destination changed")
	}
	if err := atomicDownload(context.Background(), Destination{Path: destination, OverwriteConfirmed: true},
		strings.NewReader("confirmed replacement"), 1024); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "confirmed replacement" {
		t.Fatal("confirmed replacement failed")
	}
	entries, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".vtc-download-*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("destination staging leaked")
	}
}

type destinationRaceReader struct {
	destination string
	done        bool
}

func (r *destinationRaceReader) Read(data []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	if err := os.WriteFile(r.destination, []byte("other application"), 0600); err != nil {
		return 0, err
	}
	return copy(data, "download bytes"), nil
}

func TestDownloadCapturesSourceAndCommitsOnlyCompleteBytes(t *testing.T) {
	const data = "\x00\xffterminal 中文\n"
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/api/hosts/host-a/files/download" || r.URL.Query().Get("path") != "/source/file" ||
			r.Header.Get("X-Expected-Connection-Revision") != "7" ||
			r.Header.Get("Authorization") != "Bearer test-bearer" {
			t.Error("source identity/auth changed")
		}
		if _, err := io.WriteString(w, data); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(server.URL); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "saved")
	source := DownloadRequest{HostID: "host-a", Path: "/source/file", ConnectionRevision: 7}
	cancelChoice := func(context.Context, string) (Destination, error) { return Destination{}, nil }
	choose := func(context.Context, string) (Destination, error) { return Destination{Path: destination}, nil }
	cancelled, err := b.saveDownload(context.Background(), source, 1024, cancelChoice)
	if err != nil || !cancelled || requestCount != 0 {
		t.Fatal("cancel read/wrote files")
	}
	if _, err := b.saveDownload(context.Background(), source, 1024, choose); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != data {
		t.Fatal("download bytes changed")
	}
	if _, err := b.saveDownload(context.Background(), source, 2, choose); err == nil {
		t.Fatal("oversized source allowed")
	}
	got, err = os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Fatal("failed download replaced target")
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".vtc-download-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("staging leaked")
	}
}

func TestDownloadFailureAndDisabledGate(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "saved")
	if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	broken := io.MultiReader(strings.NewReader("partial"), errorReader{})
	confirmed := Destination{Path: destination, OverwriteConfirmed: true}
	if atomicDownload(context.Background(), confirmed, broken, 1024) == nil {
		t.Fatal("partial transfer committed")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatal("old destination lost")
	}
	b := NewBridge("test-bearer")
	if err := b.SetOrigin(testOrigin); err != nil {
		t.Fatal(err)
	}
	b.AttachDownload(false, 1024, func(context.Context, string) (Destination, error) {
		t.Fatal("disabled download opened dialog")
		return Destination{}, nil
	})
	body := `{"hostId":"local","path":"/source","connectionRevision":1}`
	if resp := bridgeRequest(b, "download", body, testOrigin, "test-bearer"); resp.Code != 403 {
		t.Fatal("disabled access allowed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "revision changed", http.StatusConflict)
	}))
	defer server.Close()
	b = NewBridge("test-bearer")
	if err := b.SetOrigin(server.URL); err != nil {
		t.Fatal(err)
	}
	source := DownloadRequest{HostID: "local", Path: "/source", ConnectionRevision: 1}
	choose := func(context.Context, string) (Destination, error) { return Destination{Path: destination}, nil }
	if _, err := b.saveDownload(context.Background(), source, 1024, choose); err == nil {
		t.Fatal("stale source allowed")
	}
	got, err = os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatal("source rejection modified destination")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
