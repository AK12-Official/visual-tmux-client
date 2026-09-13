## ADDED Requirements

### Requirement: Mouse and scroll support configuration

The hub SHALL ensure that the tmux server is configured with mouse support enabled (`mouse on`), so that attached terminal clients receive terminal mouse reporting sequences and can scroll through session scrollback history using mouse wheel and trackpad gestures. The hub SHALL verify whether mouse support is already active before issuing option modification commands to prevent spurious client repaints and unintended activity triggers.

#### Scenario: Initial attachment configures mouse on

- **WHEN** a caller attaches to a tmux session on a server where mouse support is not yet active
- **THEN** the hub sets the global mouse option to on (`set-option -g mouse on`) and terminal mouse reporting is activated

#### Scenario: Subsequent attachment does not trigger spurious repaints

- **WHEN** a caller attaches to a tmux session on a server where mouse support is already active
- **THEN** the hub does not re-issue the `set-option` command and existing clients are not repainted

#### Scenario: Session creation configures mouse on

- **WHEN** a session is created via the hub API
- **THEN** the hub ensures the global mouse option is enabled on the tmux server
