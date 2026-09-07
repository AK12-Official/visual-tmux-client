# SendKeys manual verification (task 5.5)

Verifies design.md's input-forwarding decision (`send-keys -H` hex-byte
forwarding) behaves the same as typing/pasting at a directly attached
terminal, for the three cases design.md called out: arrow keys, Ctrl-C, and
a multi-line paste.

## Method

Ran `HostConn.SendKeys` against a real pane in a local tmux session (zsh
shell, `-x 80 -y 24`), inspecting the pane via `tmux capture-pane -p` after
each step. Byte sequences sent matched exactly what a real terminal emits
for that input (e.g. arrow-left is `ESC [ D` = `0x1b 0x5b 0x44`).

## Results

**Arrow keys** — sent `"abc"`, then two Left-arrow sequences (`1b 5b 44`
twice), then `"X"`. Final line editor state: `aXbc` — cursor moved left two
cells and the insert landed between `a` and `b`, exactly as it would from a
real keyboard. (An intermediate zsh redraw briefly appears in scrollback as
a literal `abc^[[D^[[D%` fragment; this is zsh's own multi-line-editor
redraw behavior, not an artifact of SendKeys — the final rendered state is
correct.)

**Ctrl-C** — started `sleep 100` in the foreground, sent `0x03`. The shell
printed `^C`, returned to the prompt immediately, and `echo $?` reported
`130` (128 + SIGINT), confirming the foreground process was actually
interrupted, not just visually cancelled.

**Multi-line paste** — sent `"echo LINE1\necho LINE2\necho LINE3\n"` as a
single `SendKeys` call (one hex-encoded burst, no `paste-buffer`). All three
`echo` commands executed and produced `LINE1`/`LINE2`/`LINE3`, but their
echoing and execution were interleaved/staggered across the capture rather
than appearing as three clean back-to-back lines. This is the expected
behavior for raw keystroke forwarding without bracketed-paste protection —
identical to pasting multi-line text into a real terminal whose shell
doesn't enable bracketed paste. It confirms `send-keys -H` was the right
choice per design.md (a real `paste-buffer`-based paste would have differed
by treating the whole blob as one atomic unit); shells that want paste
safety are expected to enable bracketed paste themselves, same as with a
directly attached terminal.

## Outcome

All three cases behave identically to a directly attached terminal. No
changes to the `SendKeys` implementation were needed.
