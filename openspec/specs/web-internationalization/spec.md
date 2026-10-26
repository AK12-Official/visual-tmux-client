# web-internationalization Specification

## Purpose

Provide a consistent English or Simplified Chinese Web experience with browser-local preferences and language switching that preserves active terminal and editing work.

## Requirements

### Requirement: Browser-local language preference

The client SHALL offer follow-browser, Simplified Chinese (`zh-CN`), and English (`en`) language choices before login and in the authenticated workspace. It SHALL default to follow-browser, retain explicit preferences across reloads, logout, and target-host changes in the same browser origin, and SHALL NOT persist them as host or server configuration. Unavailable browser storage SHALL NOT prevent startup or in-memory language switching. Invalid stored preferences SHALL behave as follow-browser.

#### Scenario: Remember an explicit choice
- **WHEN** a user selects Simplified Chinese, logs out, and reloads the page
- **THEN** the login interface remains in Simplified Chinese

#### Scenario: Switch hosts
- **WHEN** a user switches the target host
- **THEN** the interface retains its language preference

#### Scenario: Storage is unavailable
- **WHEN** browser storage reads or writes fail
- **THEN** the application still starts and language selection works for the current page lifetime

### Requirement: Deterministic browser language resolution

In follow-browser mode the client SHALL evaluate browser language preferences in order and select the first supported match. English variants SHALL match English. Chinese tags with an explicit Hans script, or CN/SG region without an explicit Hant script, SHALL match Simplified Chinese; bare `zh` SHALL also match Simplified Chinese. Explicit Hant and Chinese TW/HK/MO variants without an explicit Hans script SHALL remain unmatched. Unsupported preferences SHALL be skipped and the final fallback SHALL be English. Missing translated interface messages SHALL fall back to English rather than exposing translation keys.

#### Scenario: Browser prefers Simplified Chinese
- **WHEN** follow-browser is selected and the browser prefers `zh-SG` followed by `en`
- **THEN** the active language is Simplified Chinese

#### Scenario: Unsupported Chinese variant
- **WHEN** follow-browser is selected and the browser preferences are `zh-TW` followed by `en-US`
- **THEN** the active language is English

#### Scenario: No matching preference
- **WHEN** all browser languages are unsupported or no browser languages are available
- **THEN** the active language is English

#### Scenario: Missing translation
- **WHEN** an interface message is absent from the selected language but available in English
- **THEN** its English message is displayed

### Requirement: Complete Web presentation localization

The client SHALL translate application-owned text for login, host management, sessions, terminal controls and connection states, file management including built-in editor controls, notifications, confirmations, validation, and help controls. This SHALL include placeholders, tooltips, accessible names, and the document language. Bootstrap loading and failure surfaces SHALL use the resolved language before the application mounts. User content, paths, host/session names, file contents, terminal output, configuration keys, and machine identifiers SHALL remain unchanged.

#### Scenario: Localized startup failure
- **WHEN** loading client configuration fails with Simplified Chinese selected
- **THEN** the failure heading and retry control are Chinese and diagnostic details remain available

#### Scenario: Accessible interface
- **WHEN** the active language changes
- **THEN** visible interface text, tooltips, accessible names, and the document language reflect that language

#### Scenario: Preserve user content
- **WHEN** the interface language changes with a terminal and file open
- **THEN** their output, contents, names, and paths remain unchanged

### Requirement: State-preserving live language changes

Changing language SHALL take effect without page reload, terminal reattachment, or editor recreation. It SHALL preserve selected host and session, connection state, open panels, drafts, editor selection, and undo history. Persistent application-owned status/error messages SHALL render in the current language. Notifications already visible at the time of switching MAY retain their original language until dismissed; subsequent notifications SHALL use the current language.

#### Scenario: Switch during active work
- **WHEN** a user changes language with a connected terminal and unsaved editor changes
- **THEN** the terminal stays connected and the editor retains its draft, selection, and undo history

### Requirement: Stable errors with localized presentation

The client SHALL retain machine error codes for programmatic decisions and translate known failures at display boundaries using complete parameterized messages. Unknown failures SHALL retain the original diagnostic information with localized explanatory context. Changing language SHALL NOT change error handling, retry behavior, or API request identifiers.

#### Scenario: Known failure
- **WHEN** an operation fails with a known host configuration conflict code
- **THEN** the user sees an explanation in the active language and conflict handling uses the unchanged code

#### Scenario: Unknown failure
- **WHEN** an operation fails with an unrecognized diagnostic
- **THEN** the user sees localized context together with the original diagnostic
