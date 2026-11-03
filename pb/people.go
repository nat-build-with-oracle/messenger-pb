// GET /api/people: the people in the archive, ids and names only, never a message body, so
// people-oracle (role viewer) can build its graph without reading any chat.
//
//	GET /api/people?page=1&perPage=500[&v=2]
//	{schema: "messenger-archive/people@1", generated_at_ms, page, perPage, totalItems, totalPages,
//	 items: [{ns, id, name, origin, first_ts, last_ts}]}
//
//	ns      facebook:user (fbid), facebook:messenger-route (thread key), line:user (mid), line:group, line:room
//	        people@2 (?v=2) names the LINE ones line:app-user, line:app-group, line:app-room: they are the
//	        phone LINE app's ids, a different id space from the Messaging API's userId/groupId (same shape,
//	        not the same person key). @1 stays until people-oracle has switched.
//	        LINE ids in Messaging API form: U/C/R + 32 lowercase hex (memberships too)
//	origin  contact (contacts / LINE contacts) | sender (seen only as a message's sender)
//	        | thread (Messenger thread) | line-chat (LINE chat); one row per (ns, id): contact wins
//	first_ts, last_ts  first and last message by (or in) it, ms; null when there is none
//
// Sorted by ns, then id, so pages are stable between calls.
//
//	GET /api/people/memberships?page=1&perPage=500[&v=2]
//	{schema: "messenger-archive/memberships@1", …same envelope…,
//	 items: [{platform, chat_id, member_id, count, first_ms, last_ms}]}   (@2: platform "line-app")
//
// One row per person per chat they WROTE in (a silent member is not listed): chat_id = Messenger
// thread key or LINE chat id, count = their messages there. Sorted by platform, chat_id, member_id.
// Consumers validate exact keys: adding a key here is a new schema version, never a body field.
package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

type membership struct {
	Platform string `json:"platform" db:"platform"`
	ChatID   string `json:"chat_id" db:"chat_id"`
	MemberID string `json:"member_id" db:"member_id"`
	Count    int64  `json:"count" db:"count"`
	FirstMS  int64  `json:"first_ms" db:"first_ms"`
	LastMS   int64  `json:"last_ms" db:"last_ms"`
}

func memberships(app core.App) ([]membership, error) {
	out := []membership{}
	if err := app.DB().NewQuery(`SELECT 'facebook' AS platform, thread_key AS chat_id, sender AS member_id,
		count(*) AS count, min(ts) AS first_ms, max(ts) AS last_ms
		FROM messages WHERE sender != '' AND thread_key != '' GROUP BY thread_key, sender`).All(&out); err != nil {
		return nil, err
	}
	if db, err := openRO(pick("", "LINE_DB", phoneLine)); err == nil {
		defer db.Close()
		owner := envOr("LINE_OWNER", lineOwnerMid)
		r, err := db.Query(`SELECT chat_id, COALESCE(NULLIF(from_mid,''), ?) AS m, count(*),
			min(CAST(created_time AS INTEGER)), max(CAST(created_time AS INTEGER))
			FROM chat_history WHERE COALESCE(chat_id,'') != '' GROUP BY chat_id, m`, owner)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		for r.Next() {
			m := membership{Platform: "line"}
			if r.Scan(&m.ChatID, &m.MemberID, &m.Count, &m.FirstMS, &m.LastMS) == nil {
				m.ChatID, m.MemberID = lineCanon(m.ChatID), lineCanon(m.MemberID)
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Platform != b.Platform {
			return a.Platform < b.Platform
		}
		if a.ChatID != b.ChatID {
			return a.ChatID < b.ChatID
		}
		return a.MemberID < b.MemberID
	})
	return out, nil
}

// paged answers one page of a sorted list in the envelope consumers validate.
func paged[T any](e *core.RequestEvent, schema string, all []T) error {
	q := e.Request.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("perPage"))
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 500 {
		per = 500
	}
	from, to := min((page-1)*per, len(all)), min(page*per, len(all))
	return e.JSON(http.StatusOK, map[string]any{"schema": schema, "generated_at_ms": time.Now().UnixMilli(),
		"page": page, "perPage": per, "totalItems": len(all), "totalPages": (len(all) + per - 1) / per, "items": all[from:to]})
}

