package migrations

import "embed"

// FS contains the immutable identity schema migrations compiled into the migration binary.
//
//go:embed *.sql
var FS embed.FS
