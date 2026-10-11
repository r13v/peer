// Package web embeds the board SPA that make web builds into web/dist.
package web

import (
	"embed"
	"io/fs"
)

// dist holds .keep before the first build, so go build works without it.
//
//go:embed all:dist
var dist embed.FS

// Dist is the SPA build: index.html and the files under /_app/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is a valid literal path
	}

	return sub
}
