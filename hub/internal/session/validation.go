package session

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// MaxSessionNameRunes bounds session-name length counted in code points, not bytes.
const MaxSessionNameRunes = 64

// ValidateSessionName enforces rules to keep names unambiguous:
// no reserved tmux delimiters (: .), path separators (/ \), full-width lookalikes,
// control characters, or leading/trailing whitespace.
func ValidateSessionName(name string) error {
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidName)
	}
	runes := []rune(name)
	if len(runes) == 0 {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if len(runes) > MaxSessionNameRunes {
		return fmt.Errorf("%w: name is longer than %d characters", ErrInvalidName, MaxSessionNameRunes)
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: name contains a control character", ErrInvalidName)
		}
		if r == ':' || r == '.' {
			return fmt.Errorf("%w: name must not contain %q (reserved by tmux target syntax)", ErrInvalidName, r)
		}
		if r == '/' || r == '\\' {
			return fmt.Errorf("%w: name must not contain %q (reserved path separator)", ErrInvalidName, r)
		}
		if r == '：' || r == '．' {
			return fmt.Errorf("%w: name must not contain full-width %q", ErrInvalidName, r)
		}
	}
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return fmt.Errorf("%w: name has leading or trailing whitespace", ErrInvalidName)
	}
	return nil
}
