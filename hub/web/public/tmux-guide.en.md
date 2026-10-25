# tmux User Guide

> This guide is built into Visual Tmux Client for convenient in-app reference.
> The tmux sections are adapted from the official tmux Wiki Getting Started documentation:
> <https://github.com/tmux/tmux/wiki/Getting-Started>

---

## 1. Using Visual Tmux Client

This application is a browser-based tmux client: the session list is on the left, and a real terminal is on the right. When you attach, you are connected to a genuine tmux session: **all tmux keybindings work as normal**, and pane splits and the status bar are drawn by tmux itself.

### Connecting

After starting the server, the terminal displays an access token and address (e.g., `http://127.0.0.1:7690`). Open this address in your browser and enter the token to connect. The token is saved in your browser's local storage; refreshing the page does not require re-entering it.

### Session Management (Left Sidebar)

| Action | How-To |
|--------|--------|
| New session | Click `+ New session` to create immediately with a default name (`session-date-time`) |
| Rename | Click ✎ on the card; supports non-ASCII characters such as Chinese (up to 64 characters), cannot contain `:`, `.`, `/`, `\`, `;`, full-width `：`/`．`/`；`, or leading/trailing whitespace |
| Kill session | Click ✕ on the card (with confirmation) |
| Batch management | Click `Select` to enter multi-select mode and kill multiple sessions at once |
| Sorting | Toggle `Default / Manual`; in manual mode, drag to reorder and click ★ to pin to top (saved locally in browser) |

### Terminal Panel (Right)

Click any session on the left to attach. From left to right on the top bar: activity indicator dot, session name, connection status; on the right: `A−` / font size / `A+` font controls, `Files` to open the file manager, `⛶` for fullscreen, and `✕` to close the panel.

### File Manager

Click `Files` on the top bar to open the manager inside the terminal area without obscuring the left session list. It opens in the working directory of the current session's active pane and lets you browse freely—switching panes later will not jump the opened file manager.

The left directory tree loads subdirectories on demand. The right panel can open multiple files at the same time, with each tab independently tracking unsaved modifications (`●` next to the filename). Saving includes the modification timestamp from when the file was opened; if modified externally in the interim, the hub rejects the save and asks whether to overwrite.

Markdown files open in source mode by default; switch to `Preview` to view rendered content (sanitized before injection; scripts and external resource loading are stripped). Images preview directly; binary or oversized files display metadata and a download button rather than opening as text.

Right-click any item in the directory tree to create files/directories, rename, delete (with confirmation), download, copy paths, and **insert the path into the terminal input line**—this inserts a plain text path without a trailing newline, so it will not execute automatically; paths containing spaces are automatically quoted. If no terminal is currently attached, a notice is displayed rather than failing silently.

### Detach, Close, and Kill

| Action | Does the session keep running? |
|--------|--------------------------------|
| `C-b d` Detach | Yes. The panel shows Detached; click Reconnect to reattach |
| `✕` Close panel | Yes. Merely collapses the view; when output occurs, the session card lights up green |
| `✕` Kill | **Terminated**. All windows and panes are destroyed |

### Activity Indicators

When a background session produces output, its card lights up with a green border. When the currently viewed session produces output, a pulsing dot appears in the top bar, and `●` appears before the browser tab title. Indicators clear automatically ~2 seconds after output stops.

---

## 2. What is tmux

tmux is a **terminal multiplexer** that runs inside a terminal and allows multiple terminal programs to run simultaneously. Its core benefits include:

- **Disconnection prevention**: Run programs on a remote server with tmux; even if your SSH connection drops, processes continue running in the background.
- **Multi-client access**: Start a session at work and reconnect from home to resume right where you left off.
- **Multi-window management**: Manage multiple programs and shells in a single terminal window, like a window manager.

---

## 3. Core Concepts

tmux has an essential hierarchy of terminology: **Server → Session → Window → Pane**.

| Term | Description |
|------|-------------|
| **Server** | The main tmux daemon process; all state is preserved here, runs in the background, communicates with clients via a socket under `/tmp` |
| **Client** | A process started when a user attaches to the server, occupying an external terminal |
| **Session** | A collection of windows with a unique name, attachable by one or more clients, or detached completely |
| **Window** | A collection of panes occupying the full terminal, indexed by number within the session |
| **Pane** | A rectangular area in a window containing a terminal and running program |
| **Active pane** | The pane currently receiving your input in the current window, indicated by a green border |
| **Current window** | The window currently displayed and receiving input in the session |
| **Prefix key** | The special key combination that triggers tmux commands, default is `C-b` (Ctrl+b) |

> Memory chain: Programs run inside **Panes** → Panes belong to a **Window** → Windows belong to a **Session** → Sessions are attached by **Clients**.

---

## 4. Status Line

When attached to tmux, a green status bar appears at the bottom of the screen showing:

- **Left**: Current session name, e.g. `[0]`
- **Middle**: Window list (`index:name`), current window marked `*`, previous window marked `-`
- **Right**: Pane title (defaults to hostname) + date and time

When there are too many windows to fit, `<` or `>` indicators appear at the edge.

---

## 5. Session Management

### Creating Sessions

```bash
$ tmux new                      # Create default session (named 0) and attach
$ tmux new -s mysession         # Create session named mysession
$ tmux new 'emacs ~/.tmux.conf' # Create session and run specified command
$ tmux new -n mytopwindow top   # Name the window with -n and run top
```

### Detaching and Attaching

- **Detach**: `C-b d` — exit client; session and programs continue in background
- **Attach to last session**: `tmux attach` or `tmux a`
- **Attach to named session**: `tmux attach -t mysession`
- **Detach other clients when attaching**: `tmux attach -d -t mysession`
- **Create if not found, attach if found** (most common): `tmux new -As mysession`

### Listing and Killing Sessions

```bash
$ tmux ls                                  # List all sessions
$ tmux kill-server                         # Kill tmux server completely
```

In the command prompt, you can also enter: `:kill-server`

---

## 6. Prefix Key and Help

- **Default prefix key**: `C-b` (Ctrl+b)
- Key notation: `C-` = Ctrl, `M-` = Alt/Option, `S-` = Shift
- **`C-b c`** means press `C-b`, release, then press `c` (be sure to release Ctrl before typing `c`)
- Pressing **`C-b` twice** sends a literal `C-b` to the program in the active pane

### Getting Help

| Key | Action |
|-----|--------|
| `C-b ?` | List all keybindings with descriptions (scroll with ↑↓, exit with `q`) |
| `C-b /` | Look up description for a specific key |
| `C-b :` | Open command prompt to run tmux commands directly (separate multiple commands with `;`) |

---

## 7. Window Management

| Key / Command | Action |
|---------------|--------|
| `C-b c` | Create new window |
| `C-b 0` ~ `C-b 9` | Switch to window 0~9 |
| `C-b '` | Enter index number to switch window |
| `C-b n` / `C-b p` | Next / previous window |
| `C-b l` | Switch to last visited window |
| `C-b &` | Close current window (with confirmation) |
| `C-b ,` | Rename current window |
| `C-b .` | Move/re-index current window |
| `C-b w` | Tree view of all sessions/windows/panes |
| `C-b s` | List and select sessions |

