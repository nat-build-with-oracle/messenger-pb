// LINE tab: read-only routes over the LINE app's own databases on the phone (naver_line for
// chats and messages, contact for names). Nothing is copied into PocketBase; every request
// reads the live file, so the tab shows what the app has synced. Same sign-in as the archive.
//
// LINE_DB / LINE_CONTACT_DB override the phone paths; a missing db answers 404.
// The owner's own messages carry either their mid or an empty from_mid.
package main

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const (
	phoneLine        = "/data/data/jp.naver.line.android/databases/naver_line"
	phoneLineContact = "/data/data/jp.naver.line.android/databases/contact"
	// Photos the app has opened: <dir>/<chat id>/messages/<chat_history.id>[.thumb], plain JPEG.
	phoneLineChats = "/storage/emulated/0/Android/data/jp.naver.line.android/files/chats"
	lineOwnerMid   = "" // the phone owner's LINE mid: set LINE_OWNER (u + 32 hex)
)

var reLineChat = regexp.MustCompile(`^[a-z][0-9a-f]{32}$`)

type lineChat struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Group   bool   `json:"group"`
	Last    int64  `json:"last"`
	Preview string `json:"preview"`
	Waiting bool   `json:"waiting"` // the newest message is from someone else
}

type lineMsg struct {
	ID   int64  `json:"id"`
	From string `json:"from"`
	Name string `json:"name"`
	Me   bool   `json:"me"`
	TS   int64  `json:"ts"`
	Kind string `json:"kind"` // text | media | system
	Text string `json:"text"`
	// Photo: true when the app keeps the file (see /chat/line/{chat}/{id}).
	Photo bool `json:"photo,omitempty"`
}

// lineNames: contact mid → the name the phone shows (the owner's override first), plus group names.
func lineNames(line *sql.DB) map[string]string {
	names := map[string]string{}
	if c, err := openRO(pick("", "LINE_CONTACT_DB", phoneLineContact)); err == nil {
		defer c.Close()
		if rows, err := c.Query(`SELECT mid, COALESCE(NULLIF(overridden_name,''), profile_name, '') FROM contacts`); err == nil {
			for rows.Next() {
				var id, n string
				if rows.Scan(&id, &n) == nil {
					names[id] = n
				}
			}
			rows.Close()
		}
	}
	if rows, err := line.Query(`SELECT id, COALESCE(name,'') FROM groups`); err == nil {
		for rows.Next() {
			var id, n string
			if rows.Scan(&id, &n) == nil && n != "" {
				names[id] = n
			}
		}
		rows.Close()
	}
	return names
}

// lineText turns one chat_history row into what the chat shows. Anything not understood
// becomes a bracketed label rather than disappearing.
func lineText(typ, attType int, content, param, location string) (kind, text string) {
	switch {
	case location != "":
		return "text", "📍 " + location
	case content != "":
		return "text", content
	case typ == 5 || strings.Contains(param, "STKID"):
		return "media", "[สติกเกอร์]"
	case strings.Contains(param, "FLEX_JSON"):
		return "media", "[การ์ดข้อความ]"
	}
	switch attType {
	case 1:
		return "media", "[รูป]"
	case 2:
		return "media", "[วิดีโอ]"
	case 3:
		return "media", "[ข้อความเสียง]"
	case 14:
		return "media", "[ไฟล์]"
	}
	if typ == 1 {
		return "media", "[ไฟล์แนบ]"
	}
	return "system", "[ข้อความระบบ ประเภท " + strconv.Itoa(typ) + "]"
}

