package webapi

import "embed"

// staticFiles holds the compiled React bundle.
// Populated when `make web` runs before `make build`.
//
//go:embed all:web/dist
var staticFiles embed.FS
