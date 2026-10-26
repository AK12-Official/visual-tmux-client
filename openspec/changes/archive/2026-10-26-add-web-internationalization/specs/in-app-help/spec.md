## ADDED Requirements

### Requirement: Localized guide selection

The client SHALL ship English and Simplified Chinese versions of the embedded guide, each satisfying the existing guide-content requirements. Opening help SHALL display the active interface language, and changing language while help is open SHALL update its content without disturbing the workspace. Guide assets SHALL remain same-origin and self-contained. Cached content SHALL be separated by language; a late response for an earlier language SHALL NOT replace the current language's guide. Loading/failure controls SHALL use the active language, and a failed load SHALL be retryable on reopening help.

#### Scenario: English guide
- **WHEN** a user opens help with English active
- **THEN** the guide and help controls are English and content is fetched only from embedded same-origin assets

#### Scenario: Change language while loading
- **WHEN** a Chinese guide request is pending and the user switches to English
- **THEN** help displays English content once available and a later Chinese response does not overwrite it

#### Scenario: Reuse language-specific cache
- **WHEN** a user returns to a previously loaded guide language
- **THEN** help reuses that language's cached content without displaying the other language's guide

#### Scenario: Recover from failed loading
- **WHEN** a guide load fails and the user closes and reopens help
- **THEN** loading is retried and the failure does not poison the language's cache
