package desktop

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
)

const (
	launchLifetime  = 30 * time.Second
	bridgeBodyLimit = 1 << 20
	desktopPrefix   = "/api/desktop/"
)

// Bridge exposes only explicitly registered desktop HTTP actions. No Wails raw
// messages or generated service bindings authorize a native operation.
type Bridge struct {
	mu       sync.Mutex
	origin   string
	token    string
	code     string
	expires  time.Time
	now      func() time.Time
	actions  map[string]http.Handler
	document uint64
}

func NewBridge(token string) *Bridge {
	return &Bridge{token: token, now: time.Now, actions: make(map[string]http.Handler)}
}

// SetOrigin is called once after the owned loopback listener has bound.
func (b *Bridge) SetOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("desktop bridge requires an exact IPv4 loopback origin")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.origin != "" {
		return errors.New("desktop origin already assigned")
	}
	b.origin = origin
	return nil
}

// Register adds a native-side handler before serving requests. Registration is
// never available to the page. Every action inherits the same authorization gate.
func (b *Bridge) Register(name string, handler http.Handler) {
	if name == "" || name == "bootstrap" || strings.ContainsAny(name, "/?#") || handler == nil {
		panic("invalid desktop action registration")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.actions[name]; exists {
		panic("duplicate desktop action")
	}
	b.actions[name] = handler
}

// LaunchURL mints a single-use document authorization; it never includes bearer.
func (b *Bridge) LaunchURL() (string, error) {
	code, err := auth.GenerateBearerToken()
	if err != nil {
		return "", err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.origin == "" || b.token == "" {
		return "", errors.New("desktop bridge is not ready")
	}
	b.code, b.expires = code, b.now().Add(launchLifetime)
	// A new fragment alone is same-document navigation and cannot rerun the
	// bootstrap. The public counter forces a new document without exposing code.
	b.document++
	return b.origin + "/?desktop=1&document=" + strconv.FormatUint(b.document, 10) +
		"#launch=" + url.QueryEscape(code), nil
}

func (b *Bridge) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, desktopPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		b.serve(w, r)
	})
}

func (b *Bridge) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	b.mu.Lock()
	origin, token := b.origin, b.token
	b.mu.Unlock()
	if origin == "" || r.Host != strings.TrimPrefix(origin, "http://") || r.Header.Get("Origin") != origin {
		http.Error(w, "desktop origin refused", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "desktop actions require POST", http.StatusMethodNotAllowed)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bridgeBodyLimit)
	name := strings.TrimPrefix(r.URL.Path, desktopPrefix)
	if name == "bootstrap" {
		b.bootstrap(w, r)
		return
	}
	if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	b.mu.Lock()
	handler := b.actions[name]
	b.mu.Unlock()
	if handler == nil {
		http.NotFound(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

func (b *Bridge) bootstrap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if DecodeJSON(r, &body) != nil {
		http.Error(w, "invalid launch request", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	validCode := subtle.ConstantTimeCompare([]byte(body.Code), []byte(b.code)) == 1
	if b.code == "" || !b.now().Before(b.expires) || !validCode {
		http.Error(w, "launch authorization refused", http.StatusUnauthorized)
		return
	}
	b.code = ""
	WriteJSON(w, map[string]string{"token": b.token})
}

// DecodeJSON requires one complete typed request and refuses unknown fields.
func DecodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("one JSON document required")
	}
	return nil
}

func WriteJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value) //nolint:errcheck // the response is already committed
}
