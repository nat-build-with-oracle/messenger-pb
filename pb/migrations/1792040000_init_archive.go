// Archive schema: contacts, threads, messages. Read-only to the public API
// (listRule/viewRule "" = anyone who can reach the port — bind to 127.0.0.1);
// writes go through the importer/sync or a superuser.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		public := ""

		contacts := core.NewBaseCollection("contacts")
		contacts.ListRule, contacts.ViewRule = &public, &public
		contacts.Fields.Add(
			&core.TextField{Name: "fbid", Required: true, Max: 64},
			&core.TextField{Name: "name", Max: 500},
			&core.TextField{Name: "picture", Max: 4000},
		)
		contacts.AddIndex("idx_contacts_fbid", true, "fbid", "")
		if err := app.Save(contacts); err != nil {
			return err
		}

		threads := core.NewBaseCollection("threads")
		threads.ListRule, threads.ViewRule = &public, &public
		threads.Fields.Add(
			&core.TextField{Name: "key", Required: true, Max: 64},
			&core.TextField{Name: "name", Max: 500},
			&core.BoolField{Name: "is_group"},
			&core.BoolField{Name: "e2ee"},
			&core.NumberField{Name: "last_ts", OnlyInt: true},
			&core.TextField{Name: "preview", Max: 2000},
		)
		threads.AddIndex("idx_threads_key", true, "key", "")
		if err := app.Save(threads); err != nil {
			return err
		}

		messages := core.NewBaseCollection("messages")
		messages.ListRule, messages.ViewRule = &public, &public
		messages.Fields.Add(
			&core.TextField{Name: "mid", Required: true, Max: 200},
			&core.RelationField{Name: "thread", CollectionId: threads.Id, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "thread_key", Required: true, Max: 64},
			&core.NumberField{Name: "ts", OnlyInt: true, Required: true},
			&core.TextField{Name: "sender", Max: 64},
			&core.TextField{Name: "sender_name", Max: 500},
			&core.TextField{Name: "text", Max: 200000},
			&core.SelectField{Name: "kind", MaxSelect: 1, Values: []string{"text", "admin", "unsent", "media"}},
			&core.SelectField{Name: "source", MaxSelect: 1, Values: []string{"fts", "msys", "e2ee", "api"}},
			&core.JSONField{Name: "reply", MaxSize: 1 << 16},
			&core.JSONField{Name: "atts", MaxSize: 1 << 20},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		messages.AddIndex("idx_messages_mid", true, "mid", "")
		messages.AddIndex("idx_messages_thread_ts", false, "thread_key, ts", "")
		messages.AddIndex("idx_messages_ts", false, "ts", "")
		return app.Save(messages)
	}, func(app core.App) error {
		for _, name := range []string{"messages", "threads", "contacts"} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(c); err != nil {
				return err
			}
		}
		return nil
	})
}
