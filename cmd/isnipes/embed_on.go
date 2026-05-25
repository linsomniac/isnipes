//go:build embed

package main

import (
	"embed"
	"io/fs"
)

// embeddedFS holds the production client build, baked in by
// `go build -tags embed` after `make build` populates dist/.
//
//go:embed all:dist
var embeddedFS embed.FS

// embeddedStatic returns the embedded client assets.
func embeddedStatic() fs.FS {
	sub, err := fs.Sub(embeddedFS, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
