// The archive collections were readable by anyone who could reach the port. That was fine
// on 127.0.0.1; once the same app serves the review page on a Tailscale address, every chat
// would be open to the whole tailnet. From here on they need the same sign-in as the queue:
// a superuser, or a `users` record with role=agent or role=reviewer.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	setRules := func(app core.App, rule string) error {
		for _, name := range []string{"contacts", "threads", "messages"} {
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
		return setRules(app, `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`)
	}, func(app core.App) error {
		return setRules(app, "")
	})
}
