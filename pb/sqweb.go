// The review page and chat tabs: a Preact + Tailwind build (ui/ → sqweb/dist) embedded in
// the binary. Run `bun install && bun run build` in pb/ui before `go build`. Nothing is
// loaded from a CDN: this page holds the reviewer's login and the approve button.
package main

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

//go:embed all:sqweb/dist
var sqDist embed.FS

func registerSQWeb(app *pocketbase.PocketBase) {
	dist, _ := fs.Sub(sqDist, "sqweb/dist")
	index, _ := fs.ReadFile(dist, "index.html")
	assets := http.FileServerFS(dist)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/{$}", func(e *core.RequestEvent) error {
			h := e.Response.Header()
			h.Set("X-Frame-Options", "DENY")   // no approve button inside someone else's frame
			h.Set("Cache-Control", "no-cache") // a redeploy must reach open tabs on reload
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; frame-ancestors 'none'")
			return e.Blob(http.StatusOK, "text/html; charset=utf-8", index)
		})
		// Hashed file names: safe to cache for good.
		se.Router.GET("/assets/{path...}", func(e *core.RequestEvent) error {
			e.Response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			assets.ServeHTTP(e.Response, e.Request)
			return nil
		})
		return se.Next()
	})
}
