# Spec Delta

## ADDED Requirements

### Requirement: Application icon in desktop packages

Desktop packages SHALL include an application icon so that macOS (Dock, Finder, Launchpad) and Linux desktop shells (application menu, taskbar) display it for the installed application. Both platforms SHALL use the same artwork. The macOS icon SHALL be supplied at every size the system requests and SHALL remain legible at small sizes.

#### Scenario: macOS shows the application icon

- **WHEN** the user installs the macOS package
- **THEN** Dock, Finder and Launchpad show the application's own icon instead of a generic one, at small and large display sizes

#### Scenario: Linux shows the same artwork

- **WHEN** the user installs the Ubuntu package
- **THEN** the application menu and taskbar show the same artwork as the macOS icon

#### Scenario: Signed package remains valid

- **WHEN** a macOS package is signed, ad hoc or with a Developer ID
- **THEN** the icon is part of the bundle's sealed resources and signature verification passes

#### Scenario: Both icons come from one source

- **WHEN** the icon generation script is run
- **THEN** it produces the Linux launcher icon and the macOS icon set from the same artwork sources
