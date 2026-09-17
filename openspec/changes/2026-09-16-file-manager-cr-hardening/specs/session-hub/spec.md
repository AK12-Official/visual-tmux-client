## ADDED Requirements

### Requirement: Pane record framing

The hub SHALL return each tmux session together with a summary of its representative live pane. Pane fields SHALL be transported from tmux with each value prefixed by its byte length, so free-form values are never parsed as field separators or record terminators. A newline, colon, or string shaped like another pane record inside a session name, window name, title, or current command SHALL remain part of that field and SHALL NOT create or shift a record. A malformed frame SHALL stop parsing rather than be guessed at.

#### Scenario: Pane field contains record syntax

- **WHEN** a pane field contains a newline followed by text shaped like a complete record for another session
- **THEN** the parser returns the text inside the original field and creates no record for the named session

#### Scenario: Malformed pane frame

- **WHEN** tmux output does not contain the number of bytes its field prefix declares
- **THEN** the parser stops without attributing the remaining bytes to any pane
