package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
)

type DownloadRequest struct {
	HostID             string `json:"hostId"`
	Path               string `json:"path"`
	ConnectionRevision uint64 `json:"connectionRevision"`
}

// SaveDestination is supplied by native wiring, never by web content. Returning
// an existing destination means native overwrite confirmation has succeeded.
type SaveDestination func(context.Context, string) (string, error)

func (b *Bridge) AttachDownload(enabled bool, maxBytes int64, choose SaveDestination) {
	b.Register("download", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request DownloadRequest
		if DecodeJSON(r, &request) != nil || !validPreferenceID(request.HostID) ||
			request.ConnectionRevision == 0 || !path.IsAbs(request.Path) {
			http.Error(w, "invalid download request", http.StatusBadRequest)
			return
		}
		if !enabled {
			http.Error(w, "file management disabled", http.StatusForbidden)
			return
		}
		cancelled, err := b.saveDownload(r.Context(), request, maxBytes, choose)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		WriteJSON(w, map[string]bool{"cancelled": cancelled})
	}))
}

func (b *Bridge) saveDownload(
	ctx context.Context, source DownloadRequest, maxBytes int64, choose SaveDestination,
) (bool, error) {
	// Choose first, then validate/read the captured host/revision. A connection
	// change while the dialog is open must fail rather than redirect the source.
	destination, err := choose(ctx, path.Base(source.Path))
	if err != nil {
		return false, errors.New("destination selection failed")
	}
	if destination == "" {
		return true, nil
	}
	if !filepath.IsAbs(destination) {
		return false, errors.New("destination must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	origin, token := b.origin, b.token
	b.mu.Unlock()
	address := origin + "/api/hosts/" + url.PathEscape(source.HostID) +
		"/files/download?path=" + url.QueryEscape(source.Path)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Expected-Connection-Revision", strconv.FormatUint(source.ConnectionRevision, 10))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, errors.New("download source unavailable")
	}
	defer response.Body.Close() //nolint:errcheck // read-only source response; writes are staged separately
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("download source refused (%d)", response.StatusCode)
	}
	if maxBytes <= 0 || response.ContentLength > maxBytes {
		return false, errors.New("download exceeds file size limit")
	}
	return false, atomicDownload(ctx, destination, response.Body, maxBytes)
}

func atomicDownload(ctx context.Context, destination string, reader io.Reader, maxBytes int64) error {
	f, err := os.CreateTemp(filepath.Dir(destination), ".vtc-download-*")
	if err != nil {
		return errors.New("cannot stage download at destination")
	}
	defer func() { _ = os.Remove(f.Name()) }() //nolint:errcheck // absent after commit; preserve primary error
	limit := maxBytes
	if limit < math.MaxInt64 {
		limit++
	}
	n, err := io.Copy(f, io.LimitReader(reader, limit))
	if err != nil {
		_ = f.Close() //nolint:errcheck // preserve primary failure; uncommitted staging is removed
		return errors.New("download transfer failed")
	}
	if n > maxBytes {
		_ = f.Close() //nolint:errcheck // oversized staging is removed without committing
		return errors.New("download exceeds file size limit")
	}
	if err := ctx.Err(); err != nil {
		_ = f.Close() //nolint:errcheck // cancelled staging is removed without committing
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() //nolint:errcheck // preserve primary sync failure; staging is removed
		return errors.New("cannot sync download")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close download")
	}
	if err := os.Rename(f.Name(), destination); err != nil {
		return errors.New("cannot commit download")
	}
	return nil
}
