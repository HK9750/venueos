// Package migrations exposes versioned SQL migrations for explicit migration
// commands and integration tests.
package migrations

import "embed"

// FS contains all SQL migration files in this directory.
//
//go:embed *.sql
var FS embed.FS
