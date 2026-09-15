# Review notes — session pane subtitle

> **Completed 2026-09-15.** All three browser checks (4.3, 5.1, 6.4) passed. This
> file is kept as the recipe for rebuilding the review environment if the feature
> is ever re-verified.

Three items (4.3, 5.1, 6.4) needed a human looking at a browser, plus one
presentation fix that came out of the first review round. Everything else is
covered by `make test`, `make lint`, and the live API check recorded in
`tasks.md`.

## 0. Already running — just open the browser

The isolated server and hub are **already up** as of this writing:

- hub: <http://127.0.0.1:7699> — token `review-token`
- throwaway tmux server under `/tmp/vtc-review` (your own tmux is untouched)

Go straight to [§3 What to check](#3-what-to-check). Sections 1–2 are how to
rebuild the environment if you stop it or need to start over.

To stop everything: kill the hub (`Ctrl+C`, or `pkill -f vtc-review-hub`) and
run `./scripts/isolated-tmux.sh teardown`.

## 1. Set up an isolated server

Do **not** point the hub at your own tmux server — it would list and be able to
kill the sessions you are working in. The commands below pin both the test
sessions and the hub to a throwaway server under `/tmp`.

```sh
cd /Users/ump45/Desktop/DEV/visual-tmux-client

# Both the wrapper and the hub must agree on the server. The hub uses tmux's
# default socket name, so pin the wrapper to it too.
export VTC_TMUX_TMPDIR=/tmp/vtc-review
export VTC_TMUX_SOCKET=default

./scripts/isolated-tmux.sh new-session -d -s plain-shell -x 100 -y 30
./scripts/isolated-tmux.sh new-session -d -s split-demo  -x 100 -y 30
./scripts/isolated-tmux.sh split-window -h -t '=split-demo:'

# Give the two panes different commands so it is visible which one was chosen,
# and make the second pane the active one.
./scripts/isolated-tmux.sh send-keys -t split-demo:0.0 'exec sleep 300' Enter
./scripts/isolated-tmux.sh send-keys -t split-demo:0.1 'exec tail -f /etc/hosts' Enter

# A session whose only pane has exited (no subtitle expected).
./scripts/isolated-tmux.sh set-option -g remain-on-exit on
./scripts/isolated-tmux.sh new-session -d -s ended -x 100 -y 30 "sh -c 'exit 1'"

# Optional: a session running a program that sets its own pane title.
# Use whichever you have (nvim / vim / top). Skip if none.
./scripts/isolated-tmux.sh new-session -d -s titled -x 100 -y 30 vim
```

Confirm what the server holds:

```sh
./scripts/isolated-tmux.sh list-panes -a -F \
  '#{session_name} dead=#{pane_dead} active=#{pane_active} name=#{window_name}'
```

## 2. Start the hub against it

```sh
cd hub
TMUX_TMPDIR=/tmp/vtc-review \
VISUAL_TMUX_CLIENT_TOKEN=review-token \
VISUAL_TMUX_CLIENT_ADDR=127.0.0.1:7699 \
go run ./cmd/visual-tmux-client
```

Then open <http://127.0.0.1:7699> and enter `review-token`.

## 3. What to check

### 6.4 — the subtitle itself

These are the exact values the hub returns for the setup above (verified against
the API; `<host>` is your own hostname, which the terminal sets as the pane title):

| Session | Expected subtitle | Why |
|---|---|---|
| `plain-shell` | `zsh*: CLEONZHAO-MC0` | single live pane; `*` marks the active window |
| `split-demo` | `tail*: CLEONZHAO-MC0` | the **active** pane is pane 1 running `tail`, not pane 0 running `sleep` — this is the ranking |
| `ended` | *(no subtitle line at all)* | its only pane has exited, so there is nothing to summarise |
| `titled` | whatever `vim` sets as its title | proves the title branch of the precedence |

The important one is `split-demo`: if it ever shows `sleep`, the ranking is
wrong. Everything else depends on your shell and terminal, so check the *shape*
(`window*: title`) rather than the literal text.

### 4.3 — a row without a subtitle

Look at `ended`: it must render **no** subtitle line and no blank gap — the row
should collapse to just the session name. Confirm the pin/rename/kill buttons
work on every row.

Also confirm the row no longer shows the old `1 window · attached` line on **any**
session — window count and attached state were removed at review.

### 5.1 — layout with a long title

Create a session whose title is very long, then check the row does not grow:

```sh
./scripts/isolated-tmux.sh new-session -d -s long-title -x 100 -y 30
./scripts/isolated-tmux.sh send-keys -t long-title:0.0 \
  "printf '\033]2;%s\033\\\\' \"$(printf 'x%.0s' {1..300})\"; exec sleep 300" Enter
```

Expected: the subtitle stays on one line and ellipsises, the row height is
unchanged, and the text never runs under the action buttons. Compare row heights
across all sessions — they should look uniform.

### Ended-state placement

Select `ended` (the session with no subtitle). Its status message — “Session
"ended" has ended.” plus any detail — must appear in the **top-left** of the
terminal area, where the session's own output would have been, not centred in
the middle. It should read as the last thing the terminal printed rather than as
a dialog box.

## 4. Teardown

```sh
./scripts/isolated-tmux.sh teardown     # kills only the throwaway server
unset VTC_TMUX_TMPDIR VTC_TMUX_SOCKET
```

Then stop the hub with `Ctrl+C`.

## Reporting back

If something looks wrong, the useful details are: which session, the actual
subtitle text, and a screenshot. If the subtitle is missing entirely, run
`./scripts/isolated-tmux.sh list-panes -a -F '#{session_name} #{pane_dead} #{pane_active} #{window_name} #{pane_title} #{pane_current_command}'`
and include the output.
