// Reading the phone's Messenger stores (same three sources as native/):
//
//	fts.db                 messages_content — 1:1 E2EE text history (search index)
//	msys messages          groups / non-E2EE, replies, admin lines, attachments
//	msys client_messages   decrypted E2EE messages incl. non-text
//
// Cursors let `serve` sync only what is new since the last pass.
package main

import (
	"database/sql"
	"os"
	"strings"
)

const (
	phoneFTS  = "/data/data/com.facebook.orca/files/bankAndEcho/echo/AdvancedCrypto/896050346/fts.db"
	phoneMsys = "/data/data/com.facebook.orca/databases/msys_database_896050346"
	ownerID   = "896050346"
)

type srcMsg struct {
	MID, Thread, Sender, Text, Kind, Source string
	TS                                      int64
	Reply                                   map[string]string
	Atts                                    []map[string]any
}

type srcThread struct {
	Key, Name, Preview string
	Folder             string // inbox | pending (message request) | community; "" = unknown
	Group, E2EE        bool
	Last               int64
	Waiting            *bool // nil: no message in this batch says
}

type cursors struct{ FTS, Msys, E2EE int64 }

func openRO(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	return db, db.Ping()
}

func pick(flag, env, phone string) string {
	for _, p := range []string{flag, os.Getenv(env), phone} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func readContacts(ms *sql.DB) (map[string]string, map[string]string) {
	names, pics := map[string]string{}, map[string]string{}
	rows, err := ms.Query(`SELECT CAST(id AS TEXT), COALESCE(name,''), COALESCE(profile_picture_url,'') FROM contacts`)
	if err != nil {
		return names, pics
	}
	defer rows.Close()
	for rows.Next() {
		var id, n, p string
		if rows.Scan(&id, &n, &p) == nil {
			names[id], pics[id] = n, p
		}
	}
	return names, pics
}

// readThreads also returns the keys of E2EE shell threads it folded into their real thread,
// so the store can delete records an older sync created for them.
func readThreads(ms, fts *sql.DB, names map[string]string) ([]srcThread, map[string]string) {
	byKey := map[string]*srcThread{}
	shells := map[string]bool{} // msys threads rows of type 15: an E2EE 1:1 chat's empty twin
	add := func(t srcThread) {
		if t.Last > 0 && t.Last < 1e11 {
			t.Last *= 1000 // a few msys rows store seconds
		}
		if old, ok := byKey[t.Key]; ok {
			if t.Name != "" {
				old.Name = t.Name
			}
			if t.Folder != "" {
				old.Folder = t.Folder
			}
			if t.Last > old.Last {
				old.Last, old.Preview = t.Last, t.Preview
			}
			old.Group = old.Group || t.Group
			old.E2EE = old.E2EE || t.E2EE
			return
		}
		byKey[t.Key] = &t
	}
	if fts != nil {
		rows, err := fts.Query(`SELECT CAST(c1thread_id AS TEXT), MAX(c2timestamp_ms) FROM messages_content GROUP BY 1`)
		if err == nil {
			for rows.Next() {
				var t srcThread
				if rows.Scan(&t.Key, &t.Last) == nil {
					t.E2EE = true
					add(t)
				}
			}
			rows.Close()
		}
	}
	if ms != nil {
		rows, err := ms.Query(`SELECT CAST(thread_key AS TEXT), COALESCE(thread_name,''), thread_type,
			COALESCE(last_activity_timestamp_ms,0), COALESCE(snippet,''), COALESCE(folder_name,'') FROM threads`)
		if err == nil {
			for rows.Next() {
				var t srcThread
				var typ int
				if rows.Scan(&t.Key, &t.Name, &typ, &t.Last, &t.Preview, &t.Folder) == nil {
					t.Group = typ != 1 && typ != 15
					if typ == 15 {
						shells[t.Key] = true
					}
					add(t)
				}
			}
			rows.Close()
		}
		rows, err = ms.Query(`SELECT CAST(t.thread_id AS TEXT), c.display_ts_ms, COALESCE(c.snippet,'')
			FROM client_threads c JOIN advanced_crypto_transport_threads t ON t.client_thread_pk = c.pk`)
		if err == nil {
			for rows.Next() {
				var t srcThread
				if rows.Scan(&t.Key, &t.Last, &t.Preview) == nil {
					t.E2EE = true
					t.Folder = "inbox"
					add(t)
				}
			}
			rows.Close()
		}
	}
	// Groups nobody named: Messenger shows the members' first names ("Alice, Bob, Carol").
	members, other := map[string][]string{}, map[string]string{}
	if ms != nil {
		rows, err := ms.Query(`SELECT CAST(thread_key AS TEXT), CAST(contact_id AS TEXT) FROM participants
			WHERE contact_id <> ` + ownerID + ` ORDER BY thread_key, contact_id`)
		if err == nil {
			for rows.Next() {
				var k, id string
				if rows.Scan(&k, &id) != nil {
					continue
				}
				if other[k] == "" {
					other[k] = id
				}
				if len(members[k]) < 4 && names[id] != "" {
					members[k] = append(members[k], strings.Fields(names[id])[0])
				}
			}
			rows.Close()
		}
	}
	// An E2EE 1:1 chat appears twice in msys: a type-15 row in threads (no messages, no
	// snippet) and the transport thread keyed by the other person's id, which holds the
	// messages. messenger.com shows one chat, so fold the shell into the real one.
	folded := map[string]string{} // shell key -> the real chat it was folded into
	for k := range shells {
		real, ok := byKey[other[k]]
		if !ok || !real.E2EE {
			continue
		}
		if t := byKey[k]; t.Last > real.Last {
			real.Last = t.Last
		}
		if t := byKey[k]; t.Folder != "" && t.Folder != "inbox" { // a request stays a request
			real.Folder = t.Folder
		}
		delete(byKey, k)
		folded[k] = other[k]
	}
	out := make([]srcThread, 0, len(byKey))
	for _, t := range byKey {
		if t.Name == "" {
			t.Name = names[t.Key]
		}
		if t.Name == "" && len(members[t.Key]) > 0 {
			t.Name = strings.Join(members[t.Key], ", ")
		}
		out = append(out, *t)
	}
	return out, folded
}

// readMessages returns everything past the cursors, richest source first
// (e2ee, msys, fts) so thread+timestamp dedupe keeps the best copy.
func readMessages(ms, fts *sql.DB, c cursors) ([]srcMsg, cursors) {
	var out []srcMsg
	if ms != nil {
		e2, next := readE2EE(ms, c.E2EE)
		out, c.E2EE = append(out, e2...), next
		mm, next := readMsys(ms, c.Msys)
		out, c.Msys = append(out, mm...), next
	}
	if fts != nil {
		rows, err := fts.Query(`SELECT docid, CAST(c0message_id AS TEXT), CAST(c1thread_id AS TEXT), c2timestamp_ms,
			CAST(c4sender_id AS TEXT), COALESCE(c5message,'') FROM messages_content WHERE docid > ? ORDER BY docid`, c.FTS)
		if err == nil {
			for rows.Next() {
				var m srcMsg
				var doc int64
				if rows.Scan(&doc, &m.MID, &m.Thread, &m.TS, &m.Sender, &m.Text) == nil {
					m.Kind, m.Source = "text", "fts"
					out = append(out, m)
					c.FTS = max(c.FTS, doc)
				}
			}
			rows.Close()
		}
	}
	return out, c
}

func readMsys(ms *sql.DB, since int64) ([]srcMsg, int64) {
	rows, err := ms.Query(`SELECT message_id, CAST(thread_key AS TEXT), timestamp_ms, CAST(sender_id AS TEXT),
		COALESCE(text,''), COALESCE(is_admin_message,0), COALESCE(is_unsent,0),
		COALESCE(reply_message_text,''), COALESCE(CAST(reply_to_user_id AS TEXT),''), COALESCE(reply_snippet,''),
		reply_source_id IS NOT NULL
		FROM messages WHERE timestamp_ms > ? ORDER BY timestamp_ms`, since)
	if err != nil {
		return nil, since
	}
	var out []srcMsg
	idx := map[string]int{}
	for rows.Next() {
		var m srcMsg
		var admin, unsent, hasReply bool
		var rt, rto, rs string
		if rows.Scan(&m.MID, &m.Thread, &m.TS, &m.Sender, &m.Text, &admin, &unsent, &rt, &rto, &rs, &hasReply) != nil {
			continue
		}
		m.Source, m.Kind = "msys", "text"
		switch {
		case admin:
			m.Kind = "admin"
		case unsent:
			m.Kind = "unsent"
		}
		if hasReply {
			m.Reply = map[string]string{"text": rt, "to": rto, "snippet": rs}
		}
		idx[m.MID] = len(out)
		out = append(out, m)
		since = max(since, m.TS)
	}
	rows.Close()
	if len(out) == 0 {
		return out, since
	}
	args := make([]any, 0, len(idx))
	for id := range idx {
		args = append(args, id)
	}
	arows, err := ms.Query(`SELECT message_id, CAST(attachment_fbid AS TEXT), COALESCE(attachment_type,0),
		COALESCE(preview_width,0), COALESCE(preview_height,0), COALESCE(title_text,''),
		COALESCE(NULLIF(playable_url_mime_type,''), preview_url_mime_type, '')
		FROM attachments WHERE message_id IN (?`+strings.Repeat(",?", len(args)-1)+`) ORDER BY attachment_index`, args...)
	if err == nil {
		for arows.Next() {
			var mid, id, title, mime string
			var typ, w, h int
			if arows.Scan(&mid, &id, &typ, &w, &h, &title, &mime) == nil {
				i := idx[mid]
				out[i].Atts = append(out[i].Atts, map[string]any{"id": id, "type": typ, "w": w, "h": h, "title": title, "mime": mime})
				if out[i].Kind == "text" && out[i].Text == "" {
					out[i].Kind = "media"
				}
			}
		}
		arows.Close()
	}
	return out, since
}

func readE2EE(ms *sql.DB, since int64) ([]srcMsg, int64) {
	// display_ts_ms is INT64_MIN on system placeholders ("Messages and calls are secured…")
	rows, err := ms.Query(`SELECT m.pk, m.persistent_id, CAST(t.thread_id AS TEXT),
		CASE WHEN m.display_ts_ms > 0 THEN m.display_ts_ms ELSE COALESCE(m.authoritative_ts_ms, 0) END,
		CAST(m.sender_contact_pk AS TEXT), COALESCE(m.text,''),
		COALESCE(m.reply_message_text,''), COALESCE(CAST(m.reply_sender_contact_pk AS TEXT),''), COALESCE(m.reply_snippet,''),
		m.reply_message_pk IS NOT NULL OR m.reply_message_text IS NOT NULL
		FROM client_messages m JOIN advanced_crypto_transport_threads t ON t.client_thread_pk = m.thread_pk
		WHERE m.is_hidden = 0 AND m.pk > ? ORDER BY m.pk`, since)
	if err != nil {
		return nil, since
	}
	var out []srcMsg
	idx := map[int64]int{}
	for rows.Next() {
		var m srcMsg
		var pk int64
		var hasReply bool
		var rt, rto, rs string
		if rows.Scan(&pk, &m.MID, &m.Thread, &m.TS, &m.Sender, &m.Text, &rt, &rto, &rs, &hasReply) != nil {
			continue
		}
		since = max(since, pk)
		if m.TS <= 0 {
			continue // no real timestamp: nothing sensible to place in a timeline
		}
		m.Source, m.Kind = "e2ee", "text"
		if hasReply {
			m.Reply = map[string]string{"text": rt, "to": rto, "snippet": rs}
		}
		idx[pk] = len(out)
		out = append(out, m)
		since = max(since, pk)
	}
	rows.Close()
	if len(out) == 0 {
		return out, since
	}
	args := make([]any, 0, len(idx))
	for pk := range idx {
		args = append(args, pk)
	}
	arows, err := ms.Query(`SELECT a.message_pk, a.pk, a.attachment_type, COALESCE(a.width,0), COALESCE(a.height,0),
		COALESCE(a.mime_type,''), COALESCE(NULLIF(x.title_text,''), x.header_title, '')
		FROM client_attachments a LEFT JOIN client_xmas x ON x.attachment_pk = a.pk
		WHERE a.message_pk IN (?`+strings.Repeat(",?", len(args)-1)+`) ORDER BY a.attachment_index`, args...)
	if err == nil {
		for arows.Next() {
			var mpk, apk int64
			var typ, w, h int
			var mime, title string
			if arows.Scan(&mpk, &apk, &typ, &w, &h, &mime, &title) == nil {
				i := idx[mpk]
				out[i].Atts = append(out[i].Atts, map[string]any{"id": "e" + itoa(apk), "type": typ, "w": w, "h": h, "title": title, "mime": mime})
				if out[i].Text == "" {
					out[i].Kind = "media"
				}
			}
		}
		arows.Close()
	}
	return out, since
}
