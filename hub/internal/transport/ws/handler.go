package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/AK12-Official/visual-tmux-client/hub/internal/auth"
	"github.com/AK12-Official/visual-tmux-client/hub/internal/terminal"
	"github.com/coder/websocket"
)

const (
	defaultCols           = 80
	defaultRows           = 24
	errorWriteTimeout     = 3 * time.Second
	localHostIdentifier   = "local"
	hubShuttingDownReason = "hub shutting down"
)

// TicketRedeemer defines the interface to redeem terminal attachment tickets.
type TicketRedeemer interface {
	Redeem(ticket, session string) error
}

type errorMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Handler handles WebSocket terminal attachment requests.
type Handler struct {
	redeemer    TicketRedeemer
	procFactory terminal.ProcessFactory
	manager     *terminal.Manager
	opts        Options
	checkTmux   func() error
	onResize    func(session string, cols, rows int)
}

// NewHandler constructs a new WebSocket attach Handler.
func NewHandler(
	redeemer TicketRedeemer,
	procFactory terminal.ProcessFactory,
	manager *terminal.Manager,
	opts Options,
	checkTmux func() error,
	onResize func(session string, cols, rows int),
) *Handler {
	if opts.MaxInputMessage <= 0 {
		opts.MaxInputMessage = DefaultMaxInputMessage
	}
	if opts.MaxDimension <= 0 {
		opts.MaxDimension = DefaultMaxDimension
	}
	return &Handler{
		redeemer:    redeemer,
		procFactory: procFactory,
		manager:     manager,
		opts:        opts,
		checkTmux:   checkTmux,
		onResize:    onResize,
	}
}

func sendErrorAndClose(c *websocket.Conn, msg string, retryable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), errorWriteTimeout)
	defer cancel()

	b, _ := json.Marshal(errorMessage{ //nolint:errcheck // marshaling struct does not fail
		Type:      "error",
		Message:   msg,
		Retryable: retryable,
	})
	_ = c.Write(ctx, websocket.MessageText, b) //nolint:errcheck // best effort notification

	code := websocket.StatusPolicyViolation
	if retryable {
		code = websocket.StatusServiceRestart
	}
	_ = c.Close(code, msg) //nolint:errcheck // best effort close
}

// ParseDimensions parses rows and columns, falling back to 80x24 when invalid.
func ParseDimensions(colsStr, rowsStr string, maxDim int) (int, int) {
	cols, rows := defaultCols, defaultRows
	if c, err := strconv.Atoi(colsStr); err == nil && c >= 1 && c <= maxDim {
		cols = c
	}
	if r, err := strconv.Atoi(rowsStr); err == nil && r >= 1 && r <= maxDim {
		rows = r
	}
	return cols, rows
}

func (h *Handler) checkOriginHeader(r *http.Request) (bool, *websocket.AcceptOptions) {
	if h.opts.Origin == "" {
		return true, nil
	}
	if !CheckOrigin(r.Header.Get("Origin"), h.opts.Origin) {
		return false, nil
	}
	return true, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}
}

func (h *Handler) mapRedeemError(err error) string {
	switch {
	case errors.Is(err, auth.ErrTicketExpired):
		return "ticket expired"
	case errors.Is(err, auth.ErrTicketMismatch):
		return "ticket does not match session"
	default:
		return "invalid ticket"
	}
}

// ServeHTTP implements http.Handler for WebSocket attachments.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("hostId") != localHostIdentifier {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	session := r.PathValue("session")

	ok, acceptOpts := h.checkOriginHeader(r)
	if !ok {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, acceptOpts)
	if err != nil {
		return
	}
	conn.SetReadLimit(h.opts.MaxInputMessage)

	ticket := r.URL.Query().Get("ticket")
	if err := h.redeemer.Redeem(ticket, session); err != nil {
		sendErrorAndClose(conn, h.mapRedeemError(err), false)
		return
	}

	cols, rows := ParseDimensions(r.URL.Query().Get("cols"), r.URL.Query().Get("rows"), h.opts.MaxDimension)

	if h.checkTmux != nil {
		if err := h.checkTmux(); err != nil {
			sendErrorAndClose(conn, "tmux unavailable: "+err.Error(), true)
			return
		}
	}

	proc, err := h.procFactory.NewProcess(r.Context(), session, cols, rows)
	if err != nil {
		sendErrorAndClose(conn, "attach failed: "+err.Error(), true)
		return
	}

	var onResize func(c, r int)
	if h.onResize != nil {
		onResize = func(c, r int) {
			h.onResize(session, c, r)
		}
	}

	peer := NewConnPeer(conn)
	att := terminal.NewAttachment(
		proc,
		peer,
		h.opts.TerminalOptions,
		session,
		cols,
		rows,
		h.manager.Untrack,
		onResize,
	)

	if err := h.manager.Register(att); err != nil {
		_ = proc.Detach() //nolint:errcheck // best effort detach
		sendErrorAndClose(conn, hubShuttingDownReason, true)
		return
	}

	att.Start()
}
