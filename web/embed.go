// Package webassets embeds the built frontend static assets so the Go service
// can serve the browser page without any external static file server.
package webassets

import "embed"

// Assets is the embedded, deterministic frontend build output under dist/.
//
//go:embed all:dist
var Assets embed.FS
