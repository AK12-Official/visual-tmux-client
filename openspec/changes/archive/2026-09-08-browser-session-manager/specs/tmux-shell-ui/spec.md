## REMOVED Requirements

### Requirement: Session/window/pane navigation tree

**Reason**: The product no longer navigates a three-level tree. Windows and panes are visible inside the attached terminal because tmux renders them, so the client's navigation surface is a flat session list.

**Migration**: Use `web-session-manager`'s "Session list view", which lists sessions with their window counts and attached state.

### Requirement: Geometrically accurate pane rendering

**Reason**: This requirement is the root cause of the change. Positioning one terminal view per tmux pane from mirrored geometry cannot produce correct output, because tmux lays out text for its own notion of the terminal size while the browser sizes each view from CSS — the two never agree, so wrapping and full-screen applications render incorrectly. tmux now renders its own panes into a single correctly-sized terminal.

**Migration**: Use `web-session-manager`'s "Terminal view" and "Viewport-accurate terminal sizing". The pane grid is removed outright; there is no per-pane positioning to migrate.

### Requirement: Live pane output rendering

**Reason**: There is no longer one terminal view per pane to render into, and no per-pane output stream to render. A single terminal view renders the session's whole byte stream.

**Migration**: Use `web-session-manager`'s "Terminal view". The scrollback-seeding behavior is superseded by tmux's own repaint on attach, which — unlike the previous plain-text seeding — preserves color and attributes.

### Requirement: Keyboard input to focused pane

**Reason**: Focus is no longer a client-side concern across multiple terminal views. There is one terminal view per attached session; tmux decides which of its panes receives the input, exactly as for a conventionally attached client.

**Migration**: Use `web-session-manager`'s "Terminal view" (typing and pasting scenarios). Click-to-focus-a-pane is handled by tmux's own mouse support inside the terminal.

### Requirement: Session, window, and pane management actions

**Reason**: Split into two: session management remains a product feature but is now served by an authenticated server API with validation and confirmation semantics, while window and pane management leaves the product entirely — users perform those with tmux's own key bindings.

**Migration**: For sessions, use `web-session-manager`'s "Session creation, renaming, and termination" and `session-hub`'s corresponding operations. For windows and panes, none: users manage them through tmux directly.
