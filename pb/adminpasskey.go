// Passkey sign-in on PocketBase's own admin (/_/), whose login form has email + password only.
//
//	GET /_/                      the admin's index.html with one more <script src> (adminpasskey.js)
//	GET /_/messenger-pb-passkey.js  that script
//
// The script waits for the admin's login form, adds a "Sign in with passkey" button under it, runs
// the same WebAuthn login as the review page (passkey.go) with ?only=_superusers, stores the result
// where the admin keeps its session (localStorage "__pb_superusers__/_", {token, record}, as
// openAdmin in ui/src/api.ts does) and reloads into the admin. Password login is untouched.
//
// Why a script in the admin's own page and not a page of ours: the button sits where Nat already
// looks, and the vendored module stays as it is. The script is a file on this origin, not inline,
// so the admin keeps its Content-Security-Policy (script-src 'self'). Both routes are more specific
// than PocketBase's /_/{path...}, so they win without touching its router; every other /_/ file is
// still PocketBase's.
package main

import (
	_ "embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/ui"
)

//go:embed adminpasskey.js
var adminPasskeyJS []byte

const adminPasskeyScript = `<script type="module" src="./messenger-pb-passkey.js"></script>`

// adminCSP is PocketBase's defaultCSP (apis/serve.go, v0.40.5), which its /_/ route sets: our
// index.html answers in its place and must not loosen it.
const adminCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' http://127.0.0.1:* https://tile.openstreetmap.org data: blob:; connect-src 'self' http://127.0.0.1:* https://nominatim.openstreetmap.org; script-src 'self' http://127.0.0.1:*; frame-ancestors 'none'"

func registerAdminPasskey(app *pocketbase.PocketBase) {
	if ui.DistDirFS == nil { // built with no_ui: no admin to extend
		return
	}
	index, err := adminIndex(ui.DistDirFS)
	if err != nil {
		app.Logger().Warn("admin passkey button off", "error", err)
		return
	}
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		bindAdminPasskey(se.Router, index)
		return se.Next()
	})
}

// adminIndex is the admin's index.html with the script added before </head>.
func adminIndex(dist fs.FS) ([]byte, error) {
	b, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, err
	}
	s := string(b)
	i := strings.LastIndex(s, "</head>")
	if i < 0 {
		return nil, fs.ErrInvalid
	}
	return []byte(s[:i] + "  " + adminPasskeyScript + "\n" + s[i:]), nil
}

func bindAdminPasskey(r *router.Router[*core.RequestEvent], index []byte) {
	r.GET("/_/{$}", func(e *core.RequestEvent) error {
		h := e.Response.Header()
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("Cache-Control", "no-cache") // a redeploy must reach open tabs on reload
		return e.Blob(http.StatusOK, "text/html; charset=utf-8", index)
	})
	r.GET("/_/messenger-pb-passkey.js", func(e *core.RequestEvent) error {
		h := e.Response.Header()
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("Cache-Control", "no-cache")
		return e.Blob(http.StatusOK, "text/javascript; charset=utf-8", adminPasskeyJS)
	})
}
