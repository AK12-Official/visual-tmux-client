# web-notifications Specification

## Purpose

Surfaces action results and terminal events to the user as short-lived, leveled toast messages that can coexist, replacing the single-slot notice banner.

## Requirements

### Requirement: Toast notifications

The client SHALL surface user-facing messages as toast notifications. Multiple notifications SHALL be able to coexist, and a newly raised notification SHALL push earlier ones aside in the stack. Each notification SHALL carry exactly one level — error, warning, or info — and the levels SHALL be visually distinguishable from one another at a glance, not merely by wording. Each notification SHALL disappear automatically after a lifetime appropriate to its level, without requiring user action, and the user MAY dismiss any notification manually before then. The stack SHALL be bounded: when raising a notification would exceed the bound, the oldest notification is dropped to make room.

#### Scenario: Multiple notifications coexist

- **WHEN** a second message is raised while a first is still visible
- **THEN** both notifications are visible at the same time

#### Scenario: A new notification pushes the stack

- **WHEN** a notification is raised while others are visible
- **THEN** the earlier notifications are pushed aside to make room for the new one

#### Scenario: Levels are visually distinct

- **WHEN** error, warning, and info notifications are shown together
- **THEN** each is distinguishable by its visual treatment alone

#### Scenario: Automatic expiry

- **WHEN** a notification has been visible for its level's lifetime and the user has not interacted with it
- **THEN** it disappears without user action

#### Scenario: Manual dismissal

- **WHEN** the user dismisses a notification
- **THEN** it disappears immediately and the remaining notifications stay visible

#### Scenario: The stack is bounded

- **WHEN** more notifications are raised than the stack allows
- **THEN** the oldest ones are dropped and the most recent ones remain visible
