package main

// Draft requests (migration 1792160000): the page's "AI ร่าง" asks, the reply desk answers.
//
//	POST /api/sq/draft-requests              reviewer  {link}            → the request (an open one for the same chat is reused)
//	POST /api/sq/draft-requests/next         agent                       → claims the oldest open request, or 204
//	POST /api/sq/draft-requests/{id}/close   agent     {status,send,note} (status done | skipped | failed)
//
// A request left "taken" for 15 minutes counts as open again (the desk died mid-draft).

import (
	"net/http"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const draftStale = 15 * time.Minute

var draftMu sync.Mutex // one claim at a time, so two desks never take the same request

func registerDraftRequests(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.POST("/api/sq/draft-requests", func(e *core.RequestEvent) error {
			if sqRole(e) != "reviewer" {
				return e.ForbiddenError("sign in as reviewer or superuser", nil)
			}
			var b struct{ Link string }
			if err := e.BindBody(&b); err != nil {
				return e.BadRequestError("bad body", err)
			}
			platform, key, err := parseChatRef(b.Link, "")
			if err != nil {
				return e.BadRequestError(err.Error(), nil)
			}
			chat := ""
			if platform == "Messenger" {
				t, err := e.App.FindFirstRecordByFilter("threads", "key = {:k}", dbx.Params{"k": key})
				if err != nil {
					return e.BadRequestError("no Messenger thread with key "+key, nil)
				}
				chat = t.GetString("name")
			}
			if readOnlyChats()[chat] {
				return e.ForbiddenError(`"`+chat+`" is read-only — no drafts`, nil)
			}
			if r, err := e.App.FindFirstRecordByFilter("draft_requests", "thread_key = {:k} && (status = 'open' || status = 'taken')",
				dbx.Params{"k": key}); err == nil {
				return e.JSON(http.StatusOK, r) // already asked: show that one
			}
			col, err := e.App.FindCollectionByNameOrId("draft_requests")
			if err != nil {
				return err
			}
			r := core.NewRecord(col)
			r.Set("platform", platform)
			r.Set("thread_key", key)
			r.Set("chat", chat)
			r.Set("link", "#"+map[string]string{"Messenger": "c=", "LINE": "l="}[platform]+key)
			r.Set("by", actorOf(e))
			r.Set("status", "open")
			if err := e.App.Save(r); err != nil {
				return e.BadRequestError("could not record the request", err)
			}
			return e.JSON(http.StatusOK, r)
		})

		se.Router.POST("/api/sq/draft-requests/next", func(e *core.RequestEvent) error {
			if sqRole(e) != "agent" {
				return e.ForbiddenError("sign in as agent", nil)
			}
			draftMu.Lock()
			defer draftMu.Unlock()
			stale := time.Now().Add(-draftStale).UTC().Format("2006-01-02 15:04:05.000Z")
			r, err := e.App.FindFirstRecordByFilter("draft_requests",
				"status = 'open' || (status = 'taken' && updated < {:stale})", dbx.Params{"stale": stale})
			if err != nil {
				return e.NoContent(http.StatusNoContent)
			}
			r.Set("status", "taken")
			r.Set("taken_by", actorOf(e))
			if err := e.App.Save(r); err != nil {
				return err
			}
			return e.JSON(http.StatusOK, r)
		})

		se.Router.POST("/api/sq/draft-requests/{id}/close", func(e *core.RequestEvent) error {
			if sqRole(e) != "agent" {
				return e.ForbiddenError("sign in as agent", nil)
			}
			var b struct{ Status, Send, Note string }
			if err := e.BindBody(&b); err != nil {
				return e.BadRequestError("bad body", err)
			}
			if b.Status != "done" && b.Status != "skipped" && b.Status != "failed" {
				return e.BadRequestError("status must be done, skipped or failed", nil)
			}
			r, err := e.App.FindRecordById("draft_requests", e.Request.PathValue("id"))
			if err != nil {
				return e.NotFoundError("", nil)
			}
			if r.GetString("status") != "taken" {
				return e.BadRequestError("request is "+r.GetString("status")+", not taken", nil)
			}
			r.Set("status", b.Status)
			r.Set("send", b.Send)
			r.Set("note", b.Note)
			if err := e.App.Save(r); err != nil {
				return err
			}
			return e.JSON(http.StatusOK, r)
		})
		return se.Next()
	})
}
