## Why

The Web interface currently hardcodes mostly English text while its help guide is Chinese, with no language preference. Users need a consistent Simplified Chinese or English experience across configuration-related controls and the rest of the interface without interrupting active work.

## What Changes

- Add browser-local language selection: follow browser, Simplified Chinese, or English; retain preference through logout and host changes.
- Localize login, host management, sessions, terminal controls, file management, notifications, confirmations, accessibility text, and bootstrap failure/loading surfaces.
- Support live language changes without reconnecting terminals, discarding drafts, or resetting editor undo history.
- Keep machine error codes stable and translate known failures at presentation boundaries while preserving unknown diagnostics.
- Ship equivalent Chinese and English help guides from embedded same-origin assets and select/cache them by language.
- Leave CLI help, server logs, configuration keys, protocol identifiers, user content, and terminal output outside translation scope.

## Capabilities

### New Capabilities

- `web-internationalization`: Browser-local locale resolution, complete Web presentation localization, fallback, and state-preserving switching.

### Modified Capabilities

- `in-app-help`: Serve the guide in the active interface language with per-language caching and safe handling of overlapping loads.

## Impact

- Affects `hub/web/src`, frontend tests, Web dependencies and lockfile, and embedded guide assets under `hub/web/public`.
- Introduces Vue I18n and shared translation resources accessible before Vue mounts and from non-component modules.
- No planned changes to Go configuration schema, public client-config projection, API contracts, authentication, or tmux behavior.
