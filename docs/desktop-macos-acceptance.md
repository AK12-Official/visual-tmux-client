# macOS interactive acceptance handoff

Status: deferred at the user's request. The user will perform these checks on
their Mac. This is not a pass or a waiver of the corresponding OpenSpec tasks.
Native arm64/amd64 builds, architecture/signature checks, missing-tmux notice,
single-instance focus/signal cleanup and packaged Launch Services startup have
already passed; see [verification evidence](desktop-verification.md).

## Prepare

1. Obtain the `desktop-development-macOS-arm64` artifact for Apple Silicon or
   `desktop-development-macOS-amd64` for Intel from the draft PR's Desktop
   compatibility workflow. Extract its development ZIP and `.app` bundle.
2. Record the commit, macOS version, architecture and input method used.
   The documented deployment target is macOS 12; execution on that minimum
   version has not been validated.
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

Leave each unchecked item pending until executed. For failures, record steps,
expected/actual behavior and a diagnostic with credentials removed.

- [ ] Automatic login and workspace reload work without an exposed token or
  a second backend process. Application > Reload workspace also succeeds.
- [ ] Download a small binary file and a Unicode/text file; compare bytes with
  the originals. Cancel creates no file. Replacing an existing destination
  requires confirmation; cancellation preserves its original bytes.
- [ ] Downloads retain their original host/revision when the selected host
  changes. Reconnecting/changing that host while a chooser is open rejects an
  obsolete source. Disabled/out-of-root/oversized sources and destination write
  failures preserve the existing destination.
- [ ] Activated HTTP/HTTPS links open the system browser. Unsupported schemes
  cannot invoke native actions or replace the workspace.
- [ ] Copy selected terminal text and file paths, paste multiline text, compose
  Chinese with the input method, and check Unicode rendering. Ctrl+C without
  selection interrupts the terminal; Cmd+C/Cmd+V work as intended. Copy errors
  are visible.
- [ ] Enter/leave terminal fullscreen, exit via the OS window controls, resize
  rapidly, and scroll using mouse and trackpad during styled/Unicode output.
  The terminal refits and streaming remains usable.
- [ ] Create an unsaved text-file edit. Cancel window close and Quit/Cmd+Q;
  verify the draft and terminal attachment remain. Then confirm discard and
  verify the only window and application exit normally.
- [ ] Change locale/font/sidebar/recent-host/order/pins, quit and relaunch;
  verify restoration despite the new listener port. Preference-write errors
  stay visible without discarding editor drafts.
- [ ] Exercise local and disposable SSH hosts, trust repair, file edit/preview/
  save/download, sustained output and reconnect. Quit/relaunch and verify the
  intended tmux sessions survive and reattach without replayed input.
- [ ] Confirm Finder startup, missing-local-tmux guidance, same-profile second
  launch focus, bounded exit and next-launch state-lock availability. Existing
  headless operation must still work after the desktop application closes.

Do not mark the whole cross-platform task complete from this checklist alone;
Linux physical-input/session acceptance has its own pending portions in the
[manual checklist](desktop-manual-checklist.md).
Agent-run tmux tests must continue using the repository's guarded disposable
fixtures outside tmux; no unrelated session cleanup is part of acceptance.
