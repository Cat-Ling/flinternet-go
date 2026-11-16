// Package web embeds the server-rendered HTML templates and static assets.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static templates/*.html
var embeddedAssets embed.FS

// StaticFS returns the embedded static assets filesystem rooted at static/.
func StaticFS() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "static")
}

// TemplatesFS returns the embedded HTML templates filesystem rooted at templates/.
func TemplatesFS() (fs.FS, error) {
	return fs.Sub(embeddedAssets, "templates")
}
