// Package docssite embeds the built documentation site (docs-site/dist)
// for the Go binary to serve at /docs/. Run `npm run build` in
// docs-site/ first; the directory is required at compile time.
package docssite

import (
	"embed"
)

// Dist is the VitePress + Redoc build output.
var (
	//go:embed dist
	Dist embed.FS
)
