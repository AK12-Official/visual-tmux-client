package desktop

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
)

// Both generated bearer and launch credentials are 32-byte base64url values.
// Redact their shape even after a one-use launch code has been consumed, without
// retaining a history of secrets or sharing mutable controller state with logs.
var credentialPattern = regexp.MustCompile(`[A-Za-z0-9_-]{43,}`)

func RedactDiagnostic(message string) string {
	return credentialPattern.ReplaceAllString(message, "[redacted]")
}

func NativeLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			switch attr.Value.Kind() {
			case slog.KindString:
				attr.Value = slog.StringValue(RedactDiagnostic(attr.Value.String()))
			case slog.KindAny:
				attr.Value = slog.StringValue(RedactDiagnostic(fmt.Sprint(attr.Value.Any())))
			}
			return attr
		},
	}))
}
