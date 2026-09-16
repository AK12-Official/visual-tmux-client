# AGENTS.md

Repository guidance for coding agents. CLAUDE.md imports this file.

## Develop outside tmux

Start Codex or Claude Code in a terminal outside tmux when developing, testing,
or debugging this project. If the agent is known to be inside tmux, ask the
user to resume outside it before tmux-backed tests or debugging; read-only
review can continue. Never kill or detach the user's session to move the agent.
Clearing TMUX does not move a process out of tmux, and tools may hide TMUX.

Other tmux servers can still exist. This workflow is not a sandbox, and the
following isolation rules remain mandatory even outside tmux.

## Commands

Do not issue ad hoc tmux commands or use pkill, killall, or process-search
pipelines to clean up tmux. Exercise tmux behavior through the existing Go
tests and their isolation helpers. Do not kill unrelated servers to fix a
failed test. These are Agent instructions, not automatic command interception.

## Tests

Which server a tmux command reaches is decided by the first of these that
applies, measured on tmux 3.7b because the intuitive version of this rule is
wrong:

| the command | the server it reaches |
| --- | --- |
| passes `-S <path>` | that path; `TMUX` is ignored |
| passes `-L <name>` | `${TMUX_TMPDIR:-/tmp}/tmux-<uid>/<name>`; `TMUX` is ignored |
| names neither | `TMUX` -- the session the run is inside |
| names neither, with `TMUX` cleared | `${TMUX_TMPDIR:-/tmp}/tmux-<uid>/default` |

So clearing `TMUX` is not isolation: it falls back to the default socket, which
is where a developer's server usually lives. A test is isolated only when it
does all three -- drop `TMUX` and `TMUX_PANE` from the child environment, point
`TMUX_TMPDIR` at a private directory that has been created, and name an explicit
socket with `-S` or `-L`. Cleanup must use the same environment and socket.

- Use `testutil.SocketDir(t)` for sockets, not `t.TempDir()`; macOS limits socket
  path length. Keep socket names short.
- Use existing helpers: `newTestClient` in `hub/internal/tmux`, `testTmuxEnv`
  in `hub/cmd/visual-tmux-client`, and `tmux.ScrubbedEnv()` in product code.
- Do not weaken helpers to pass a test. Any new package that starts tmux needs
  `func TestMain(m *testing.M) { os.Exit(testutil.ParentGuard(m)) }`.
  It is currently installed in the CLI and tmux packages. The testutil package
  tests the guard itself against disposable servers.

ParentGuard detects server/session-list changes after tests; it cannot prevent
all damage or detect every pane/option mutation. Never treat a green test as
proof of complete isolation.
