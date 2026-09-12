package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func assertUpgradeAccepted(t *testing.T, conn *websocket.Conn, resp *http.Response, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected handshake success, got err=%v (resp=%v)", err, resp)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}
	mt, data := readFrame(t, conn)
	if mt != websocket.MessageText {
		t.Fatalf("expected text error frame, got type %d", mt)
	}
	var em errorMessage
	if err := json.Unmarshal(data, &em); err != nil {
		t.Fatalf("unmarshal error frame: %v", err)
	}
	if em.Type != "error" || em.Message != "invalid ticket" {
		t.Fatalf("expected invalid ticket error, got %+v", em)
	}
}

func assertHandshakeRejected(t *testing.T, conn *websocket.Conn, resp *http.Response, err error, wantStatus int) {
	t.Helper()
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatalf("expected dial to fail, but got active conn")
	}
	if err == nil {
		t.Fatalf("expected error on dial, got nil")
	}
	if resp == nil || resp.StatusCode != wantStatus {
		t.Fatalf("expected HTTP %d, got resp=%v err=%v", wantStatus, resp, err)
	}
}

// 1.3 Verify configured-origin/rewritten-Host acceptance and mismatched-origin/same-Host rejection.
func TestExplicitOriginRewrittenHostAccepted(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: "https://tmux.example.com"}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	// Client declares the configured public Origin, but request Host is the internal test server address (simulating a Host-rewriting proxy).
	conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", http.Header{
		"Origin": []string{"https://tmux.example.com"},
	})
	assertUpgradeAccepted(t, conn, resp, err)
}

func TestExplicitOriginMismatchedOriginSameHostRejected(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: "https://tmux.example.com"}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	// Client Origin matches request Host (ts.URL), but does not match configured origin.
	conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", http.Header{
		"Origin": []string{ts.URL},
	})
	assertHandshakeRejected(t, conn, resp, err, http.StatusForbidden)
}

// 2.1 Real-router handshake cases for default policy.
func TestDefaultOriginHandshakeCases(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: ""}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	tests := []struct {
		name       string
		host       string
		origin     string
		extraHdr   http.Header
		wantStatus int // 101 for accept, 403 for reject
	}{
		{
			name:       "http host match",
			host:       "example.com:7690",
			origin:     "http://example.com:7690",
			wantStatus: http.StatusSwitchingProtocols,
		},
		{
			name:       "https host match",
			host:       "example.com:7690",
			origin:     "https://example.com:7690",
			wantStatus: http.StatusSwitchingProtocols,
		},
		{
			name:       "hostname case insensitive match",
			host:       "ExAmPlE.CoM:7690",
			origin:     "http://example.com:7690",
			wantStatus: http.StatusSwitchingProtocols,
		},
		{
			name:       "bracketed ipv6 match",
			host:       "[::1]:7690",
			origin:     "http://[::1]:7690",
			wantStatus: http.StatusSwitchingProtocols,
		},
		{
			name:       "different port rejected",
			host:       "example.com:7690",
			origin:     "http://example.com:7691",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "foreign host rejected",
			host:       "example.com:7690",
			origin:     "http://evil.example:7690",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "null origin rejected",
			host:       "example.com:7690",
			origin:     "null",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "unparseable origin rejected",
			host:       "example.com:7690",
			origin:     "http://[invalid:ipv6",
			wantStatus: http.StatusForbidden,
		},
		{
			name:   "misleading forwarded headers not trusted",
			host:   "internal.example.com:7690",
			origin: "http://public.example.com:7690",
			extraHdr: http.Header{
				"X-Forwarded-Host": []string{"public.example.com:7690"},
				"Forwarded":        []string{"host=public.example.com:7690"},
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hdr := http.Header{}
			if tc.extraHdr != nil {
				hdr = tc.extraHdr.Clone()
			}
			if tc.origin != "" {
				hdr.Set("Origin", tc.origin)
			}
			opts := &websocket.DialOptions{
				Host:       tc.host,
				HTTPHeader: hdr,
			}
			conn, resp, err := dialWSOpts(t, ts, "/ws/local/demo", opts)
			if tc.wantStatus == http.StatusSwitchingProtocols {
				assertUpgradeAccepted(t, conn, resp, err)
			} else {
				assertHandshakeRejected(t, conn, resp, err, tc.wantStatus)
			}
		})
	}
}

