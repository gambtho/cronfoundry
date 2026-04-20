package db

import "embed"

// MigrationsFS is the embedded filesystem containing all goose SQL migrations.
// Migration files live under migrations/ and follow goose's NNN_name.sql
// naming convention. An empty directory is valid during early bring-up; goose
// will treat it as a no-op.
//
//go:embed all:migrations
var MigrationsFS embed.FS
