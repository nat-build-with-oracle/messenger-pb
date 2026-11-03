// Forwarding rules: new messages in one chat are posted to a Discord webhook (forward.go).
// `forwards` holds the rules; `forward_log` is the ledger — one row per (rule, message),
// unique by hash, so a message is never posted twice. Both are superuser-only: the webhook
// URL is a write credential for the Discord channel, and the agent must not read it.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		forwards := core.NewBaseCollection("forwards")
		forwards.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 200},
			&core.SelectField{Name: "platform", Required: true, MaxSelect: 1, Values: []string{"Messenger"}},
			&core.TextField{Name: "thread_key", Required: true, Max: 64},
			&core.URLField{Name: "webhook", Required: true, OnlyDomains: []string{"discord.com", "discordapp.com"}, Hidden: true},
			&core.BoolField{Name: "enabled"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(forwards); err != nil {
			return err
		}
		log := core.NewBaseCollection("forward_log")
		log.Fields.Add(
			&core.RelationField{Name: "forward", Required: true, CollectionId: forwards.Id, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "mid", Required: true, Max: 200},
			&core.TextField{Name: "hash", Required: true, Max: 64},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: []string{"sending", "sent", "failed"}},
			&core.NumberField{Name: "http", OnlyInt: true},
			&core.TextField{Name: "error", Max: 2000},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		log.AddIndex("idx_forward_log_hash", true, "hash", "")
		return app.Save(log)
	}, func(app core.App) error {
		for _, name := range []string{"forward_log", "forwards"} {
			if c, err := app.FindCollectionByNameOrId(name); err == nil {
				if err := app.Delete(c); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