// 2.2 Originless clients and exact explicit-origin semantics.
func TestOriginlessAndExplicitPolicySemantics(t *testing.T) {
	t.Run("originless default policy", func(t *testing.T) {
		s := newServer(&config{addr: "127.0.0.1:0", origin: ""}, "token")
		ts := httptest.NewServer(s.handler())
		defer ts.Close()

		// Omitted Origin
		conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", nil)
		assertUpgradeAccepted(t, conn, resp, err)

		// Empty Origin
		conn, resp, err = dialWSResp(t, ts, "/ws/local/demo", http.Header{"Origin": []string{""}})
		assertUpgradeAccepted(t, conn, resp, err)
	})

	t.Run("originless explicit policy", func(t *testing.T) {
		s := newServer(&config{addr: "127.0.0.1:0", origin: "https://tmux.example.com"}, "token")
		ts := httptest.NewServer(s.handler())
		defer ts.Close()

		// Omitted Origin
		conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", nil)
		assertUpgradeAccepted(t, conn, resp, err)

		// Empty Origin
		conn, resp, err = dialWSResp(t, ts, "/ws/local/demo", http.Header{"Origin": []string{""}})
		assertUpgradeAccepted(t, conn, resp, err)
	})

	t.Run("explicit origin scheme mismatch rejected", func(t *testing.T) {
		s := newServer(&config{addr: "127.0.0.1:0", origin: "https://tmux.example.com"}, "token")
		ts := httptest.NewServer(s.handler())
		defer ts.Close()

		// http instead of https
		conn, resp, err := dialWSResp(t, ts, "/ws/local/demo", http.Header{
			"Origin": []string{"http://tmux.example.com"},
		})
		assertHandshakeRejected(t, conn, resp, err, http.StatusForbidden)
	})

	t.Run("explicit origin exact match overrides request host", func(t *testing.T) {
		s := newServer(&config{addr: "127.0.0.1:0", origin: "https://configured.example.com"}, "token")
		ts := httptest.NewServer(s.handler())
		defer ts.Close()

		// Origin matches request Host but differs from configured origin -> rejected
		opts := &websocket.DialOptions{
			Host: "requested.example.com",
			HTTPHeader: http.Header{
				"Origin": []string{"https://requested.example.com"},
			},
		}
		conn, resp, err := dialWSOpts(t, ts, "/ws/local/demo", opts)
		assertHandshakeRejected(t, conn, resp, err, http.StatusForbidden)

		// Origin matches configured origin even though request Host is different -> accepted
		opts = &websocket.DialOptions{
			Host: "requested.example.com",
			HTTPHeader: http.Header{
				"Origin": []string{"https://configured.example.com"},
			},
		}
		conn, resp, err = dialWSOpts(t, ts, "/ws/local/demo", opts)
		assertUpgradeAccepted(t, conn, resp, err)
	})
}

// 2.3 Rejected origins leave tickets redeemable and spawn no attachment/PTY;
// accepted origins still enforce ticket validation.
func TestRejectedOriginPreservesTicketAndCreatesNoAttachment(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: ""}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	ticketID, _, _ := s.tickets.issue("probe")

	// Attempt WebSocket connection with a foreign origin and valid ticket.
	conn, resp, err := dialWSResp(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s", ticketID), http.Header{
		"Origin": []string{"http://evil.example:7690"},
	})
	assertHandshakeRejected(t, conn, resp, err, http.StatusForbidden)

	// Verify no attachment was tracked on server
	s.mu.Lock()
	attachedCount := len(s.attachments)
	s.mu.Unlock()
	if attachedCount != 0 {
		t.Fatalf("expected 0 attachments after rejected origin, got %d", attachedCount)
	}

	// Verify the ticket is still redeemable
	if err := s.tickets.redeem(ticketID, "probe"); err != nil {
		t.Fatalf("expected ticket to remain redeemable after origin rejection, got err: %v", err)
	}
}

func TestAcceptedOriginEnforcesTicketLifecycle(t *testing.T) {
	s := newServer(&config{addr: "127.0.0.1:0", origin: ""}, "token")
	ts := httptest.NewServer(s.handler())
	defer ts.Close()

	now := time.Now()
	s.tickets.now = func() time.Time { return now }

	// 1. Missing ticket
	conn := dialWS(t, ts, "/ws/local/probe", http.Header{"Origin": []string{ts.URL}})
	mt, data := readFrame(t, conn)
	if mt != websocket.MessageText {
		t.Fatalf("expected text frame, got %d", mt)
	}
	var em errorMessage
	_ = json.Unmarshal(data, &em)
	if em.Type != "error" || em.Message != "invalid ticket" {
		t.Fatalf("missing ticket: expected invalid ticket, got %+v", em)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// 2. Expired ticket
	expID, _, _ := s.tickets.issue("probe")
	now = now.Add(ticketTTL + time.Second) // advance clock past TTL
	conn = dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s", expID), http.Header{"Origin": []string{ts.URL}})
	mt, data = readFrame(t, conn)
	_ = json.Unmarshal(data, &em)
	if em.Type != "error" || em.Message != "ticket expired" {
		t.Fatalf("expired ticket: expected ticket expired, got %+v", em)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// 3. Reused ticket
	reusedID, _, _ := s.tickets.issue("probe")
	_ = s.tickets.redeem(reusedID, "probe")
	conn = dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s", reusedID), http.Header{"Origin": []string{ts.URL}})
	mt, data = readFrame(t, conn)
	_ = json.Unmarshal(data, &em)
	if em.Type != "error" || em.Message != "invalid ticket" {
		t.Fatalf("reused ticket: expected invalid ticket, got %+v", em)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")

	// 4. Mismatched session
	mismatchID, _, _ := s.tickets.issue("session-other")
	conn = dialWS(t, ts, fmt.Sprintf("/ws/local/probe?ticket=%s", mismatchID), http.Header{"Origin": []string{ts.URL}})
	mt, data = readFrame(t, conn)
	_ = json.Unmarshal(data, &em)
	if em.Type != "error" || em.Message != "ticket does not match session" {
		t.Fatalf("session mismatch: expected ticket does not match session, got %+v", em)
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}
