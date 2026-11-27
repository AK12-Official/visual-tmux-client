# Desktop checks requiring the user's machine

Use the development package from the current PR's Desktop compatibility run.
Record the commit, OS version, architecture, desktop session and input method.

## Linux physical desktop

User confirmation on 2026-11-16: the installed Linux desktop passes the three
physical-session checks below, plus automatic login/reload, Ctrl+C without a
selection, file edit/save, byte-equal download, download cancellation/overwrite,
dirty-quit cancellation preserving drafts, confirmed quit/restart, preference
restoration and original tmux session reattachment. The exact installed package
revision, display protocol and input-method version were not supplied in this
confirmation. It does not establish macOS acceptance or additional unreported
host/error-path coverage.

The user additionally confirmed on 2026-11-16 that the desktop automatic
language option displays the updated “自动” label after installing the update.

Automated native evidence is in [Linux acceptance](desktop-linux-acceptance.md).
These checks require the actual input devices and desktop session:

- [x] Launch the installed app from the desktop launcher in your normal session.
  Open it again and verify the existing window receives focus. Check normal
  window controls, display scaling and moving between your monitors.
- [x] Compose Chinese using your normally configured input method, including
  candidate selection, correction and mixed Chinese/English. Verify no lost or
  doubled text, and test multiline paste and selected-text copy.
- [x] Scroll terminal history with the physical mouse and touchpad, including
  smooth scrolling and direction changes, while styled/Unicode output streams.
  Resize and enter/leave fullscreen; verify readable output and correct refit.

If your session uses a different portal backend or compositor from the verified
GNOME/Mutter and X11 environments, repeat save, cancel, replacement confirmation
and byte comparison there. The Weston 13 desktop-shell portal crash is retained
as a known reproducer in the Linux record; it is not a passing configuration.

- [ ] After installing the package, check the application menu, the dock or
  taskbar while the app runs, and the window switcher: each shows the terminal
  prompt icon, sharp at the size the desktop draws it. Switch the desktop icon
  theme or scale and check it again.
- [ ] Save a proxy address in Settings, then open a new local terminal. Run
  `tmux show-environment -t <session> https_proxy` in it, replacing `<session>`
  with the name the sidebar shows for it. It prints `https_proxy=socks5h://`
  followed by the saved address. A session opened before the address was saved
  prints nothing and says `unknown variable` instead, and so does a new session
  after the address is cleared. Then run a command that honors these variables, such as
  `curl -I https://github.com`, and check that it connects. If your shell
  configuration sets proxy variables of its own, its values are in effect in
  the terminal, which the settings dialog points out.
- [ ] Switch the interface language from English to Chinese and back with the
  sidebar open, in the host manager list and in the add-host form. No button,
  selector or input may change height or width, and no label may wrap or be
  cut off. A longer error or hint may wrap. Terminal connections and unsaved
  editor content survive both switches.

## macOS

The user reported passing the full [macOS manual acceptance checklist](desktop-macos-acceptance.md)
on 2026-11-23, including the minimum-version runtime check. Developer ID signing
and notarization remain optional and require the owner’s signing credentials.

- [ ] Investigate and fix progressive desktop slowdown during extended use.
  The user observed increasing lag until the app became unusable. Track the
  fix and sustained-use verification in
  [desktop performance TODO](TODO.md#桌面应用).

- [ ] After unpacking the development archive, check the icon in Finder, in
  Launchpad and in the Dock while the app runs. Each shows the terminal prompt
  icon at the same visual size as neighboring apps, sharp in list, icon and
  Cover Flow views. Relaunch the app so Dock and Launchpad drop any cached icon
  from an older package.
- [ ] Save a proxy address in Settings, then open a new local terminal. Run
  `tmux show-environment -t <session> https_proxy` in it, replacing `<session>`
  with the name the sidebar shows for it. It prints `https_proxy=socks5h://`
  followed by the saved address. A session opened before the address was saved
  prints nothing and says `unknown variable` instead, and so does a new session
  after the address is cleared. Then run a command that honors these variables, such as
  `curl -I https://github.com`, and check that it connects. If your shell
  configuration sets proxy variables of its own, its values are in effect in
  the terminal, which the settings dialog points out.
- [ ] Switch the interface language from English to Chinese and back with the
  sidebar open, in the host manager list and in the add-host form. No button,
  selector or input may change height or width, and no label may wrap or be
  cut off. A longer error or hint may wrap. Terminal connections and unsaved
  editor content survive both switches.

Automated CI and installed-package gates passed at implementation commit
`5f8a6a37ec0e59d4183dbcfb0e14238fea297cd8`; see the final Linux evidence record.
The user subsequently confirmed that the remaining Desktop OpenSpec acceptance
tasks passed and approved closing them.
