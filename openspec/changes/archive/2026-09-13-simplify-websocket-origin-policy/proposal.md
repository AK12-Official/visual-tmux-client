## Why

Issue [#2](https://github.com/AK12-Official/visual-tmux-client/issues/2) reports permanent terminal reconnecting when binding to `0.0.0.0:7690`: the released default compares the browser Origin with the listening address. The current branch fixes that mismatch through a wrapper that removes Origin; consolidating the policy at the handshake entry makes the fix easier to verify and maintain.

## What Changes

- Keep an unset `VISUAL_TMUX_CLIENT_ORIGIN` unset and use the WebSocket library's default request-host validation, independent of the listen address.
- Remove the wrapper route and request-header deletion; select the origin policy inside the terminal handshake handler.
- Make an explicitly configured origin an exact restriction, including when a reverse proxy forwards a different Host; skip duplicate library origin checking only after that application check succeeds.
- Preserve originless client compatibility and session-bound single-use ticket authorization.
- Add real-route handshake regression coverage and isolated tmux attachment coverage, including foreign-origin rejection without ticket consumption.
- Correct English/Chinese README defaults and CLI help to distinguish request-host matching from explicit origin matching.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `session-hub`: Clarify cross-origin connection rejection with default request-host matching, explicit exact-origin precedence, originless clients, and rejection before upgrade or ticket redemption.

## Impact

- Implementation targets: `hub/main.go`, `hub/server.go`, `hub/attach.go`, and the existing origin helper in `hub/auth.go` as needed.
- Tests: `hub/main_test.go`, `hub/attach_test.go`, and `hub/auth_test.go` as appropriate.
- Documentation: `README.md` and `README.zh-CN.md`; existing deployment advice in `SECURITY.md` remains applicable.
- Reuses pinned `github.com/coder/websocket v1.8.15`; no dependency upgrade, new API, or configuration variable.
- Explicit-origin acceptance behind a Host-rewriting proxy is a deliberate compatibility improvement over both the release and current branch. Default policy is host-and-port matching, not strict scheme equality.
- Frontend retry diagnostics, trusted-forwarded-header configuration, ticket lifecycle redesign, and release work are outside scope.
