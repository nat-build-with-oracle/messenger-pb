// Pictures for the chat tab. The native server on the phone (`messenger serve 127.0.0.1:8722`)
// already keeps a cache of profile pictures and attachment previews, fetched while the
// signed fbcdn URLs were still valid. These routes pass those through, behind the same
// sign-in as the archive. An <img> cannot send an Authorization header, so the page asks
// for a short-lived file token (POST /api/files/token) and puts it in ?token=.
//
// NATIVE_URL points at the native server (default http://127.0.0.1:8722, "off" disables).
package main

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

var (
	reAvatar = regexp.MustCompile(`^[0-9]{1,24}$`)
	reAtt    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._$-]{0,199}$`) // fbids, "e<pk>" for E2EE, "ee.mid.$…" link cards
)

// checkFileToken: ?token= must be a file token of a superuser or a users record that may read.
func checkFileToken(e *core.RequestEvent) error {
	rec, err := e.App.FindAuthRecordByToken(e.Request.URL.Query().Get("token"), core.TokenTypeFile)
	if err != nil {
		return e.UnauthorizedError("file token required", nil)
	}
	if !rec.IsSuperuser() && !(rec.Collection().Name == "users" && canRead(rec.GetString("role"))) {
		return e.ForbiddenError("not allowed", nil)
	}
	return nil
}

func registerChatMedia(app *pocketbase.PocketBase) {
	native := envOr("NATIVE_URL", "http://127.0.0.1:8722")
	if native == "off" {
		return
	}
	target, err := url.Parse(native)
	if err != nil {
		app.Logger().Error("chat media: bad NATIVE_URL", "url", native)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "picture cache unavailable", http.StatusBadGateway)
	}

	pass := func(kind string, re *regexp.Regexp) func(*core.RequestEvent) error {
		return func(e *core.RequestEvent) error {
			if err := checkFileToken(e); err != nil {
				return err
			}
			id := e.Request.PathValue("id")
			if !re.MatchString(id) {
				return e.NotFoundError("", nil)
			}
			r := e.Request.Clone(e.Request.Context())
			r.URL.Path, r.URL.RawPath, r.URL.RawQuery = "/api/"+kind+"/"+url.PathEscape(id), "", ""
			r.Header.Del("Authorization")
			r.Header.Del("Cookie")
			proxy.ServeHTTP(e.Response, r)
			return nil
		}
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/chat/avatar/{id}", pass("avatar", reAvatar))
		se.Router.GET("/chat/media/{id}", pass("media", reAtt))
		return se.Next()
	})
}
