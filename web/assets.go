// Package web serves the bundled Agentkit UI without Node.js or local assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// Rebuild after editing the frontend, and include dist in the same change.
//go:generate npm run build

//go:embed dist
var assets embed.FS

// Handler serves the built UI at /. The caller routes /api to server separately.
func Handler() http.Handler {
	files, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err) // The embedded directory is checked by the Go compiler.
	}
	static := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		static.ServeHTTP(w, r)
	})
}
