# Spec Delta

## ADDED Requirements

### Requirement: Language-independent control geometry

Buttons, selectors and inputs SHALL keep the same height and width in every supported language. Controls that belong to one group SHALL share a minimum width that fits the longest supported label, SHALL keep their label on one line and SHALL center it. Descriptive text such as error messages, help content and hints is not bound by this requirement and may wrap.

#### Scenario: Switch language with the sidebar open

- **WHEN** the user switches the interface language while the sidebar, top bar and an open dialog are visible
- **THEN** every visible button, selector and input keeps its height and width

#### Scenario: Controls in one group have equal width

- **WHEN** buttons form one row or toolbar, such as the actions of a host entry or the sidebar sort modes
- **THEN** they are equally wide in each supported language, sized to fit the longest label

#### Scenario: Longest label stays on one line

- **WHEN** a control's label is at its longest in the active language
- **THEN** the label is shown in full on one line, neither wrapped nor cut off

#### Scenario: Descriptive text still wraps

- **WHEN** an error message, hint or help passage is longer in the active language
- **THEN** it wraps to more lines and is shown in full, and only the controls it displaces move

#### Scenario: Position is stable where text length is equal

- **WHEN** the interface language changes and the descriptive text around a control occupies the same number of lines in both languages
- **THEN** that control keeps its position

#### Scenario: Geometry is measured in a real WebView

- **WHEN** the desktop geometry check runs on macOS and on Linux
- **THEN** it measures the visible controls in each supported language and fails if any control differs in height or width
