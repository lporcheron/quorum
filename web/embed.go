// Package web embeds the static assets served under /static/.
package web

import "embed"

//go:embed static
var StaticFS embed.FS
