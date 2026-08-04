package migrations

import "embed"

// FS contains organization-service migrations.
//
//go:embed *.sql
var FS embed.FS
