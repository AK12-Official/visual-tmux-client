package host

import (
	"strings"
)

// EncodePOSIXArg escapes an argument so a POSIX shell evaluates it literally,
// preventing command injection, word splitting, variable expansion, and globbing.
func EncodePOSIXArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// EncodePOSIXCommand encodes an executable and its arguments into a shell command line.
func EncodePOSIXCommand(args ...string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(EncodePOSIXArg(a))
	}
	return b.String()
}
