// Seen receipts: msys `participants` holds each person's latest read and delivered time per
// chat, and nothing older. Every pass copies them into `seen`, and when a read time moves
// forward it adds a `seen_log` row, so the history builds up from the day this runs.
package main

import (
	"database/sql"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type srcSeen struct {
	Thread, Contact string
	Read, Delivered int64
}

// readSeen: everyone but the owner; E2EE shell threads mapped to the chat they were folded into.
func readSeen(ms *sql.DB, folded map[string]string) []srcSeen {
	if ms == nil {
		return nil
	}
	rows, err := ms.Query(`SELECT CAST(thread_key AS TEXT), CAST(contact_id AS TEXT),
		COALESCE(read_watermark_timestamp_ms,0), COALESCE(delivered_watermark_timestamp_ms,0)
		FROM participants WHERE contact_id <> ` + ownerID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []srcSeen
	for rows.Next() {
		var s srcSeen
		if rows.Scan(&s.Thread, &s.Contact, &s.Read, &s.Delivered) != nil || s.Read <= 0 {
			continue
		}
		if real, ok := folded[s.Thread]; ok {
			s.Thread = real
		}
		out = append(out, s)
	}
	return out
}

// writeSeen upserts what changed. The first time a (chat, person) pair is seen there is
// nothing to compare with, so it is recorded without a log row.
func (s *store) writeSeen(tx core.App, list []srcSeen) (logged int, err error) {
	if s.seen == nil {
		s.seen = map[string]int64{}
		var rows []struct {
			K  string `db:"k"`
			TS int64  `db:"ts"`
		}
		if err := tx.DB().NewQuery("SELECT thread_key || '|' || contact AS k, CAST(read_ts AS INTEGER) AS ts FROM seen").All(&rows); err != nil {
			return 0, err
		}
		for _, r := range rows {
			s.seen[r.K] = r.TS
		}
	}
	seenCol, err := tx.FindCollectionByNameOrId("seen")
	if err != nil {
		return 0, err
	}
	logCol, err := tx.FindCollectionByNameOrId("seen_log")
	if err != nil {
		return 0, err
	}
	for _, v := range list {
		k := v.Thread + "|" + v.Contact
		prev, known := s.seen[k]
		if known && v.Read <= prev {
			continue
		}
		r, ferr := tx.FindFirstRecordByFilter("seen", "thread_key = {:t} && contact = {:c}", dbx.Params{"t": v.Thread, "c": v.Contact})
		if ferr != nil {
			r = core.NewRecord(seenCol)
			r.Set("thread_key", v.Thread)
			r.Set("contact", v.Contact)
		}
		r.Set("name", s.senderName(v.Contact))
		r.Set("read_ts", v.Read)
		r.Set("delivered_ts", v.Delivered)
		if err := tx.Save(r); err != nil {
			return logged, err
		}
		if known && prev > 0 {
			l := core.NewRecord(logCol)
			l.Set("thread_key", v.Thread)
			l.Set("contact", v.Contact)
			l.Set("name", s.senderName(v.Contact))
			l.Set("read_ts", v.Read)
			l.Set("prev_ts", prev)
			if err := tx.Save(l); err != nil {
				return logged, err
			}
			logged++
		}
		s.seen[k] = v.Read
	}
	return logged, nil
}
