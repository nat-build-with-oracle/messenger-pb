// Daily forum posts: a rule with `daily` on posts each Bangkok day of its chat into its own
// Discord forum post, named "dd/mm/yyyy". `forward_days` remembers which forum post (thread id)
// belongs to which day, one row per (rule, day), so the next message of the same day joins the
// same post. Superuser-only, like `forwards`.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		forwards, err := app.FindCollectionByNameOrId("forwards")
		if err != nil {
			return err
		}
		forwards.Fields.Add(&core.BoolField{Name: "daily"})
		if err := app.Save(forwards); err != nil {
			return err
		}
		days := core.NewBaseCollection("forward_days")
		days.Fields.Add(
			&core.RelationField{Name: "forward", Required: true, CollectionId: forwards.Id, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "day", Required: true, Max: 10},
			&core.TextField{Name: "thread_id", Required: true, Max: 32},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		days.AddIndex("idx_forward_days_day", true, "forward, day", "")
		return app.Save(days)
	}, func(app core.App) error {
		if c, err := app.FindCollectionByNameOrId("forward_days"); err == nil {
			if err := app.Delete(c); err != nil {
				return err
			}
		}
		if c, err := app.FindCollectionByNameOrId("forwards"); err == nil {
			c.Fields.RemoveByName("daily")
			return app.Save(c)
		}
		return nil
	})
}
