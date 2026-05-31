package dashboard

import (
	"embed"
	"io/fs"
	"log/slog"
)

//go:embed all:dist
var webDistFS embed.FS

func WebDistFS() fs.FS {
	sub, err := fs.Sub(webDistFS, "dist")
	if err != nil {
		slog.Warn("dashboard: embedded dist not found, dashboard UI unavailable", "err", err)
		return nil
	}
	return sub
}
