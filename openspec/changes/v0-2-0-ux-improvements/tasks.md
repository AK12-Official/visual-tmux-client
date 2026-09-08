# Tasks: v0.2.0 UX Improvements

## 1. Toast notification foundation

- [x] 1.1 Create `hub/web/src/toasts.ts` (module store: `notify(level, message)`, `useToasts()`, levels error/warning/info with 8/5/3 s lifetimes, stack cap 5 dropping oldest) and `hub/web/src/components/ToastStack.vue` (stacked rendering, newest pushes others up, level-distinct styling, manual dismiss); verify `npm --prefix hub/web run build` passes and manual triggering of several messages shows coexistence, per-level colors, and auto-expiry
- [x] 1.2 Replace `App.vue`'s single `notice` ref with the toast store for all existing emitters (create/rename/kill failures, terminal notices, copy failures); verify a rejected rename shows a leveled toast and the old gray banner no longer renders

## 2. Hub fixes (Go)

- [x] 2.1 Rewrite `validateSessionName` as a code-point walk (non-empty, valid UTF-8, no control characters, no `:`/`.`, no leading/trailing whitespace, ≤ 64 runes) returning an error that names the first violated constraint; add table-driven cases to `hub/tmux_test.go` covering CJK acceptance and each rejection; verify `go test ./...` in `hub/`
- [x] 2.2 Make empty-name creation retry generated names with a `-1`…`-9` suffix on collision; add a test creating two nameless sessions within the same second; verify both succeed with distinct names and `go test ./...` passes
- [x] 2.3 Move token printing out of `resolveToken` into a single startup banner in `main.go` that always prints the effective token with its source (`environment`/`generated`) plus an `open http://…` line derived from `--addr` (substituting `127.0.0.1` for wildcard hosts); update `hub/auth_test.go`; verify `go test ./...` and a manual run with and without `VISUAL_TMUX_CLIENT_TOKEN` shows the banner in both cases

## 3. Re-attachable terminals

- [x] 3.1 Add the ended overlay to `TerminalView.vue`: when the connection state is `ended` and the session still exists in the polled list, show "detached" with a Reconnect action that disposes the current `TerminalSession` and builds a new one; when the session is gone, state that it ended; verify manually: detach with `Ctrl-b d` → Reconnect restores live I/O without a reload; kill the session → the ended wording appears with no reconnect offer
- [x] 3.2 Confirm `KeepAlive` never serves a dead instance after reconnect (switch away and back after a detach+reconnect); verify the view still streams and accepts input

## 4. One-click session creation

- [x] 4.1 Replace the name input + Create button in `SessionList.vue` with a single create action that emits creation without a name, and drop the name from `App.vue`'s create handler; verify clicking it produces a `session-…`-named session in the list, and two clicks within one second produce two distinct sessions (with task 2.2)

## 5. Terminal header controls

- [x] 5.1 Add the terminal header to `App.vue`'s main region (session name, connection state moved here from the app bar, activity dot placeholder); verify it renders for the selected session and the placeholder view shows when none is selected
- [x] 5.2 Implement font-size −/+ : persisted `vtc:font-size` state (default 13, clamp 8–24, 1 px steps) passed as a prop, applied in `TerminalView` via `term.options.fontSize` followed by a re-fit; verify the grid re-negotiates (columns change) and the size survives a reload
- [x] 5.3 Implement fullscreen: `requestFullscreen()` on the terminal container with the exit path restoring layout; verify entering/leaving fullscreen re-fits the terminal to the new box
- [x] 5.4 Implement close: sets selection to null and returns to the placeholder; verify the session remains alive and listed, and its card can still show activity

## 6. Session list redesign

- [x] 6.1 Rework session cards to a taller two-line layout (name row, meta/actions row); verify visual check across 1/2-digit window counts and long CJK names (ellipsis, no wrap)
- [x] 6.2 Add select mode: a toggle that shows per-card checkboxes, suspends click-to-navigate, and offers a confirmed bulk kill that issues sequential DELETEs and reports per-session failures as toasts; verify selecting 3 sessions and killing removes exactly those, and a mid-batch failure names the failed session
- [x] 6.3 Add ordering state `vtc:order` (`mode`, `order[]`, `pinned[]`) with default/manual mode toggle; manual rendering is pinned first, then saved order, then unseen sessions appended; renames and kills keep the stored entries consistent; verify mode and positions survive reload
- [x] 6.4 Add drag reorder and pin/unpin in manual mode (HTML5 drag-and-drop on cards); verify a dragged position and a pin persist across reload and that a newly created session appears without disturbing saved positions
- [x] 6.5 Add sidebar collapse: persisted `vtc:sidebar-collapsed`, 260 px ↔ ~44 px icon rail (expand, create, help), animated width; verify the terminal re-fits on collapse/expand and the state survives reload

## 7. Activity indication

- [x] 7.1 Add the throttled `onActivity` hook to `terminal.ts` (fires on binary frames, at most once per 500 ms per session); verify via a temporary console log that output in a background session fires it
- [x] 7.2 Track activity in `App.vue` (`lastActiveAt` per session with ~2 s decay timers) and highlight non-selected cards in `SessionList.vue` with a distinct border; verify running `ls` in a backgrounded session lights its card and the highlight fades ~2 s after output stops
- [x] 7.3 Indicate the selected session's activity with the header breathing dot and a `● ` prefix on `document.title`, both cleared on decay; verify the tab title signals output while the tab is backgrounded and restores when output stops

## 8. In-app help

- [x] 8.1 Adapt the author's desktop tmux guide into `hub/web/public/tmux-guide.zh-CN.md`: keep fundamentals and the cheat sheet, drop personal references, add a 使用本程序 section (token connection, create/rename, attach/detach/close semantics); verify the file is served by both `vite dev` and the built hub
- [x] 8.2 Add the `marked` dependency and `HelpModal.vue` (fetch-once with cached render, `v-html` output, Esc/backdrop close); wire help buttons into the sidebar footer and the collapsed rail; verify opening/closing the overlay keeps an attached terminal connected
- [x] 8.3 Verify self-containment: with the guide open, the browser devtools network panel shows requests only to the hub origin

## 9. Release v0.2.0

- [x] 9.1 Update `README.md` and `README.zh-CN.md`: Unicode name rules, always-printed token banner, new features, and version-bumped download/run examples
- [x] 9.2 Bump versions (`npm --prefix hub/web version 0.2.0 --no-git-tag-version`, `Makefile` `VERSION`), run `make test`, build all four targets via `make package`, and smoke-test the local artifact (`--version`, `--help`, tarball contains binary + both READMEs + LICENSE)
- [ ] 9.3 After `main` CI is green, tag `v0.2.0`, push, and verify the GitHub Release assets (four archives + checksums) per the release workflow doc
