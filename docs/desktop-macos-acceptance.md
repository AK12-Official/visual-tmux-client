# macOS interactive acceptance handoff

The user reported passing all macOS manual checks, including the minimum-version
runtime check, on 2026-11-23. The tested build, exact macOS version, architecture
and input method were not supplied. Earlier automated results are in the
[verification evidence](desktop-verification.md). The user also reported a
separate long-running responsiveness issue, tracked in
[desktop performance TODO](TODO.md#桌面应用).

## Prepare

1. Obtain the `desktop-development-macOS-arm64` artifact for Apple Silicon or
   `desktop-development-macOS-amd64` for Intel from the draft PR's Desktop
   compatibility workflow. Extract its development ZIP and `.app` bundle.
2. Record the commit, macOS version, architecture and input method used.
   The documented deployment target is macOS 12.
3. These artifacts are ad-hoc signed development builds, without Developer ID
   or notarization. Follow the normal macOS application-opening policy. The
   optional notarization implementation is not evidence of a notarized artifact.
4. Open the normal **Visual Tmux Client.app** from Finder. Use a separate host
   state directory for test hosts if the normal state is already owned by a
   headless process. Configuration/state paths and explicit `--config` use are
   documented in [desktop operation](desktop.md).

Use normal desktop mode for the following checks. `--spike --probe-startup` is
only a synthetic startup smoke check. `--spike --probe` drives dialogs
automatically and is not the manual acceptance procedure.

## Record results

The checked items reflect the user’s manual acceptance report. For future
failures, record steps, expected and actual behavior, and sanitized diagnostics.

- [x] Automatic login and workspace reload work without an exposed token or
  a second backend process. Application > Reload workspace also succeeds.
- [x] Download a small binary file and a Unicode/text file; compare bytes with
  the originals. Cancel creates no file. Replacing an existing destination
  requires confirmation; cancellation preserves its original bytes.
- [x] Downloads retain their original host/revision when the selected host
  changes. Reconnecting/changing that host while a chooser is open rejects an
  obsolete source. Disabled/out-of-root/oversized sources and destination write
  failures preserve the existing destination.
- [x] Activated HTTP/HTTPS links open the system browser. Unsupported schemes
  cannot invoke native actions or replace the workspace.
- [x] Copy selected terminal text and file paths, paste multiline text, compose
  Chinese with the input method, and check Unicode rendering. Ctrl+C without
  selection interrupts the terminal; Cmd+C/Cmd+V work as intended. Copy errors
  are visible.
- [x] Enter/leave terminal fullscreen, exit via the OS window controls, resize
  rapidly, and scroll using mouse and trackpad during styled/Unicode output.
  The terminal refits and streaming remains usable.
- [x] Create an unsaved text-file edit. Cancel window close and Quit/Cmd+Q;
  verify the draft and terminal attachment remain. Then confirm discard and
  verify the only window and application exit normally.
- [x] Change locale/font/sidebar/recent-host/order/pins, quit and relaunch;
  verify restoration despite the new listener port. Preference-write errors
  stay visible without discarding editor drafts.
- [x] Exercise local and disposable SSH hosts, trust repair, file edit/preview/
  save/download, sustained output and reconnect. Quit/relaunch and verify the
  intended tmux sessions survive and reattach without replayed input.
- [x] Confirm Finder startup, missing-local-tmux guidance, same-profile second
  launch focus, bounded exit and next-launch state-lock availability. Existing
  headless operation must still work after the desktop application closes.

Linux physical-input/session acceptance remains pending in the
[manual checklist](desktop-manual-checklist.md).
Agent-run tmux tests must continue using the repository's guarded disposable
fixtures outside tmux; no unrelated session cleanup is part of acceptance.
