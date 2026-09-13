## 1. Hub Global Options and Mouse Support

- [x] 1.1 Implement `ensureGlobalOptions` in `hub/server.go` to independently and idempotently check and configure `window-size latest` and `mouse on` via `show-options -gv` checks.
- [x] 1.2 Invoke `ensureGlobalOptions` on WebSocket attachment in `hub/attach.go` and on session creation in `hub/api.go`.
- [x] 1.3 Add automated unit tests in `hub/` verifying `ensureGlobalOptions` sets `mouse on` and avoids redundant set commands when already active.

## 2. Documentation Update

- [x] 2.1 Update `hub/web/public/tmux-guide.zh-CN.md` to reflect that mouse support and wheel/trackpad scrolling are enabled by default.

## 3. Verification and Build

- [x] 3.1 Run all Go tests in `hub/` and verify they pass.
- [x] 3.2 Run frontend typecheck and build in `hub/web` (`npm run build`) to ensure assets are updated and build cleanly.
- [x] 3.3 Verify end-to-end mouse wheel scroll behavior in tmux session with output exceeding viewport.
