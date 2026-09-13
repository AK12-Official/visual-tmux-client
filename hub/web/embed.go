package web

import (
	"embed"
	"io/fs"
)

// distFS embeds the built frontend from the dist directory.
//
//go:embed all:dist
var distFS embed.FS

// FS returns an fs.FS rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
