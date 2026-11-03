// POST /api/aliases {platform, key, alias}: set our own name for a chat; an empty alias
// removes it. Any signed-in agent or reviewer; the author is kept in `by`.
package main

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func registerAliases(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.POST("/api/aliases", func(e *core.RequestEvent) error {
			if r := sqRole(e); r != "agent" && r != "reviewer" {
				return e.ForbiddenError("sign in as agent or reviewer", nil)
			}
			var b struct{ Platform, Key, Alias string }
			if err := e.BindBody(&b); err != nil {
				return e.BadRequestError("bad body", err)
			}
			b.Alias = strings.TrimSpace(b.Alias)
			if (b.Platform != "Messenger" && b.Platform != "LINE") || strings.TrimSpace(b.Key) == "" {
				return e.BadRequestError("platform (Messenger or LINE) and key are required", nil)
			}
			r, err := e.App.FindFirstRecordByFilter("aliases", "platform = {:p} && key = {:k}", dbx.Params{"p": b.Platform, "k": b.Key})
			if b.Alias == "" {
				if err == nil {
					if err := e.App.Delete(r); err != nil {
						return err
					}
				}
				return e.JSON(http.StatusOK, map[string]any{"platform": b.Platform, "key": b.Key, "alias": ""})
			}
			if err != nil {
				col, err := e.App.FindCollectionByNameOrId("aliases")
				if err != nil {
					return err
				}
				r = core.NewRecord(col)
				r.Set("platform", b.Platform)
				r.Set("key", b.Key)
			}
			r.Set("alias", b.Alias)
			r.Set("by", actorOf(e))
			if err := e.App.Save(r); err != nil {
				return e.BadRequestError("could not save the alias", err)
			}
			return e.JSON(http.StatusOK, map[string]any{"platform": b.Platform, "key": b.Key, "alias": b.Alias})
		})
		return se.Next()
	})
}