// linePhoto: the cached file for one photo message, full size first, else the thumbnail.
func linePhoto(chat string, id int64) string {
	dir := filepath.Join(envOr("LINE_CHATS_DIR", phoneLineChats), chat, "messages")
	for _, name := range []string{strconv.FormatInt(id, 10), strconv.FormatInt(id, 10) + ".thumb"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.Mode().IsRegular() {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

func registerLine(app *pocketbase.PocketBase) {
	open := func(e *core.RequestEvent) (*sql.DB, error) {
		if !canRead(sqRole(e)) {
			return nil, e.ForbiddenError("sign in first", nil)
		}
		db, err := openRO(pick("", "LINE_DB", phoneLine))
		if err != nil {
			return nil, e.NotFoundError("no LINE database on this machine", nil)
		}
		return db, nil
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/line/chats", func(e *core.RequestEvent) error {
			db, err := open(e)
			if err != nil {
				return err
			}
			defer db.Close()
			names := lineNames(db)
			rows, err := db.Query(`SELECT chat_id, COALESCE(chat_name,''), COALESCE(type,0), COALESCE(last_from_mid,''),
				COALESCE(last_message,''), CAST(COALESCE(last_created_time,'0') AS INTEGER) FROM chat
				ORDER BY CAST(COALESCE(last_created_time,'0') AS INTEGER) DESC`)
			if err != nil {
				return e.InternalServerError("read LINE chats", err)
			}
			defer rows.Close()
			out := []lineChat{}
			for rows.Next() {
				var c lineChat
				var typ int
				var from string
				if rows.Scan(&c.ID, &c.Name, &typ, &from, &c.Preview, &c.Last) != nil {
					continue
				}
				c.Group = typ != 1
				c.Waiting = from != "" && from != envOr("LINE_OWNER", lineOwnerMid)
				if c.Name == "" {
					c.Name = names[c.ID]
				}
				if c.Group && from != "" && names[from] != "" && !strings.Contains(c.Preview, names[from]) {
					c.Preview = strings.Fields(names[from])[0] + ": " + c.Preview
				}
				out = append(out, c)
			}
			return e.JSON(http.StatusOK, out)
		})

		// A photo as JPEG, behind a file token like the other chat pictures (sqmedia.go).
		se.Router.GET("/chat/line/{chat}/{id}", func(e *core.RequestEvent) error {
			if err := checkFileToken(e); err != nil {
				return err
			}
			chat := e.Request.PathValue("chat")
			id, err := strconv.ParseInt(e.Request.PathValue("id"), 10, 64)
			if !reLineChat.MatchString(chat) || err != nil {
				return e.NotFoundError("", nil)
			}
			path := linePhoto(chat, id)
			if path == "" {
				return e.NotFoundError("not in the phone's LINE cache", nil)
			}
			e.Response.Header().Set("Content-Type", "image/jpeg")
			e.Response.Header().Set("Cache-Control", "private, max-age=86400")
			http.ServeFile(e.Response, e.Request, path)
			return nil
		})

		se.Router.GET("/api/line/chats/{id}", func(e *core.RequestEvent) error {
			id := e.Request.PathValue("id")
			if !reLineChat.MatchString(id) {
				return e.NotFoundError("", nil)
			}
			db, err := open(e)
			if err != nil {
				return err
			}
			defer db.Close()
			limit, _ := strconv.Atoi(e.Request.URL.Query().Get("limit")) // newest N; the page asks for more as you scroll up
			if limit <= 0 || limit > 5000 {
				limit = 200
			}
			names := lineNames(db)
			var total int
			_ = db.QueryRow(`SELECT count(*) FROM chat_history WHERE chat_id = ?`, id).Scan(&total)
			rows, err := db.Query(`SELECT id, COALESCE(from_mid,''), COALESCE(type,0), COALESCE(attachement_type,0),
				COALESCE(content,''), COALESCE(parameter,''), COALESCE(location_name,''),
				CAST(COALESCE(created_time,'0') AS INTEGER) FROM chat_history WHERE chat_id = ?
				ORDER BY CAST(COALESCE(created_time,'0') AS INTEGER) DESC, id DESC LIMIT ?`, id, limit)
			if err != nil {
				return e.InternalServerError("read LINE messages", err)
			}
			defer rows.Close()
			owner := envOr("LINE_OWNER", lineOwnerMid)
			msgs := []lineMsg{}
			for rows.Next() {
				var m lineMsg
				var typ, att int
				var content, param, loc string
				if rows.Scan(&m.ID, &m.From, &typ, &att, &content, &param, &loc, &m.TS) != nil {
					continue
				}
				m.Me = m.From == "" || m.From == owner
				m.Name = names[m.From]
				m.Kind, m.Text = lineText(typ, att, content, param, loc)
				if att == 1 {
					m.Photo = linePhoto(id, m.ID) != ""
				}
				msgs = append(msgs, m)
			}
			return e.JSON(http.StatusOK, map[string]any{"name": names[id], "total": total, "items": msgs})
		})
		return se.Next()
	})
}
