// Seen receipts and "not answered yet".
//
//	seen      — each person's latest read/delivered time per chat, from msys participants
//	seen_log  — one row every time a person's read time moves: a history the phone keeps no copy of
//	threads.waiting — true when the chat's newest real message is from someone else
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		signedIn := `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`
		threads, err := app.FindCollectionByNameOrId("threads")
		if err != nil {
			return err
		}
		threads.Fields.Add(&core.BoolField{Name: "waiting"})
		threads.AddIndex("idx_threads_waiting", false, "waiting, last_ts", "")
		if err := app.Save(threads); err != nil {
			return err
		}
		seen := core.NewBaseCollection("seen")
		seen.ListRule, seen.ViewRule = &signedIn, &signedIn
		seen.Fields.Add(
			&core.TextField{Name: "thread_key", Required: true, Max: 64},
			&core.TextField{Name: "contact", Required: true, Max: 64},
			&core.TextField{Name: "name", Max: 500},
			&core.NumberField{Name: "read_ts", OnlyInt: true},
			&core.NumberField{Name: "delivered_ts", OnlyInt: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		seen.AddIndex("idx_seen_key", true, "thread_key, contact", "")
		if err := app.Save(seen); err != nil {
			return err
		}
		log := core.NewBaseCollection("seen_log")
		log.ListRule, log.ViewRule = &signedIn, &signedIn
		log.Fields.Add(
			&core.TextField{Name: "thread_key", Required: true, Max: 64},
			&core.TextField{Name: "contact", Required: true, Max: 64},
			&core.TextField{Name: "name", Max: 500},
			&core.NumberField{Name: "read_ts", OnlyInt: true},
			&core.NumberField{Name: "prev_ts", OnlyInt: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		log.AddIndex("idx_seen_log_thread", false, "thread_key, read_ts", "")
		return app.Save(log)
	}, nil)
}
