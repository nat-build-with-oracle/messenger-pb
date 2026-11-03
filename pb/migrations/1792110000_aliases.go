// Aliases: our own name for a chat ("Joe" for Joseph Example), for Messenger and LINE alike.
// Kept apart from threads.name, which the sync rewrites from the phone. Readable by any
// signed-in role; written only through POST /api/aliases (alias.go), so every change has an author.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		signedIn := `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`
		c := core.NewBaseCollection("aliases")
		c.ListRule, c.ViewRule = &signedIn, &signedIn
		c.Fields.Add(
			&core.SelectField{Name: "platform", Required: true, MaxSelect: 1, Values: []string{"Messenger", "LINE"}},
			&core.TextField{Name: "key", Required: true, Max: 70},
			&core.TextField{Name: "alias", Required: true, Max: 100},
			&core.TextField{Name: "by", Max: 200},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_aliases_key", true, "platform, key", "")
		return app.Save(c)
	}, func(app core.App) error {
		if c, err := app.FindCollectionByNameOrId("aliases"); err == nil {
			return app.Delete(c)
		}
		return nil
	})
}
