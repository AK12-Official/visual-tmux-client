# Desktop checks requiring the user's machine

Use the development package from the current PR's Desktop compatibility run.
Record the commit, OS version, architecture, desktop session and input method.
Keep macOS OpenSpec acceptance pending until the corresponding checks pass.

## Linux physical desktop

Automated native evidence is in [Linux acceptance](desktop-linux-acceptance.md).
These checks require the actual input devices and desktop session:

- [ ] Launch the installed app from the desktop launcher in your normal session.
  Open it again and verify the existing window receives focus. Check normal
  window controls, display scaling and moving between your monitors.
- [ ] Compose Chinese using your normally configured input method, including
  candidate selection, correction and mixed Chinese/English. Verify no lost or
  doubled text, and test multiline paste and selected-text copy.
- [ ] Scroll terminal history with the physical mouse and touchpad, including
  smooth scrolling and direction changes, while styled/Unicode output streams.
  Resize and enter/leave fullscreen; verify readable output and correct refit.

If your session uses a different portal backend or compositor from the verified
GNOME/Mutter and X11 environments, repeat save, cancel, replacement confirmation
and byte comparison there. The Weston 13 desktop-shell portal crash is retained
as a known reproducer in the Linux record; it is not a passing configuration.

## macOS

Follow the full [macOS handoff](desktop-macos-acceptance.md), using the artifact
for your Mac's architecture. In particular:

- [ ] Finder launch, automatic login, both reload paths and repeat-launch focus.
- [ ] Native file save/download, exact bytes, cancel and overwrite; stale host
  revision and failed destination writes must preserve the existing file.
- [ ] System-browser links, clipboard, Chinese input, terminal shortcuts,
  mouse/touchpad scrolling, fullscreen and resize.
- [ ] Dirty editor close and Cmd+Q: cancel preserves drafts/attachments; confirm
  discard exits. Relaunch restores locale, font, sidebar, recent host and pins.
- [ ] Local/SSH terminal and file workflow, trust repair, reconnect and tmux
  session survival after desktop quit/relaunch.
- [ ] Missing-tmux guidance, bounded exit, next-launch state-lock availability
  and continued headless operation.

The supported minimum macOS version also requires execution on that OS version;
building on newer CI runners does not prove minimum-version runtime support.
Developer ID signing/notarization is optional and requires the owner's signing
credentials if a signed release is desired. Current packages are development
builds and do not claim notarization.

This document lists user-dependent checks. Pending automated CI/install-package
gates remain agent work and must not be reassigned to the user merely because
they are queued or running.
