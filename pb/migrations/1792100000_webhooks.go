// Webhooks beyond Discord: a rule may post to any https URL, as Discord's message shape or
// as plain JSON, optionally signed (X-Signature: sha256=<hmac of the body with `secret`).
// Agents may PROPOSE a rule (sq.go /api/forwards/propose); it is created disabled and only a
// superuser turns it on, because a webhook carries private chats off the tailnet.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("forwards")
		if err != nil {
			return err
		}
		if f, ok := c.Fields.GetByName("webhook").(*core.URLField); ok {
			f.OnlyDomains = nil
		}
		c.Fields.Add(
			&core.SelectField{Name: "format", MaxSelect: 1, Values: []string{"discord", "json"}},
			&core.TextField{Name: "secret", Max: 200, Hidden: true},
			&core.TextField{Name: "proposed_by", Max: 200},
		)
		return app.Save(c)
	}, nil)
}
