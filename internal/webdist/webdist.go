// Package webdist embeds the built web console (web/dist copied to
// internal/webdist/dist at build time). The checked-in dist/index.html is a
// placeholder so the package always compiles; the release build overwrites
// the directory with the real Vite output.
package webdist

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the console files rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("webdist: " + err.Error())
	}
	return sub
}
