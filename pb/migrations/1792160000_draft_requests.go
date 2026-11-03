// draft_requests: "AI ร่าง" from the page. A reviewer asks for a draft for one chat; the reply desk
// on m5 (`just desk`) claims the request, runs `just draft`, and closes it with the draft it queued.
// The server only records the ask: it runs no AI. Reviewer and agent may read; every write goes
// through /api/sq/draft-requests (sqdraft.go), so the state changes stay in one place.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		c := core.NewBaseCollection("draft_requests")
		c.Fields.Add(
			&core.SelectField{Name: "platform", Required: true, MaxSelect: 1, Values: []string{"Messenger", "LINE"}},
			&core.TextField{Name: "thread_key", Required: true, Max: 64},
			&core.TextField{Name: "chat", Max: 500},
			&core.TextField{Name: "link", Required: true, Max: 100},
			&core.TextField{Name: "by", Max: 200},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1, Values: []string{"open", "taken", "done", "skipped", "failed"}},
			&core.TextField{Name: "taken_by", Max: 200},
			&core.TextField{Name: "send", Max: 30},
			&core.TextField{Name: "note", Max: 2000},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		rule := `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`
		c.ListRule, c.ViewRule = &rule, &rule
		c.AddIndex("idx_draft_requests_status", false, "status", "")
		return app.Save(c)
	}, func(app core.App) error {
		if c, err := app.FindCollectionByNameOrId("draft_requests"); err == nil {
			return app.Delete(c)
		}
		return nil
	})
}
