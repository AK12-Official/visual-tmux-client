# Pane-grid re-layout verification (task 8.3)

Verifies that splitting a pane on the underlying tmux server updates the
shell's pane grid live -- without a full window/app reload -- by way of
the existing `window.layout-changed` lifecycle event.

## Method

No new code was required. The wiring already exists from earlier tasks:

- `host.go`'s `handleNotification` publishes `eventbus.WindowLayoutChanged`
  (`"window.layout-changed"`) whenever tmux emits a `%layout-change`
  notification for a window (task 2.3's line, confirmed via grep at
  `engine/tmuxconn/host.go:772`).
- `App.vue`'s single `engine:lifecycle` subscription (task 7.2) treats
  `window.layout-changed` as one of the `TREE_RELEVANT_LIFECYCLE_EVENTS`
  and calls `refresh()`, which re-fetches `Sessions()` and passes the
  updated domain model down to `PaneGrid` as props.
- `PaneGrid.vue`'s `watch(() => [props.sessions, props.selectedWindowId],
  render, { deep: true, immediate: true })` (task 8.1) re-runs `render()`
  on any such prop change. `render()` calls `pruneDeadPanes()` (removes
  only panes no longer in the domain model) and `ensureTracked()` (reuses
  an existing pane's `Terminal` instance if already registered, only
  creating a new one for a genuinely new pane ID) -- so a split's second,
  newly-created pane gets a fresh `Terminal`, while the original pane's
  `Terminal` instance, scrollback, and live-output subscription are left
  completely undisturbed.

This was verified end-to-end rather than accepted from code review alone,
per the task's explicit instruction to "verify by splitting a pane on the
underlying tmux server and observing the grid update."

Per the standing safety rule (this tooling runs inside a default-socket
tmux session that must not be disturbed), built an isolated single-pane
server on a dedicated, non-default socket:

```
tmux -L layoutverify kill-server        # cleanup from any prior run
tmux -L layoutverify new-session -d -s main -x 100 -y 30
```

Confirmed via `tmux -L layoutverify list-panes` a single pane (`%0 0,0
100x30`) and via `echo $TMUX` / `tmux list-sessions` that the agent's own
default-socket session (`session-20260907-152842`) and the unrelated
`smoketest` session were untouched.

Ran the real product shell (`wails dev`), connected to socket
`layoutverify` (confirmed via screenshot: session "MAIN", single pane
`%0 zsh`), then -- from a separate shell, not through the app -- ran:

```
tmux -L layoutverify split-window -t main -h
```

and confirmed engine-side via `tmux -L layoutverify list-panes -t main -F
'#{pane_id} #{pane_left},#{pane_top} #{pane_width}x#{pane_height}
active=#{pane_active}'`:

```
%0 0,0 50x30 active=0
%1 51,0 49x30 active=1
```

### Screen-automation hazard (new this run)

Reusing the AX-set + real-keystroke-append/backspace technique from
`pane-output-verification.md` to type the socket name into the field
(needed again here because the active input source was still the Chinese
Pinyin IME), a first attempt produced a corrupted value
`"layoutverif1"` -- the trailing `"y"` was lost rather than the `"1"`
being cleanly appended after it. Root cause: `set value of <text field>
to "..."` via AppleScript does not reliably leave the text-insertion
cursor positioned at the end of the field afterward, so the following
real keystroke landed mid-string instead of appending. Fixed by inserting
an explicit End-key press (`key code 119`) immediately after the
`set value of` call and before the keystroke-append/backspace pair,
guaranteeing the cursor is at the end of the field first. Re-ran and
confirmed via screenshot a clean, uncorrupted `"layoutverify"` value
before clicking Connect. Purely a screen-automation workaround, recorded
here (as with the IME note in `pane-output-verification.md`) since it is
safety-relevant to this verification methodology, not a change to the
app itself.

## Outcome

A screenshot taken after the split (and after the engine-side
`list-panes` confirmation above) showed the shell's pane grid displaying
two panes side by side -- a wider pane on the left and a narrower one on
the right, matching the tmux-reported geometry (`%0` 50-wide vs `%1`
49-wide) -- with no manual refresh, window reload, or re-connect
performed. The grid updated live purely from the `window.layout-changed`
event flowing through `App.vue`'s `refresh()` into `PaneGrid`'s reactive
`watch`.

Disconnected, then tore down the isolated server (`tmux -L layoutverify
kill-server`) and confirmed via `tmux -L layoutverify list-sessions`
that it was fully gone, and via `echo $TMUX` / `tmux list-sessions` that
the agent's own default-socket session (`session-20260907-152842`) and
`smoketest` were unaffected throughout. Stopped the `wails dev` process
and the compiled app, confirmed via `ps aux` that no leftover processes
remained.
