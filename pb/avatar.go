// GET /api/people/avatar?ns=&id=: one person's profile picture, bytes only, for people-oracle.
//
//	ns=facebook:user&id=<fbid>    from the phone's avatar cache (AVATAR_DIR/<fbid>.jpg), saved by the
//	                              native messenger server while the signed fbcdn URL was still valid
//	ns=line:app-user&id=U<32 hex>  (or line:user, the @1 name)     fetched server-side from LINE's profile CDN by the contact's
//	                              picture_path; the CDN URL itself is never returned
//
// Role viewer or above. Answers image/jpeg|png|webp up to 10 MB, 404 when there is none.
// Headers: X-Avatar-Source: phone-cache|line-cdn, X-Avatar-Observed-Ms when known (file time,
// or the CDN's Last-Modified).
package main

import (
	"database/sql"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const avatarMax = 10 << 20

var (
	reFBID     = regexp.MustCompile(`^[0-9]{1,24}$`)
	reLineUser = regexp.MustCompile(`^U[0-9a-f]{32}$`)
	reLinePath = regexp.MustCompile(`^/[A-Za-z0-9_-]{8,200}$`) // what picture_path holds; anything else is refused
	avatarHTTP = &http.Client{Timeout: 20 * time.Second}
)

func avatarType(b []byte) string {
	switch t := http.DetectContentType(b); t {
	case "image/jpeg", "image/png", "image/webp":
		return t
	}
	return ""
}

func registerAvatar(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/people/avatar", func(e *core.RequestEvent) error {
			if !canRead(sqRole(e)) {
				return e.ForbiddenError("sign in first", nil)
			}
			ns, id := e.Request.URL.Query().Get("ns"), e.Request.URL.Query().Get("id")
			var body []byte
			var source string
			var observed int64
			switch {
			case ns == "facebook:user" && reFBID.MatchString(id):
				f := filepath.Join(envOr("AVATAR_DIR", "/data/local/tmp/messenger-avatars"), id+".jpg")
				st, err := os.Stat(f)
				if err != nil || !st.Mode().IsRegular() || st.Size() > avatarMax {
					return e.NotFoundError("", nil)
				}
				if body, err = os.ReadFile(f); err != nil {
					return e.NotFoundError("", nil)
				}
				source, observed = "phone-cache", st.ModTime().UnixMilli()
			case (ns == "line:app-user" || ns == "line:user") && reLineUser.MatchString(id):
				db, err := openRO(pick("", "LINE_CONTACT_DB", phoneLineContact))
				if err != nil {
					return e.NotFoundError("", nil)
				}
				var path sql.NullString
				err = db.QueryRow(`SELECT picture_path FROM contacts WHERE mid = ?`, "u"+id[1:]).Scan(&path)
				db.Close()
				if err != nil || !reLinePath.MatchString(path.String) {
					return e.NotFoundError("", nil)
				}
				r, err := avatarHTTP.Get("https://profile.line-scdn.net" + path.String)
				if err != nil {
					return e.NotFoundError("", nil)
				}
				defer r.Body.Close()
				if r.StatusCode != http.StatusOK {
					return e.NotFoundError("", nil)
				}
				if body, err = io.ReadAll(io.LimitReader(r.Body, avatarMax+1)); err != nil || len(body) > avatarMax {
					return e.NotFoundError("", nil)
				}
				source = "line-cdn"
				if t, err := http.ParseTime(r.Header.Get("Last-Modified")); err == nil {
					observed = t.UnixMilli()
				}
			default:
				return e.NotFoundError("", nil)
			}
			typ := avatarType(body)
			if typ == "" {
				return e.NotFoundError("", nil)
			}
			h := e.Response.Header()
			h.Set("X-Avatar-Source", source)
			if observed > 0 {
				h.Set("X-Avatar-Observed-Ms", strconv.FormatInt(observed, 10))
			}
			h.Set("Cache-Control", "private, max-age=3600")
			return e.Blob(http.StatusOK, typ, body)
		})
		return se.Next()
	})
}
