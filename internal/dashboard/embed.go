package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var webDistFS embed.FS

func WebDistFS() fs.FS {
	sub, err := fs.Sub(webDistFS, "dist")
	if err != nil {
		panic("dashboard: embedded dist not found: " + err.Error())
	}
	return sub
}
