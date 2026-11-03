// Routes for the sender (users.role = sender): a fixed program that delivers drafts a
// reviewer approved — never the agent, and never able to approve. It reads the outbox,
// claims one approved revision (approved → sending, which locks the text), delivers it,
// and reports sent or failed with proof. No route here retries anything.
package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

func registerSender(app *pocketbase.PocketBase) {
	sender := func(h func(*core.RequestEvent) error) func(*core.RequestEvent) error {
		return func(e *core.RequestEvent) error {
			if r := sqRole(e); r != "sender" {
				return e.ForbiddenError(fmt.Sprintf("needs role sender (you are %q)", r), nil)
			}
			return h(e)
		}
	}
	conflict := func(e *core.RequestEvent, format string, a ...any) error {
		return e.JSON(http.StatusConflict, map[string]any{"error": fmt.Sprintf(format, a...)})
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		// Approved drafts for one platform, oldest first, with the exact text to deliver.
		se.Router.GET("/api/sq/outbox", sender(func(e *core.RequestEvent) error {
			filter, params := "status = 'approved' && archived = false", map[string]any{}
			if p := e.Request.URL.Query().Get("platform"); p != "" {
				filter += " && platform = {:p}"
				params["p"] = p
			}
			rs, err := e.App.FindRecordsByFilter("sends", filter, "created", 20, 0, params)
			if err != nil {
				return err
			}
			out := []map[string]any{}
			for _, r := range rs {
				out = append(out, map[string]any{"id": r.Id, "rev": r.GetInt("rev"), "platform": r.GetString("platform"),
					"chat": r.GetString("chat"), "threadKey": r.GetString("thread_key"), "message": r.GetString("message")})
			}
			return e.JSON(http.StatusOK, out)
		}))

		// approved + the revision the sender read → sending. Two senders cannot both win.
		se.Router.POST("/api/sq/sends/{id}/claim", sender(func(e *core.RequestEvent) error {
			var b struct{ Rev int }
			_ = e.BindBody(&b)
			var r *core.Record
			var refused string
			err := e.App.RunInTransaction(func(tx core.App) error {
				var err error
				if r, err = tx.FindRecordById("sends", e.Request.PathValue("id")); err != nil {
					return err
				}
				if s, cur := r.GetString("status"), r.GetInt("rev"); s != "approved" || cur != b.Rev {
					refused = fmt.Sprintf("status %s at rev %d — not approved rev %d, refusing", s, cur, b.Rev)
					return nil
				}
				r.Set("status", "sending")
				return tx.Save(r)
			})
			if err != nil {
				return e.NotFoundError("", err)
			}
			if refused != "" {
				return conflict(e, "%s", refused)
			}
			sqEvent(e.App, r, "sending", actorOf(e), "", "")
			return e.JSON(http.StatusOK, map[string]any{"id": r.Id, "rev": r.GetInt("rev"), "status": "sending",
				"chat": r.GetString("chat"), "threadKey": r.GetString("thread_key"), "message": r.GetString("message")})
		}))

		// sending + the same revision → sent or failed, with proof (multipart for proof_img).
		se.Router.POST("/api/sq/sends/{id}/done", sender(func(e *core.RequestEvent) error {
			r, err := e.App.FindRecordById("sends", e.Request.PathValue("id"))
			if err != nil {
				return e.NotFoundError("", err)
			}
			var b struct {
				Rev   int    `json:"rev" form:"rev"`
				OK    bool   `json:"ok" form:"ok"`
				Proof string `json:"proof" form:"proof"`
			}
			if err := e.BindBody(&b); err != nil {
				return e.BadRequestError("bad body", err)
			}
			if s, cur := r.GetString("status"), r.GetInt("rev"); s != "sending" || cur != b.Rev {
				return conflict(e, "status %s at rev %d — not sending rev %d", s, cur, b.Rev)
			}
			if strings.TrimSpace(b.Proof) == "" {
				return e.BadRequestError("proof is required", nil)
			}
			to := map[bool]string{true: "sent", false: "failed"}[b.OK]
			r.Set("status", to)
			r.Set("proof", b.Proof)
			if b.OK {
				r.Set("sent_at", types.NowDateTime())
			}
			if files, err := e.FindUploadedFiles("proof_img"); err == nil && len(files) > 0 {
				r.Set("proof_img", files[0])
			}
			if err := e.App.Save(r); err != nil {
				return err
			}
			sqEvent(e.App, r, to, actorOf(e), "", b.Proof)
			sqNotify(e.App, r, to+" by the sender", b.Proof, actorOf(e))
			return e.JSON(http.StatusOK, map[string]any{"id": r.Id, "rev": r.GetInt("rev"), "status": to})
		}))
		return se.Next()
	})
}