Command prompt examples:

```
:neww -dn mynewwindow     # Create window named mynewwindow in background
:neww top                 # Create new window running top
:movew -r                 # Renumber windows to close index gaps
```

---

## 8. Pane Operations

### Splitting

| Key | Action |
|-----|--------|
| `C-b %` | Split left/right (vertical split) |
| `C-b "` | Split top/bottom (horizontal split) |

### Switching and Viewing

| Key | Action |
|-----|--------|
| `C-b ↑↓←→` | Switch to pane above/below/left/right (wraps around) |
| `C-b q` | Briefly show pane numbers; press number to jump |
| `C-b o` | Switch to next pane in index order |
| `C-b C-o` | Swap current pane with next pane |

### Resizing and Zooming

| Key | Action |
|-----|--------|
| `C-b C-↑↓←→` | Small resize adjustments |
| `C-b M-↑↓←→` | Larger resize adjustments |
| `C-b z` | Toggle pane zoom/fullscreen (status line shows `Z`) |
| `C-b Space` | Cycle through preset layouts |
| `C-b {` / `C-b }` | Swap with previous / next pane |

### Preset Layouts

| Key | Layout |
|-----|--------|
| `C-b M-1` | even-horizontal (horizontal even split) |
| `C-b M-2` | even-vertical (vertical even split) |
| `C-b M-3` | main-horizontal (large pane on top) |
| `C-b M-4` | main-vertical (large pane on left) |
| `C-b M-5` | tiled |

### Closing

| Key | Action |
|-----|--------|
| `C-b x` | Close current pane (with confirmation) |

---

## 9. Copy and Paste

tmux provides its own clipboard buffer system called **paste buffers**.

### Entering Copy Mode

- `C-b [` enters copy mode (freezes output, scroll back and select text)
- `C-b ]` pastes the most recently copied buffer
- `C-b =` opens buffer mode, listing all buffers with preview to paste or delete selectively

Copy mode defaults to **emacs style** keys; if your `VISUAL`/`EDITOR` environment variable contains `vi`, it uses **vi style**. Common emacs keys:

| Key | Action |
|-----|--------|
| `C-Space` | Start selection |
| `C-w` | Copy selection and exit copy mode |
| `C-g` | Cancel selection |
| `C-a` / `C-e` | Beginning of line / end of line |
| `C-r` | Reverse incremental search |
| `q` | Exit copy mode |

Buffer management commands (in `:` prompt):

```
:setb -bfoo bar              # Create buffer named foo with text bar
:saveb -bbuffer0 ~/saved     # Save buffer contents to file
:loadb -bbuffername ~/a/file # Load buffer from file
```

