// Package migrations embeds versioned schema migrations for the store runner.
package migrations

import (
	"embed"
)

// FS holds the *.sql migration files, applied in name order.
//
//go:embed *.sql
var FS embed.FS
