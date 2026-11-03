// The sender: a fixed program (not the agent) that delivers drafts a reviewer approved.
// It logs in as a `users` record with role=sender, which may only claim an approved
// revision and report how the send went (sq.go). Claiming moves the draft to `sending`,
// which locks the text: a revise is refused until the sender reports sent or failed.
package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	addValues := func(app core.App, collection, field string, values ...string) error {
		c, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			return err
		}
		f, ok := c.Fields.GetByName(field).(*core.SelectField)
		if !ok {
			return nil
		}
		for _, v := range values {
			if !slices.Contains(f.Values, v) {
				f.Values = append(f.Values, v)
			}
		}
		return app.Save(c)
	}
	m.Register(func(app core.App) error {
		if err := addValues(app, "users", "role", "sender"); err != nil {
			return err
		}
		if err := addValues(app, "sends", "status", "sending"); err != nil {
			return err
		}
		return addValues(app, "send_events", "kind", "sending")
	}, nil)
}
