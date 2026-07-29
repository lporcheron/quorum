// Package translations embeds the go-i18n message catalogs.
package translations

import "embed"

//go:embed *.toml
var FS embed.FS
