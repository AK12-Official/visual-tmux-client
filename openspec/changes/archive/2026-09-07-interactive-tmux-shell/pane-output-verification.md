# Pane output verification (task 8.2)

Verifies that each pane's xterm.js `Terminal` is seeded with its existing
scrollback on first display, and that subsequent live output arrives and
renders in real time, with no ordering hazard between the two.

## Method

Extended `src/components/PaneGrid.vue`'s per-pane `TrackedPane` record
with `ready: boolean` and `pending: Uint8Array[]`. When a pane's
`Terminal` is first created, it kicks off an async `PaneScrollback(paneID)`
call (base64 -> `Uint8Array`, written straight into the terminal); until
that fetch resolves, `ready` stays `false` and any `engine:pane-output`
bytes that arrive for that pane in the meantime are queued in `pending`
(preserving arrival order) rather than written immediately. Once the
scrollback write lands, the queue is flushed once, in order, and `ready`
flips to `true` so all further output is written straight through. This
guarantees a pane's history always renders before any output that raced
ahead of it, which was flagged as a hazard in `xterm-bridge-verification.md`
(task 6.2).

A single `EventsOn('engine:pane-output', ...)` subscription is made once,
in `onMounted`, for the component's whole lifetime (mirroring `App.vue`'s
`engine:lifecycle` pattern) and covers *all* pane IDs unconditionally,
filtering by `evt.paneId` against the live `registry` map downstream --
this avoids a subscribe-after-pane-exists race for a pane that starts
producing output before this component has finished creating its
`TrackedPane` entry.

Per the standing safety rule (the tooling driving this verification runs
inside a default-socket tmux session that must not be disturbed), built
an isolated server on a dedicated, non-default socket, pre-seeded with a
recognizable marker *before* the app ever connects (to exercise the
"existing scrollback shown on first display" scenario specifically):

```
tmux -L paneoutputverify new-session -d -s main -x 100 -y 30
tmux -L paneoutputverify send-keys -t main "echo preexisting-scrollback-marker" Enter
```

Confirmed via `tmux -L paneoutputverify capture-pane -p -t main` that the
marker was present in pane `%0`'s scrollback prior to connecting.

Ran the real product shell (`wails dev`), connected to socket
`paneoutputverify`, and observed the rendered pane -- all driven via
screen automation, confirmed via `echo $TMUX` / `tmux list-sessions`
before and after that the agent's own default-socket session
(`session-20260907-152842`) was never touched.

Note: this verification run hit a screen-automation obstacle worth
recording since it's a closer cousin of the hyphen-corruption quirk noted
in `pane-grid-verification.md` -- the macOS input source active during
this session was a Chinese Pinyin IME, which intercepted simulated
keystroke-based typing of the plain-Latin socket name and turned it into
pinyin composition fragments (e.g. "paneoutputverify" typed as keystrokes
became "pa ne out pu t v e ri f y" plus a live candidate popup), rather
than inserting the literal text. Setting the field's accessibility value
directly (`set value of <text field> to "paneoutputverify"` via
AppleScript/System Events) bypassed the IME cleanly, but raised a
follow-up concern worth spelling out: `set value of` on a WebKit-hosted
`<input>` might only change the AX-visible value without dispatching the
DOM `input` event Vue's `v-model` listens for, leaving the reactive
`socketName` ref stale (empty) underneath a correct-looking field --
which would be actively dangerous here, since an empty socket name is
exactly what `tmuxconn.Connect` treats as tmux's default socket. Resolved
by appending one harmless real keystroke (`"1"`) after the AX-set, then
deleting it with a real backspace keystroke: both are genuine DOM
`input` events, and since a browser's input event always carries the
full current field value (not just the changed character), the backspace
event's payload was the complete, correct string
(`"paneoutputverify"`), confirming it actually reached Vue's reactive
state before "Connect" was ever clicked. Not a change to the app itself
-- purely a screen-automation workaround, recorded here since it's
directly safety-relevant.

## Outcome

Immediately upon connecting and the window's pane becoming visible, the
rendered xterm.js pane showed the pre-existing `echo
preexisting-scrollback-marker` command and its `preexisting-scrollback-marker`
output line -- confirming scrollback seeding on first display.

Then ran, from a separate shell, `tmux -L paneoutputverify send-keys -t
main "echo LIVE-OUTPUT-STREAM-TEST-<timestamp>" Enter`; the command and
its output appeared live in the same rendered pane, directly below the
seeded scrollback and in correct order -- confirming live output
streaming with no interleaving or reordering against the seeded history.

Disconnected, then tore down the isolated server (`tmux -L
paneoutputverify kill-server`) and confirmed via `echo $TMUX` and `tmux
list-sessions` that the agent's own default-socket session
(`session-20260907-152842`) was unaffected throughout and the
verification server is fully gone.
