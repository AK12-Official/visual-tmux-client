## 1. Consolidate origin policy at the handshake

- [x] 1.1 Preserve empty default origin in `hub/main.go` and retain configuration tests for unset/empty and explicit values; verify `TestParseConfigOriginPolicy` passes without deriving an origin from wildcard addr.
- [x] 1.2 Route directly to `attach`, remove `attachRoute` and `requestOriginAllowed`, and use nil Accept options for default policy; replace helper/wrapper tests with a real-route same-host handshake test that passes with Origin present.
- [x] 1.3 Apply exact `checkOrigin` only in explicit mode, returning 403 on failure and enabling request-local `InsecureSkipVerify` only after success; verify configured-origin/rewritten-Host acceptance and mismatched-origin/same-Host rejection through real WebSocket handshakes, and review that Origin is never deleted or replaced.

## 2. Cover origin and authorization behavior

- [x] 2.1 Add tmux-independent real-router handshake cases for HTTP/HTTPS host matching, hostname case, bracketed IPv6, different ports, foreign host, null, unparseable Origin, and misleading forwarded headers; verify accepted cases upgrade then report invalid ticket and rejected cases return 403.
- [x] 2.2 Cover originless clients in both modes and exact explicit-origin semantics including scheme mismatch; verify only matching non-empty origins pass explicit mode even when a different Origin matches request Host.
- [x] 2.3 Verify rejected origins leave an issued ticket redeemable and create no attachment/PTY; verify accepted origins still refuse missing, expired, reused, and wrong-session tickets using existing ticket tests plus necessary handshake coverage.
- [x] 2.4 Extend the isolated tmux attach fixture for config produced by parseConfig with wildcard addr and empty origin; verify a connection bearing the actual test-server Origin and a valid session ticket receives ready, with WebSocket and tmux cleanup registered.
- [x] 2.5 Run the issue regression against pre-fix behavior in an isolated checkout or temporary copy and against the revised implementation; record old failure and new success without resetting or modifying the user's working branch for the comparison.

## 3. Document and validate the complete change

- [x] 3.1 Update CLI help and both README origin tables to describe default request-host-and-port matching and exact explicit configuration, including proxy Host preservation or explicit public Origin; verify no documentation still describes the default as the listen address and retain the HTTPS/token deployment guidance.
- [x] 3.2 Run focused origin/attach tests, then `go test ./...` and `go test -race ./...` from `hub` in a tmux-enabled environment; record failures and skips separately, and require the ready-frame regression to run successfully before declaring issue #2 fixed.
- [x] 3.3 Review the final diff for a single policy selection point, no request-header deletion, no dependency changes, and no unrelated frontend changes; run `openspec validate simplify-websocket-origin-policy --strict` and confirm the implementation meets each delta scenario.