> Automatic copy buffers keep up to 50 entries (oldest discarded); named buffers are not deleted automatically. You can also configure tmux to synchronize with the system clipboard.

---

## 10. Mouse and Scrolling Support

Visual Tmux Client **automatically enables mouse support** (`set -g mouse on`) upon connecting and creating sessions, with no manual setup needed. To enable manually in a native terminal, enter `:set -g mouse on` in the `:` prompt.

With mouse mode enabled:

- **Mouse wheel / trackpad scroll** → When session output exceeds one screen, scroll up to view history (tmux enters copy mode automatically); scroll down to the bottom to exit copy mode and return to the interactive command line.
- **Left click pane** → Switch active pane
- **Left click window name in status line** → Switch window
- **Drag pane border with left button** → Resize panes
- **Drag inside pane with left button** → Select text (releasing copies to tmux buffer)
- **Hold Shift while dragging** → Select text in browser (use with `Ctrl+C` / `Cmd+C` to copy to system clipboard)
- **Right click pane** → Open context menu

---

## 11. Configuration File `~/.tmux.conf`

The tmux server reads `~/.tmux.conf` **when starting up** (only at server start, not when creating each session). Reload while running:

```
:source ~/.tmux.conf
```

Lines starting with `#` are comments, with one command per line. Common configuration examples:

### 1. Change Prefix Key to `C-a`

```
set -g prefix C-a
unbind C-b
bind C-a send-prefix
```

### 2. Enable Mouse and vi Mode

```
set -g mouse on
set -g mode-keys vi
set -g status-keys vi
```

### 3. Customize Status Line

```
set -g status-position top
set -g status-style bg=blue
set -g status-right '%H:%M'
set -g window-status-current-style underscore
```

### 4. Pane Borders

```
set -g pane-border-style fg=red
set -g pane-active-border-style 'fg=red,bg=yellow'
set -g pane-border-status top
set -g pane-border-format '#[bold]#{pane_title}#[default]'
```

---

## 12. Options System

tmux options fall into several scopes:

- **Server options** (`-s`): Affect the entire server
- **Session options** (`-g` global session): Affect sessions
- **Window options** (`-wg`): Affect windows
- **Pane options**: Affect panes

Common operations:

```bash
$ tmux show -s              # View server options
$ tmux show -g              # View global session options
$ tmux show -wg             # View global window options
$ tmux show -g status       # View single option
```

Setting options (in config file or `:` prompt):

```
set -g status off              # Turn off status bar
set -s default-terminal 'tmux-256color'
set -gu status                 # Unset, restoring default
```

### Quick Reference of Common Options

| Option | Type | Description |
|--------|------|-------------|
| `prefix` | session | Prefix key, defaults to `C-b` |
| `base-index` | session | Window starting index (default 0, often set to 1) |
| `history-limit` | session | Scrollback history lines per pane |
| `mouse` | session | Whether mouse support is enabled |
| `mode-keys` | window | Copy mode keybindings (emacs/vi) |
| `status` | session | Whether status bar is visible |
| `status-position` | session | Status bar position (top/bottom) |
| `renumber-windows` | session | Automatically renumber windows when closed |
| `escape-time` | server | Escape key wait time in ms (often set to 0) |
| `default-terminal` | server | Internal `TERM` value |
| `synchronize-panes` | window | Send keystrokes to all panes simultaneously |
| `set-clipboard` | server | Whether to synchronize with system clipboard |

---

## 13. Styles and Formats (Advanced)

### Color Styles

Colors accept `black red green yellow blue magenta cyan white`, `brightred`, `colour0`~`colour255`, or hex codes like `#882244`.

```
set -g status-style 'bg=blue'
set -g status-left 'default #[fg=red] red #[fg=blue] blue'
set -g status-left-length 100
```

`#[]` specifies inline styling, `#{}` specifies format variables (e.g. `#{session_name}`), and `#()` runs embedded shell commands (e.g. `#(uptime)`).

---

## 14. Quick Reference Cheat Sheet

| Action | Keybinding |
|--------|------------|
| Create session | `tmux new -s name` |
| Attach session | `tmux a -t name` |
| Detach | `C-b d` |
| List sessions | `C-b s` or `tmux ls` |
| New window | `C-b c` |
| Switch window | `C-b number` / `C-b n` / `C-b p` |
| Split left/right | `C-b %` |
| Split top/bottom | `C-b "` |
| Switch pane | `C-b arrow keys` |
| Zoom pane | `C-b z` |
| Close pane | `C-b x` |
| Copy mode | `C-b [` |
| Paste | `C-b ]` |
| Command prompt | `C-b :` |
| Help | `C-b ?` |

---

## References

- [tmux Wiki - Getting Started](https://github.com/tmux/tmux/wiki/Getting-Started)
- [tmux Manual - man.openbsd.org/tmux](https://man.openbsd.org/tmux)
