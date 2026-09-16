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
	File  *os.File
	Size  int64
	Mtime int64
	// Binary reports whether the contents are not text, so the caller can decline
	// to present them as text rather than decoding the bytes into replacement
	// characters. It is decided from a bounded sample of the file, so it is a
	// classification rather than a guarantee.
	Binary bool
}
