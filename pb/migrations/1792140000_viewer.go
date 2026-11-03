// role=viewer: reads the archive (chats, seen receipts, aliases) and nothing else. No queue,
// no forwards, no writes. Meant for a login link (`messenger-pb login-link`): whoever holds
// the link can read every chat until it expires, so it gets the least a reader needs.
package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

var viewerReads = []string{"contacts", "threads", "messages", "seen", "seen_log", "aliases"}

func init() {
	setRules := func(app core.App, rule string) error {
		for _, name := range viewerReads {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			c.ListRule, c.ViewRule = &rule, &rule
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		if f, ok := users.Fields.GetByName("role").(*core.SelectField); ok && !slices.Contains(f.Values, "viewer") {
			f.Values = append(f.Values, "viewer")
			if err := app.Save(users); err != nil {
				return err
			}
		}
		return setRules(app, `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer" || @request.auth.role = "viewer")`)
	}, func(app core.App) error {
		return setRules(app, `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`)
	})
}
