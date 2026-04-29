// Package migrations embeds the numbered SQL migration files for ds-service.
//
// Files follow the convention NNNN_description.up.sql. The internal/db
// package consumes this via the FS variable.
package migrations

import "embed"

// FS embeds every .up.sql file in this directory at compile time.
//
//go:embed *.up.sql
var FS embed.FS
