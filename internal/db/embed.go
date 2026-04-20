package db

import "embed"

// MigrationsFS is the embedded filesystem containing all goose SQL migrations.
// Migration files live under migrations/ and follow goose's NNN_name.sql
// naming convention.
//
//go:embed migrations/*.sql
var MigrationsFS embed.FS
