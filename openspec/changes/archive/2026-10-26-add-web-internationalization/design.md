## Context

See proposal.md for motivation and capability scope. The frontend uses Vue 3 Composition API and TypeScript. Text lives in Vue templates, event handlers, terminal lifecycle code, file-error helpers, and pre-mount bootstrap HTML. Help currently loads only `tmux-guide.zh-CN.md` and caches one rendered result. API errors include machine-readable identifiers that some callers inspect. Terminal panels and CodeMirror instances deliberately survive workspace interactions.

This design is warranted because localization crosses those modules and introduces a dependency. No backend configuration or protocol change is necessary.

## Goals / Non-Goals

**Goals:** One locale state available before mount and throughout component and non-component code; predictable matching/fallback; complete sentence translations; stable terminal/editor identity; testable locale selection without requiring a browser.

**Non-Goals:** Account synchronization, locale routing, remote translation services, arbitrary user-provided language packs, or changes to filesystem ordering and terminal interpretation.

## Decisions

### 1. Share a Vue I18n Composition API instance

Create a small `src/i18n` module with a pure preference resolver, guarded persistence, locale resources, and one shared instance initialized before bootstrap. Vue mounts with that instance; bootstrap and non-component modules use the same translator through a narrow exported entry point. Keep DOM access out of the resolver and initialize document language at the browser boundary.

Use semantic namespaces (`common`, `auth`, `hosts`, `sessions`, `terminal`, `files`, `help`, `errors`) and whole sentences with named parameters. Include both small interface catalogs in the local bundle so pre-mount errors do not depend on fetching translations. Retain lazy loading of file-manager code and guide rendering dependencies.

Alternative: a custom typed dictionary would reduce dependency cost, but would require maintaining interpolation, fallback, and reactive translation behavior. Vue I18n fits the existing Vue model and future language expansion. Select a supported release compatible with the project's toolchain during implementation and commit its lockfile.

### 2. Keep language preference separate from runtime configuration

Store only `auto`, `zh-CN`, or `en` under a dedicated origin-local key such as `vtc:locale`. Treat invalid values as `auto`; catch storage access failures and retain an in-memory choice. Use the matching rules in the new spec, including explicit handling of traditional Chinese variants. Resolve browser preferences at startup and when selecting follow-browser; listen for browser `languagechange` while in automatic mode.

Provide a compact native language select on login and in the workspace header, using self-identifying language names. Do not build a new settings page or store preference in host settings. No YAML or client-config fields are added. This avoids introducing server configuration just to express a per-browser choice.

### 3. Translate at presentation boundaries

Keep raw API errors and protocol codes unchanged. Map known codes to message keys when presenting failures; preserve original diagnostics for unknown cases. Keep error/state identifiers and interpolation parameters for long-lived visible errors so they can be recomputed when locale changes. Do not translate an error before existing code comparisons run. Adapt `files/reasons.ts` to localized lookup without losing its code-coverage checks.

Render reactive status text through computed translations rather than module-load string constants. New toasts resolve messages at creation; existing transient toasts may expire in their original language. Translate confirmation text at invocation. Preserve escaping of bootstrap diagnostic HTML; translations do not justify rendering untrusted HTML.

Alternative: translating in the transport layer risks changing branch behavior and makes it difficult to preserve diagnostics, so transport contracts remain stable.

### 4. Update presentation without remounting work

Never key the application, terminal, or editor by locale. Update template/computed text reactively and set `document.documentElement.lang`. Translate CodeMirror built-in phrases through its configurable extension mechanism, using a reconfigurable compartment rather than recreating its EditorView. Preserve existing terminal/session keys, selection, open file tabs, document state, and undo history. Do not translate terminal bytes or user-origin strings.

### 5. Cache help per locale and guard async completion

Add a self-contained English Markdown guide alongside the Chinese guide. Use an explicit locale-to-asset map and per-language HTML/promise caches. On locale changes, stop presenting the previous language as the current guide, show localized loading, and publish a response only if it still corresponds to the active language/open state. Cache successful responses by locale; clear failed promises to allow retry. Existing guide content and same-origin restrictions remain in force.

## Risks / Trade-offs

- Incomplete extraction or mismatched parameters -> verify catalog key/parameter parity and audit templates, title/aria attributes, bootstrap text, terminal notices, and editor phrases.
- Translated errors break behavior -> retain raw identifiers and cover representative known/unknown errors across both languages.
- Switching loses connection or editor state -> DOM regressions verify instance identity, unchanged attachment count, draft/selection, and actual undo behavior.
- Out-of-order help responses -> test delayed responses across rapid switches and failed-load retry.
- Longer translations overflow controls -> manually inspect both languages at narrow and desktop widths, including host forms and file confirmations.
- Added dependency/bundle cost -> keep catalogs local but preserve existing lazy feature boundaries and inspect build output.

## Migration Plan

Introduce the locale core, migrate interface surfaces and error presentation, then add localized guides and regression coverage. Existing storage keys and backend configuration stay compatible; absent language preference selects automatic mode. Deliver through the existing frontend build and embedded-asset packaging. Rollback restores the previous frontend; the new storage key is harmless when ignored by older versions.

Run the frontend suite and production build. Review embedded static asset serving before deciding whether targeted Go checks are needed; any tmux-backed checks must use the repository's existing isolation helpers and an agent running outside tmux.
