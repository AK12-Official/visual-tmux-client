package files

import "os"

// Entry is one child of a listed directory. Directories report a size of zero
// rather than the size of the directory inode, which is meaningless to a
// browser and would need a stat per entry to obtain.
type Entry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// ListResult is one directory's immediate children, with the canonical path
// they were read from and whether the configured bound cut the listing short.
type ListResult struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// ReadResult is an open file together with what a caller needs to stream it and
// to decide how to present it. The caller is responsible for closing File.
type ReadResult struct {
	File *os.File
	Size int64
	// Mtime is the modification time in milliseconds since the epoch, which is
	// the precision a JSON client can hold exactly.
	Mtime int64
	// MtimeNanos is the same instant at the precision the filesystem reports,
	// and it is what a later write is compared against. The two travel together
	// because they answer different questions: Mtime is what a client can carry
	// without losing digits, and this is what decides whether the file changed.
	MtimeNanos int64
	// Binary reports whether the contents are not text, so the caller can decline
	// to present them as text rather than decoding the bytes into replacement
	// characters. It is decided by examining the whole file, so it is a
	// classification of the contents rather than of their first few kilobytes.
	Binary bool
}

// WriteResult is what a completed write reports about the file it produced.
//
// The caller adopts this as the modification time it has observed. Guessing it
// instead -- which is what a client that assumed the current time would do --
// dates the file behind itself and makes the very next save look like a conflict.
type WriteResult struct {
	// Mtime is the resulting modification time in milliseconds since the epoch.
	Mtime int64
	// MtimeNanos is the same instant at the precision the filesystem reports,
	// which is what the next write will be compared against.
	MtimeNanos int64
}

// ExpectedMtime is the modification time a caller last observed for a target, at
// whichever precisions it was able to report.
//
// Two precisions travel because each solves a different problem. Milliseconds fit
// in a JavaScript number exactly, so they are what the JSON contract and the wire
// headers carry. Nanoseconds are what a filesystem actually records, so they are
// what decides whether the file changed: two edits inside one millisecond are two
// edits, and a comparison that cannot tell them apart lets the later save
// overwrite the earlier one without saying so.
type ExpectedMtime struct {
	// Millis is the observed modification time in milliseconds since the epoch.
	Millis int64
	// Nanos is the same instant at nanosecond precision, when the caller could
	// report it. A caller that reports only milliseconds is compared against
	// those instead -- weaker, but it is what the caller gave us to compare.
	Nanos *int64
}

// matches reports whether a file's observed modification time is still the one
// this expectation was taken from.
func (e *ExpectedMtime) matches(info os.FileInfo) bool {
	if e.Nanos != nil {
		return info.ModTime().UnixNano() == *e.Nanos
	}
	return info.ModTime().UnixMilli() == e.Millis
}
