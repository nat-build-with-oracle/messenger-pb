// send-queue inside PocketBase — the approval gate for anything an agent sends into a
// real chat. Ported from send-queue (Nexus Oracle, 2026-09-24), same invariants:
//   - nothing is recorded sent unless its CURRENT revision was approved;
//   - revising drops the draft back to pending (approval binds to the exact text);
//   - the agent can never approve its own drafts.
//
// The original enforced the last rule by network address (approve refused from the agent's
// host). Here it is identity: the agent logs in as a `users` record with role=agent, the
// reviewer is a superuser or role=reviewer, and every state change goes through sq.go.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		if users.Fields.GetByName("role") == nil {
			users.Fields.Add(&core.SelectField{Name: "role", MaxSelect: 1, Values: []string{"agent", "reviewer"}})
			if err := app.Save(users); err != nil {
				return err
			}
		}

		// Reads: agents and reviewers. Writes: none through the generic record API — every
		// state change is a route in sq.go, so no client can set status directly.
		signedIn := `@request.auth.collectionName = "users" && (@request.auth.role = "agent" || @request.auth.role = "reviewer")`

		sends := core.NewBaseCollection("sends")
		sends.ListRule, sends.ViewRule = &signedIn, &signedIn
		sends.Fields.Add(
			&core.SelectField{Name: "platform", Required: true, MaxSelect: 1, Values: []string{"Messenger", "LINE"}},
			&core.TextField{Name: "chat", Required: true, Max: 500},
			&core.TextField{Name: "thread_key", Max: 64},
			&core.TextField{Name: "message", Required: true, Max: 20000},
			&core.NumberField{Name: "rev", OnlyInt: true, Min: ptr(1.0)},
			&core.TextField{Name: "batch", Max: 100},
			&core.TextField{Name: "part", Max: 20},
			&core.TextField{Name: "note", Max: 5000},
			&core.TextField{Name: "source_note", Max: 5000},
			&core.URLField{Name: "source_url"},
			&core.SelectField{Name: "status", Required: true, MaxSelect: 1,
				Values: []string{"pending", "approved", "rejected", "sent", "failed"}},
			&core.BoolField{Name: "archived"},
			&core.DateField{Name: "sent_at"},
			&core.TextField{Name: "proof", Max: 5000},
			// Protected: a screenshot of a private chat needs a file token, not just its URL.
			&core.FileField{Name: "proof_img", MaxSelect: 1, MaxSize: 8 << 20, Protected: true,
				MimeTypes: []string{"image/png", "image/jpeg", "image/webp"}},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		sends.AddIndex("idx_sends_status", false, "status, archived", "")
		if err := app.Save(sends); err != nil {
			return err
		}

		events := core.NewBaseCollection("send_events")
		events.ListRule, events.ViewRule = &signedIn, &signedIn
		events.Fields.Add(
			&core.RelationField{Name: "send", Required: true, CollectionId: sends.Id, MaxSelect: 1, CascadeDelete: true},
			&core.SelectField{Name: "kind", Required: true, MaxSelect: 1, Values: []string{
				"queued", "revised", "comment", "feedback", "approved", "rejected",
				"sent", "failed", "notified", "notify_failed"}},
			&core.TextField{Name: "by", Required: true, Max: 200},
			&core.NumberField{Name: "rev", OnlyInt: true},
			&core.TextField{Name: "body", Max: 20000},
			&core.TextField{Name: "comment", Max: 5000},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		events.AddIndex("idx_send_events_send", false, "send", "")
		return app.Save(events)
	}, func(app core.App) error {
		for _, name := range []string{"send_events", "sends"} {
			if c, err := app.FindCollectionByNameOrId(name); err == nil {
				if err := app.Delete(c); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func ptr[T any](v T) *T { return &v }
