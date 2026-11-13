package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

var ErrPreferenceConflict = errors.New("desktop preferences changed; reload before saving")

type PreferenceSnapshot struct {
	Version  int               `json:"version"`
	Revision uint64            `json:"revision"`
	Values   map[string]string `json:"values"`
}

type Preferences struct {
	mu      sync.Mutex
	path    string
	state   PreferenceSnapshot
	warning string
}

// OpenPreferences never replaces an unreadable or corrupt file during startup.
// The host registry must own this private directory before opening preferences.
func OpenPreferences(directory string) *Preferences {
	p := &Preferences{path: filepath.Join(directory, "desktop-preferences.json"),
		state: PreferenceSnapshot{Version: 1, Values: map[string]string{}}}
	info, err := os.Lstat(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return p
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > bridgeBodyLimit {
		p.warning = "Desktop preferences could not be loaded; using defaults."
		return p
	}
	f, err := os.Open(p.path)
	if err != nil {
		p.warning = "Desktop preferences could not be read; using defaults."
		return p
	}
	defer f.Close() //nolint:errcheck // read-only preference input; no pending writes
	decoder := json.NewDecoder(io.LimitReader(f, bridgeBodyLimit+1))
	decoder.DisallowUnknownFields()
	var state PreferenceSnapshot
	var extra any
	if decoder.Decode(&state) != nil || !errors.Is(decoder.Decode(&extra), io.EOF) ||
		state.Version != 1 || validatePreferences(state.Values) != nil {
		p.warning = "Desktop preferences are invalid; using defaults. Original file preserved."
		return p
	}
	p.state = state
	return p
}

func (p *Preferences) Snapshot() (PreferenceSnapshot, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	values := make(map[string]string, len(p.state.Values))
	for k, v := range p.state.Values {
		values[k] = v
	}
	return PreferenceSnapshot{Version: 1, Revision: p.state.Revision, Values: values}, p.warning
}

func (p *Preferences) Save(next PreferenceSnapshot) error {
	if next.Version != 1 {
		return errors.New("unsupported preference version")
	}
	if err := validatePreferences(next.Values); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if next.Revision != p.state.Revision {
		return ErrPreferenceConflict
	}
	next.Revision++
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(data) > bridgeBodyLimit {
		return errors.New("preferences exceed size limit")
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".desktop-preferences-*")
	if err != nil {
		return errors.New("cannot create preference staging file")
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }() //nolint:errcheck // absent after commit; preserve primary error
	if _, err := f.Write(data); err != nil {
		_ = f.Close() //nolint:errcheck // preserve primary failure; staging is removed
		return errors.New("cannot write preferences")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close() //nolint:errcheck // preserve primary failure; staging is removed
		return errors.New("cannot sync preferences")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close preferences")
	}
	if err := os.Rename(name, p.path); err != nil {
		return errors.New("cannot replace preferences")
	}
	// Own the decoded copy rather than retaining the caller's mutable map.
	var committed PreferenceSnapshot
	if err := json.Unmarshal(data, &committed); err != nil {
		return err
	}
	p.state, p.warning = committed, ""
	dir, err := os.Open(filepath.Dir(p.path))
	if err != nil {
		return errors.New("cannot sync preference directory")
	}
	defer dir.Close() //nolint:errcheck // directory already synced; no pending writes
	if err := dir.Sync(); err != nil {
		return errors.New("preference durability uncertain; reload before retrying")
	}
	return nil
}

const (
	maxPreferenceKeys       = 512
	maxPreferenceFont       = 256
	maxPreferenceIDBytes    = 128
	maxPreferenceOrderBytes = 65536
)

func validatePreferences(values map[string]string) error {
	if values == nil || len(values) > maxPreferenceKeys {
		return errors.New("invalid preference collection")
	}
	for key, value := range values {
		if !validPreference(key, value) {
			return fmt.Errorf("invalid desktop preference key/value for %q", key)
		}
	}
	return nil
}

func validPreference(key, value string) bool {
	switch key {
	case "vtc:locale":
		return value == "auto" || value == "en" || value == "zh-CN"
	case "vtc:font-size":
		n, err := strconv.Atoi(value)
		return err == nil && n > 0 && n <= maxPreferenceFont
	case "vtc:sidebar-collapsed":
		return value == "0" || value == "1"
	case "vtc:recent-host":
		return validPreferenceID(value)
	default:
		return strings.HasPrefix(key, "vtc:order:") &&
			validPreferenceID(strings.TrimPrefix(key, "vtc:order:")) && validOrder(value)
	}
}

func validPreferenceID(value string) bool {
	return len(value) > 0 && len(value) <= maxPreferenceIDBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}

func validOrder(value string) bool {
	if len(value) > maxPreferenceOrderBytes {
		return false
	}
	var order struct {
		Mode   string   `json:"mode"`
		Order  []string `json:"order"`
		Pinned []string `json:"pinned"`
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&order) != nil || (order.Mode != "default" && order.Mode != "manual") {
		return false
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || len(order.Order)+len(order.Pinned) > 4096 {
		return false
	}
	for _, group := range [][]string{order.Order, order.Pinned} {
		for _, name := range group {
			if !validPreferenceID(name) || utf8.RuneCountInString(name) > 64 {
				return false
			}
		}
	}
	return true
}

// Attach serves snapshots and writes only behind Bridge's POST/origin/auth gate.
func (p *Preferences) Attach(bridge *Bridge) {
	bridge.Register("preferences-read", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var empty struct{}
		if DecodeJSON(r, &empty) != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		snapshot, warning := p.Snapshot()
		WriteJSON(w, struct {
			PreferenceSnapshot
			Warning string `json:"warning,omitempty"`
		}{snapshot, warning})
	}))
	bridge.Register("preferences-write", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var next PreferenceSnapshot
		if DecodeJSON(r, &next) != nil {
			http.Error(w, "invalid preferences", http.StatusBadRequest)
			return
		}
		if err := p.Save(next); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrPreferenceConflict) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		snapshot, _ := p.Snapshot()
		WriteJSON(w, snapshot)
	}))
}
