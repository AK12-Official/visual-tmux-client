## Context

See proposal.md for motivation and specs/session-hub/spec.md for the behavioral contract. Design is warranted because origin verification protects an interactive terminal and the explicit-policy path intentionally disables a duplicate library check.

Observed at branch head `fac6ec9`: `parseConfig` leaves origin empty; `server.attachRoute` checks the request host then clones the request and deletes Origin; `server.attach` still performs the old exact check and calls `websocket.Accept` with nil options. The pinned library's `accept.go` already parses Origin and compares its host with request Host using `strings.EqualFold`. With nil options it refuses foreign hosts, allows absent Origin, and does not compare schemes. Explicit origin currently passes the application check but can fail the library check behind a Host-rewriting proxy.

The existing attach fixture uses an isolated tmux server but sets a fixed explicit origin, and most successful connections omit Origin. The new branch tests only check the wrapper's foreign-origin rejection and its helper. README defaults still describe the listening address. Existing session-hub specs have a general cross-origin requirement; this change clarifies it without changing ticket authorization.

## Goals / Non-Goals

**Goals:** Make the handshake entry visibly select one effective origin policy, retain the original request headers, and verify behavior through the real router and WebSocket protocol.

**Non-Goals:** No generalized origin-policy framework, scheme discovery from forwarded headers, URL canonicalization rules, new configuration validation subsystem, frontend changes, or movement of ticket redemption before upgrade.

## Decisions

### Select policy at the existing attach entry

Restore the route directly to `s.attach`. Preserve the empty configuration default and remove `attachRoute` and `requestOriginAllowed`. Keep hostId validation and attachment lifecycle ordering intact. Default configuration calls `websocket.Accept(w, r, nil)` on the unmodified request, relying on the pinned library for origin verification.

Alternative: keep the wrapper and delete Origin after validation. Rejected because it couples multiple checks through a synthetic absence of security-relevant request data. A second Header clone is also unnecessary because Request.Clone already clones headers.

Alternative: implement another default URL parser and host comparison. Rejected because the library already supplies this behavior and the branch's manual string equality misses case-insensitive host comparison.

### Explicit configuration is authoritative

For non-empty `s.origin`, use the existing `checkOrigin` exact comparison, preserving its originless allowance. Return 403 on failure. Only after success create request-local AcceptOptions with `InsecureSkipVerify: true`, with an adjacent explanation that application origin verification has already completed. Pass these options to the same Accept call; never use a shared mutable options object or set this flag on the default path.

This flag disables only the library's duplicate Origin check. WebSocket protocol validation and subsequent ticket redemption still apply. A mismatched Origin must return before reaching Accept. Retain and clarify the helper's scope rather than changing it to mean implicit acceptance when configured origin is empty.

Alternative: use OriginPatterns alone. Rejected because request Host remains implicitly allowed, violating exact configured-origin precedence. Alternative: keep nil options even after exact checking. Rejected because it preserves the known rejection of a configured public origin when a proxy rewrites Host.

### Keep the library's default semantics explicit

Default mode matches host-and-port strings case-insensitively, including IPv6 brackets; it does not normalize default ports, resolve host aliases, or compare the browser scheme to backend TLS. Neither Forwarded nor X-Forwarded-Host is trusted. A proxy may preserve public Host, or operators may configure the public Origin explicitly.

The library's URL parser is more permissive than the branch's HTTP/HTTPS string-prefix helper for synthetic non-browser Origin values. No new HTTP/HTTPS-only parser restriction is introduced; documented browser HTTP/HTTPS cases, null, unparseable input, host case, IPv6, and port mismatches define the regression contract. Explicit configuration retains byte-for-byte matching without wildcard interpretation or normalization.

### Test the handshake boundary independently from tmux

Use the actual `s.handler()` with httptest and real WebSocket dialing. Policy-only cases can omit a ticket: acceptance is demonstrated by HTTP 101 followed by the existing invalid-ticket error, while rejected origins return 403. This keeps core policy checks executable without tmux. Use test-local Host overrides for domains, IPv6, and proxy cases instead of public DNS or external services. Supply valid upgrade headers when asserting origin rejection, since the library validates handshake syntax first.

For rejection side effects, issue a valid ticket and verify it is still redeemable after the rejected request, plus absence of attachment/PTY side effects. For complete issue coverage, parameterize or extend the isolated attach fixture to accept config from parseConfig with a wildcard addr and empty origin. Dial the httptest server through its actual address with its browser Origin and an issued session ticket, then require a ready frame. The listener can use a loopback ephemeral port: the configuration still exercises the original bind-derived-origin bug without opening the test server to the LAN.

Prove the complete regression test fails against pre-fix behavior in an isolated checkout or temporary copy, not by resetting the user's branch. Existing ticket tests cover expiry/reuse/mismatch; supplement only where needed to demonstrate allowed Origin cannot bypass these rules. Close WebSockets and stop isolated tmux servers in cleanup.

## Risks / Trade-offs

- A future refactor could enable the library bypass without application validation -> keep both adjacent, request-local, and cover explicit mismatch even when Origin matches Host.
- Library default matching is not strict browser same-origin semantics -> describe host-and-port matching accurately in CLI help and both READMEs; use explicit HTTPS origin for a scheme restriction.
- Trusting request Host does not provide a public-host allowlist -> retain bearer/ticket authorization and existing explicit-origin deployment advice; do not trust forwarded headers implicitly.
- tmux integration tests can be skipped on machines without tmux -> keep policy tests independent and require a tmux-enabled validation run before claiming issue #2 resolved.
- Explicit origin with rewritten Host changes previously rejected traffic to accepted traffic -> document that acceptance requires exact configured Origin and valid ticket, and test both independently.

## Migration Plan

No persisted data or environment-variable migration is needed. Update code, regression tests, CLI help, and both README tables together. Run focused origin/attach tests, then the hub suite and race checks in a tmux-enabled environment; distinguish skipped tests from passes. A release can be rolled back to the prior binary, but wildcard deployments then need an explicit origin matching the browser address and, with the old double check, a proxy preserving public Host. Publishing a release is outside this change.