type person struct {
	NS      string `json:"ns"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Origin  string `json:"origin"`
	FirstTS *int64 `json:"first_ts"`
	LastTS  *int64 `json:"last_ts"`
}

// span: first and last message time (ms) of one id, read from a GROUP BY.
type span struct {
	ID      string `db:"id"`
	Name    string `db:"name"`
	FirstTS int64  `db:"first_ts"`
	LastTS  int64  `db:"last_ts"`
}

// originRank: when one (ns, id) comes from several places, the first origin here names it.
var originRank = map[string]int{"contact": 0, "sender": 1, "thread": 2, "line-chat": 3}

func people(app core.App) ([]person, error) {
	byKey := map[string]*person{}
	add := func(ns, id, name, origin string, sp *span) {
		if id == "" {
			return
		}
		if strings.HasPrefix(ns, "line:") {
			id = lineCanon(id)
		}
		k := ns + "\x00" + id
		p := byKey[k]
		if p == nil {
			p = &person{NS: ns, ID: id, Name: name, Origin: origin}
			byKey[k] = p
		} else if originRank[origin] < originRank[p.Origin] {
			p.Origin = origin
			if name != "" {
				p.Name = name
			}
		} else if p.Name == "" {
			p.Name = name
		}
		if sp != nil && sp.LastTS > 0 {
			if p.FirstTS == nil || sp.FirstTS < *p.FirstTS {
				p.FirstTS = &sp.FirstTS
			}
			if p.LastTS == nil || sp.LastTS > *p.LastTS {
				p.LastTS = &sp.LastTS
			}
		}
	}
	db := app.DB()
	var rows []span
	if err := db.NewQuery(`SELECT fbid AS id, name, 0 AS first_ts, 0 AS last_ts FROM contacts`).All(&rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		add("facebook:user", r.ID, r.Name, "contact", nil)
	}
	rows = nil
	if err := db.NewQuery(`SELECT sender AS id, max(sender_name) AS name, min(ts) AS first_ts, max(ts) AS last_ts
		FROM messages WHERE sender != '' GROUP BY sender`).All(&rows); err != nil {
		return nil, err
	}
	for i := range rows {
		add("facebook:user", rows[i].ID, rows[i].Name, "sender", &rows[i])
	}
	var threads []span
	if err := db.NewQuery(`SELECT t.key AS id, t.name AS name, COALESCE(min(m.ts),0) AS first_ts, COALESCE(max(m.ts),0) AS last_ts
		FROM threads t LEFT JOIN messages m ON m.thread_key = t.key GROUP BY t.key`).All(&threads); err != nil {
		return nil, err
	}
	for i := range threads {
		add("facebook:messenger-route", threads[i].ID, threads[i].Name, "thread", &threads[i])
	}

	// LINE lives in the app's own database on the phone; none on m5, so no LINE rows there.
	if ldb, err := openRO(pick("", "LINE_DB", phoneLine)); err == nil {
		defer ldb.Close()
		names := lineNames(ldb)
		owner := envOr("LINE_OWNER", lineOwnerMid)
		for id, n := range names {
			if lineNS(id) == "line:user" {
				add("line:user", id, n, "contact", nil)
			}
		}
		scan := func(q, origin string, ns func(string) string, args ...any) error {
			r, err := ldb.Query(q, args...)
			if err != nil {
				return err
			}
			defer r.Close()
			for r.Next() {
				var sp span
				if r.Scan(&sp.ID, &sp.FirstTS, &sp.LastTS) == nil {
					add(ns(sp.ID), sp.ID, names[sp.ID], origin, &sp)
				}
			}
			return nil
		}
		if err := scan(`SELECT COALESCE(NULLIF(from_mid,''), ?) AS id, min(CAST(created_time AS INTEGER)), max(CAST(created_time AS INTEGER))
			FROM chat_history GROUP BY id`, "sender", func(string) string { return "line:user" }, owner); err != nil {
			return nil, err
		}
		if err := scan(`SELECT c.chat_id, COALESCE(min(CAST(h.created_time AS INTEGER)),0), COALESCE(max(CAST(h.created_time AS INTEGER)),0)
			FROM chat c LEFT JOIN chat_history h ON h.chat_id = c.chat_id GROUP BY c.chat_id`, "line-chat", lineNS); err != nil {
			return nil, err
		}
	}
	out := make([]person, 0, len(byKey))
	for _, p := range byKey {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NS != out[j].NS {
			return out[i].NS < out[j].NS
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// lineCanon: the phone's LINE database keeps ids as u/c/r + 32 hex; the Messaging API (and
// people-oracle) write U/C/R + the same hex. Ids leave this endpoint in that canonical form.
func lineCanon(id string) string {
	if id == "" {
		return id
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// lineNS: a LINE id starts with u (a person, and a 1:1 chat has the person's id), c (a group) or r (a room).
func lineNS(id string) string {
	switch {
	case id != "" && id[0] == 'c':
		return "line:group"
	case id != "" && id[0] == 'r':
		return "line:room"
	}
	return "line:user"
}

func registerPeople(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/people", func(e *core.RequestEvent) error {
			if !canRead(sqRole(e)) {
				return e.ForbiddenError("sign in first", nil)
			}
			all, err := people(e.App)
			if err != nil {
				return e.InternalServerError("read people", err)
			}
			if e.Request.URL.Query().Get("v") == "2" {
				for i := range all {
					if strings.HasPrefix(all[i].NS, "line:") {
						all[i].NS = "line:app-" + strings.TrimPrefix(all[i].NS, "line:")
					}
				}
				sort.Slice(all, func(i, j int) bool {
					if all[i].NS != all[j].NS {
						return all[i].NS < all[j].NS
					}
					return all[i].ID < all[j].ID
				})
				return paged(e, "messenger-archive/people@2", all)
			}
			return paged(e, "messenger-archive/people@1", all)
		})
		se.Router.GET("/api/people/memberships", func(e *core.RequestEvent) error {
			if !canRead(sqRole(e)) {
				return e.ForbiddenError("sign in first", nil)
			}
			all, err := memberships(e.App)
			if err != nil {
				return e.InternalServerError("read memberships", err)
			}
			if e.Request.URL.Query().Get("v") == "2" {
				for i := range all {
					if all[i].Platform == "line" {
						all[i].Platform = "line-app"
					}
				}
				return paged(e, "messenger-archive/memberships@2", all) // "line-app" sorts like "line": order unchanged
			}
			return paged(e, "messenger-archive/memberships@1", all)
		})
		return se.Next()
	})
}
