package desktop

import (
	"errors"
	"net/http"
	"net/url"
)

const maxExternalURLBytes = 8192

func ExternalURL(raw string) (string, error) {
	if len(raw) > maxExternalURLBytes {
		return "", errors.New("external URL too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", errors.New("only HTTP/HTTPS links without embedded credentials may be opened")
	}
	return u.String(), nil
}

func (b *Bridge) AttachExternalLinks(open func(string) error) {
	b.Register("open-external", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL string `json:"url"`
		}
		if DecodeJSON(r, &body) != nil {
			http.Error(w, "invalid link request", http.StatusBadRequest)
			return
		}
		address, err := ExternalURL(body.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := open(address); err != nil {
			http.Error(w, "system browser unavailable", http.StatusServiceUnavailable)
			return
		}
		WriteJSON(w, map[string]bool{"ok": true})
	}))
}
