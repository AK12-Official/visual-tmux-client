# Pane grid verification (task 8.1)

Verifies that the pane grid's rendered layout visually matches a real
multi-pane split window's actual on-screen geometry, sourced purely from
the domain model's per-pane `X/Y/Width/Height` fields (task 2.1/2.3), not
by reparsing the raw tmux layout string in the frontend (design.md
explicitly rejects that alternative).

## Method

Added `src/components/PaneGrid.vue`: one `@xterm/xterm` `Terminal`
instance per pane, created once and kept mounted for the pane's full
lifetime (design.md: window switches show/hide rather than
destroy/recreate). Each pane's terminal container is absolutely
positioned as a percentage box derived from
`totalWidth = max(p.X + p.Width)` / `totalHeight = max(p.Y + p.Height)`
across the selected window's panes. Wired into `App.vue` alongside the
existing `NavigationTree`, sharing the same `sessions`/`selectedWindowId`
state; added `reconcileSelectedWindow()` so the grid falls back to the
active window of the first session on connect/refresh instead of showing
nothing until a manual tree click.

Per the standing safety rule (the tooling driving this verification runs
inside a default-socket tmux session that must not be disturbed), built
an isolated server on a dedicated, non-default socket:

```
tmux -L panegridverify new-session -d -s main -x 120 -y 40
tmux -L panegridverify new-window -t main -n editor
tmux -L panegridverify split-window -t main:editor -h
tmux -L panegridverify split-window -t main:editor.1 -v
```

This produced window `editor` with three panes: `%1` (0,0, 60x40, full
height, left half), `%2` (61,0, 59x20, top right), `%3` (61,21, 59x19,
active, bottom right) — confirmed via `tmux -L panegridverify list-panes
-t main:editor -F '#{pane_id} #{pane_left},#{pane_top}
#{pane_width}x#{pane_height} active=#{pane_active}'`.

Ran the real product shell (`wails dev`), connected to socket
`panegridverify`, and selected window `editor` in the nav tree — all
driven via screen automation, confirmed via `echo $TMUX` / `tmux
list-sessions` before and after that the agent's own default-socket
session was never touched.

Note: an earlier verification attempt hit an unrelated tooling problem —
the accessibility-automation text input occasionally corrupted or
silently rejected hyphen-containing strings typed into the socket-name
field — which cost significant time but is a screen-automation quirk,
not a bug in the app. Worked around by using a hyphen-free socket name
(`panegridverify`) for this verification; not a change to the app itself.

## Outcome

The rendered grid showed three terminal boxes whose position and size
visually matched the real layout exactly: one box spanning the full
height on the left (~50% width), and two boxes stacked top/bottom on the
right (~50% width, roughly even split, top slightly taller matching
20 vs. 19 rows), with the bottom-right box (pane `%3`, the active pane)
highlighted with the `pane-grid__term--active` blue border and the other
two unbordered — correctly reflecting `pane_active` from the real
server.

Disconnected, then tore down the isolated server (`tmux -L
panegridverify kill-server`) and confirmed via `echo $TMUX` and `tmux
list-sessions` that the agent's own default-socket session
(`session-20260907-152842`) was unaffected and the verification server is
fully gone.

Live re-layout on split/resize (task 8.3) and output streaming (task 8.2)
are out of scope here — this task only verifies the grid's geometry for
an already-stable layout.
