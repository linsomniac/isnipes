//go:build !embed

package main

import (
	"io/fs"
	"testing/fstest"
)

// embedOffNotice is served at / when the binary was built WITHOUT embedded
// assets (a plain `go build`, no -tags embed). It points the operator at
// the two ways to serve the real client. testing/fstest does not pull in
// the testing framework, so this is safe in a production binary.
const embedOffNotice = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>isnipes</title></head>
<body>
<h1>isnipes server</h1>
<p>This binary was built without embedded client assets.</p>
<p>Serve the client by running with <code>--web-dist DIR</code>, or rebuild
with <code>make build</code> (which runs the web build and
<code>go build -tags embed</code>).</p>
</body></html>
`

// embeddedStatic returns a single-page notice FS for non-embed builds.
func embeddedStatic() fs.FS {
	return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(embedOffNotice)}}
}
