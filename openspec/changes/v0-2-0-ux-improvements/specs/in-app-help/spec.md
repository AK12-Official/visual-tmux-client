# in-app-help Delta

## Purpose

Provides in-application access to an embedded tmux guide and brief client usage notes, served entirely from the hub with no external network dependency.

## ADDED Requirements

### Requirement: In-app help access

The client SHALL offer a help control that opens the guide in an overlay without navigating away from the app. The help content SHALL be fetched from the hub's own embedded assets, with no requests to any origin other than the hub. Closing the overlay SHALL return the user to their prior state, with any selected session and terminal attachments intact.

#### Scenario: Opening the guide

- **WHEN** the user activates the help control
- **THEN** the guide opens in an overlay over the current view

#### Scenario: Closing the guide

- **WHEN** the user closes the help overlay
- **THEN** the previous view is restored and an attached terminal, if any, is still connected

#### Scenario: Self-contained content

- **WHEN** the guide is displayed
- **THEN** no request is made to any origin other than the hub itself

### Requirement: Guide content

The embedded guide SHALL cover tmux fundamentals — sessions, windows, panes, the prefix key, copy mode, and configuration — and SHALL include a section on using the client itself: connecting with the token, creating and renaming sessions, and what attaching to, detaching from, and closing a terminal panel mean. The guide SHALL be rendered as formatted rich text (headings, code blocks, tables), not as raw markup.

#### Scenario: tmux fundamentals are covered

- **WHEN** the user opens the guide
- **THEN** it contains sections on sessions, windows, panes, the prefix key, copy mode, and configuration

#### Scenario: Client usage is covered

- **WHEN** the user opens the guide
- **THEN** it contains a section on connecting with the token and on the client's session and terminal actions

#### Scenario: Markup is rendered

- **WHEN** the guide contains headings, code blocks, or tables
- **THEN** they are rendered with styling rather than shown as literal markup characters
