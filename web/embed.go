// Package web embeds the built SPA for the Go binary to serve.
package web

import (
	"embed"
)

// Dist is the Vite build output (web/dist). It always contains at least the
// committed placeholder so the Go build works before the first web build;
// the placeholder is never served (see internal/http SPA fallback).
//
//go:embed dist
var Dist embed.FS
