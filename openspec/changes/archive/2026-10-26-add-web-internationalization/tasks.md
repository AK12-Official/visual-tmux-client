## 1. Locale foundation

- [x] 1.1 Add a compatible Vue I18n dependency and lockfile update, shared pre-mount instance, and English/Chinese catalogs; verify TypeScript/build integration and a translation call outside component setup.
- [x] 1.2 Implement pure language matching and guarded browser-local preference storage; test ordered matching, Hans/Hant and regional tags, English variants, invalid preferences, empty browser languages, storage failures, and reload persistence.
- [x] 1.3 Add namespace/key and interpolation-parameter parity checks; verify missing translations fall back to English and both shipped catalogs pass parity checks.

## 2. Language selection and startup

- [x] 2.1 Add accessible selectors on login and the workspace header, update document language, and handle automatic browser-language changes; verify selection survives logout/host changes and explicit choice ignores browser language changes.
- [x] 2.2 Localize pre-mount loading/failure/retry text and global diagnostic wrappers; test configuration failure before Vue mount in both languages and confirm original diagnostics remain escaped and readable.

## 3. Interface and errors

- [x] 3.1 Extract login, host-management, session-management, and terminal-control text, including placeholders, validation, status labels, tooltips, and accessible names; verify representative DOM flows in both languages and language changes on persistent statuses.
- [x] 3.2 Localize application-owned file-manager text, confirmations, notices, and error reasons with whole-message interpolation; verify known error mappings retain coverage and user paths/content remain unchanged.
- [x] 3.3 Separate known error presentation from raw machine identifiers across API consumers and terminal notices; test representative conflicts/authentication failures and unknown diagnostics in both languages without changing branch/retry behavior.
- [x] 3.4 Configure CodeMirror built-in phrases for live locale updates without recreating the editor; verify localized search controls, unchanged editor identity, preserved selection/draft, and successful undo after switching.

## 4. Help guides

- [x] 4.1 Add an English embedded guide equivalent to the Chinese guide and localize help controls; verify both cover existing required topics and use only same-origin resources.
- [x] 4.2 Implement per-language guide caches and stale-response protection; test switching during loading, switching while open, cache reuse, closing during load, and retry after failure.

## 5. Integration verification

- [x] 5.1 Add workspace regressions that switch language with active terminal and unsaved files; verify selected host/session, terminal instance and attachment count, open panels, draft, selection, and undo history survive.
- [x] 5.2 Audit application-owned strings and inspect login, workspace, host forms, editor controls, help, and destructive confirmations in both languages at desktop/narrow widths; record that tooltips/accessibility text are translated and user content is unchanged.
- [x] 5.3 Run `npm test` and `npm run build` in `hub/web`; inspect lazy feature boundaries and packaged guide serving, and run targeted existing asset-serving checks if needed. Any tmux-backed verification must use existing isolation helpers outside tmux. Record results and run `openspec validate add-web-internationalization --strict`.
