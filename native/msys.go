// msys merge: the app's msys_database holds what the fts search index never
// sees — group chats, replies, unsent markers, call/system lines, stickers and
// attachments — as a rolling cache of recent messages. Thread keys never collide
// with fts ids, so the two sources union cleanly.
package main

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type reply struct {
	Text    string `json:"text,omitempty"`
	To      string `json:"to,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

type attachment struct {
	ID    string `json:"id"`
	Type  int    `json:"type"` // 1 sticker, 2 image, 3/15 gif, 4 video, 7 link/share card
	W     int    `json:"w,omitempty"`
	H     int    `json:"h,omitempty"`
	Title string `json:"title,omitempty"`
	Sub   string `json:"sub,omitempty"`
	File  string `json:"file,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Mime  string `json:"mime,omitempty"`
}

func openMsys() *sql.DB {
	path := envStr("MESSENGER_MSYS", msysDB)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil || db.Ping() != nil {
		return nil
	}
	return db
}

// msysMsgs returns msys messages (newest-first per the caller's ORDER BY) with replies + attachments.
func msysMsgs(db *sql.DB, where string, args ...any) ([]apiMsg, error) {
	rows, err := db.Query(`SELECT message_id, CAST(thread_key AS TEXT), timestamp_ms, CAST(sender_id AS TEXT),
		COALESCE(text,''), COALESCE(is_admin_message,0), COALESCE(is_unsent,0),
		COALESCE(reply_message_text,''), COALESCE(CAST(reply_to_user_id AS TEXT),''), COALESCE(reply_snippet,''),
		reply_source_id IS NOT NULL
		FROM messages `+where, args...)
	if err != nil {
		return nil, err
	}
	out := []apiMsg{}
	byID := map[string]int{}
	for rows.Next() {
		var m apiMsg
		var admin, unsent, hasReply bool
		var r reply
		if err := rows.Scan(&m.ID, &m.Thread, &m.TS, &m.Sender, &m.Text, &admin, &unsent,
			&r.Text, &r.To, &r.Snippet, &hasReply); err != nil {
			rows.Close()
			return nil, err
		}
		switch {
		case admin:
			m.Kind = "admin"
		case unsent:
			m.Kind = "unsent"
		}
		if hasReply {
			m.Reply = &r
		}
		byID[m.ID] = len(out)
		out = append(out, m)
	}
	rows.Close()
	if len(out) == 0 {
		return out, rows.Err()
	}
	ids := make([]any, 0, len(out))
	for _, m := range out {
		ids = append(ids, m.ID)
	}
	arows, err := db.Query(`SELECT message_id, CAST(attachment_fbid AS TEXT), COALESCE(attachment_type,0),
		COALESCE(preview_width,0), COALESCE(preview_height,0), COALESCE(title_text,''), COALESCE(subtitle_text,''),
		COALESCE(filename,''), COALESCE(filesize,0), COALESCE(NULLIF(playable_url_mime_type,''), preview_url_mime_type, '')
		FROM attachments WHERE message_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) ORDER BY attachment_index`, ids...)
	if err != nil {
		return out, nil // messages without attachments beat no messages
	}
	defer arows.Close()
	for arows.Next() {
		var mid string
		var a attachment
		if arows.Scan(&mid, &a.ID, &a.Type, &a.W, &a.H, &a.Title, &a.Sub, &a.File, &a.Size, &a.Mime) == nil {
			if i, ok := byID[mid]; ok {
				out[i].Atts = append(out[i].Atts, a)
			}
		}
	}
	return out, nil
}

type threadRow struct {
	ID      string `json:"id"`
	C       int    `json:"c"`
	Last    int64  `json:"last"`
	Preview string `json:"preview"`
	Sender  string `json:"sender,omitempty"`
	Group   bool   `json:"group,omitempty"`
}

// msysThreads: every thread the app knows (incl. groups with no cached messages yet).
func msysThreads(db *sql.DB) []threadRow {
	rows, err := db.Query(`SELECT CAST(t.thread_key AS TEXT), COALESCE(t.last_activity_timestamp_ms,0),
		COALESCE(t.snippet,''), COALESCE(CAST(t.snippet_sender_contact_id AS TEXT),''), t.thread_type,
		(SELECT COUNT(*) FROM messages m WHERE m.thread_key=t.thread_key)
		FROM threads t`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []threadRow
	for rows.Next() {
		var t threadRow
		var typ int
		if rows.Scan(&t.ID, &t.Last, &t.Preview, &t.Sender, &typ, &t.C) == nil {
			if t.C == 0 && t.Preview == "" {
				continue // folder/community shells with nothing to show
			}
			if t.Last < 1e11 {
				t.Last *= 1000 // a few rows store seconds
			}
			t.Group = typ != 1 && typ != 15
			out = append(out, t)
		}
	}
	return out
}

// --- media: attachment previews, cached on the phone like avatars -------------

var (
	mediaDir = envStr("MESSENGER_MEDIA", "/data/local/tmp/messenger-media")
	reE2EE   = regexp.MustCompile(`^e[0-9]+$`)                     // client_attachments pk → local plaintext file
	reMedia  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._$-]*$`) // fbids, plus "ee.mid.$…" for link cards
)

func mediaURL(db *sql.DB, fbid string) string {
	var u string
	_ = db.QueryRow(`SELECT COALESCE(NULLIF(preview_url,''), playable_url, '') FROM attachments
		WHERE CAST(attachment_fbid AS TEXT)=? LIMIT 1`, fbid).Scan(&u)
	return u
}

func handleMedia(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if db == nil || !reMedia.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		if reE2EE.MatchString(id) {
			serveE2EEMedia(db, w, r, id)
			return
		}
		path := filepath.Join(mediaDir, id)
		if _, err := os.Stat(path); err != nil {
			if last, missed := avatarMiss.Load("m" + id); !missed || time.Since(last.(time.Time)) > 10*time.Minute {
				if fetchURL(mediaURL(db, id), path) != nil {
					avatarMiss.Store("m"+id, time.Now())
				}
			}
		}
		if _, err := os.Stat(path); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("cache-control", "max-age=31536000, immutable")
		http.ServeFile(w, r, path)
	}
}

// prefetchMedia caches every attachment preview while its signed URL is valid.
func prefetchMedia(db *sql.DB) {
	if db == nil {
		return
	}
	// images only: files (PDFs etc.) download on click, not in the background
	rows, err := db.Query(`SELECT CAST(attachment_fbid AS TEXT), COALESCE(preview_url,'') FROM attachments
		WHERE preview_url_mime_type LIKE 'image/%'`)
	if err != nil {
		return
	}
	type job struct{ id, url string }
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.url) == nil && j.url != "" && reMedia.MatchString(j.id) {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		path := filepath.Join(mediaDir, j.id)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if last, missed := avatarMiss.Load("m" + j.id); missed && time.Since(last.(time.Time)) < time.Hour {
			continue
		}
		if fetchURL(j.url, path) != nil {
			avatarMiss.Store("m"+j.id, time.Now())
		}
	}
}
