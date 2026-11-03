// Writing into PocketBase: idempotent upserts keyed by fbid / thread key / mid,
// plus thread+timestamp dedupe (fts and client_messages hold the same E2EE
// message under different ids). Used by `import` (bulk, hooks muted) and by
// the sync loop inside `serve` (live, hooks publish to MQTT).
package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type store struct {
	app      core.App
	contacts map[string]string // fbid -> record id
	threads  map[string]string // thread key -> record id
	mids     map[string]bool
	slots    map[string]bool // thread_key|ts
	names    map[string]string
	seen     map[string]int64 // thread|contact -> read_ts, loaded on first use
}

func loadStore(app core.App) (*store, error) {
	s := &store{app: app, contacts: map[string]string{}, threads: map[string]string{},
		mids: map[string]bool{}, slots: map[string]bool{}, names: map[string]string{}}
	var rows []struct {
		ID  string `db:"id"`
		Key string `db:"k"`
		TS  int64  `db:"ts"`
		TK  string `db:"tk"`
	}
	if err := app.DB().NewQuery("SELECT id, fbid AS k, 0 AS ts, '' AS tk FROM contacts").All(&rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.contacts[r.Key] = r.ID
	}
	rows = rows[:0]
	if err := app.DB().NewQuery("SELECT id, key AS k, 0 AS ts, '' AS tk FROM threads").All(&rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.threads[r.Key] = r.ID
	}
	rows = rows[:0]
	if err := app.DB().NewQuery("SELECT id, mid AS k, CAST(ts AS INTEGER) AS ts, thread_key AS tk FROM messages").All(&rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.mids[r.Key] = true
		s.slots[r.TK+"|"+itoa(r.TS)] = true
	}
	return s, nil
}

func (s *store) upsertContacts(tx core.App, names, pics map[string]string) (n int, err error) {
	col, err := tx.FindCollectionByNameOrId("contacts")
	if err != nil {
		return 0, err
	}
	for id, name := range names {
		s.names[id] = name
		if _, ok := s.contacts[id]; ok {
			continue // names rarely change; keep import cheap
		}
		r := core.NewRecord(col)
		r.Set("fbid", id)
		r.Set("name", name)
		r.Set("picture", pics[id])
		if err := tx.Save(r); err != nil {
			return n, fmt.Errorf("contact %s: %w", id, err)
		}
		s.contacts[id] = r.Id
		n++
	}
	return n, nil
}

func (s *store) upsertThreads(tx core.App, list []srcThread) (n int, err error) {
	col, err := tx.FindCollectionByNameOrId("threads")
	if err != nil {
		return 0, err
	}
	for _, t := range list {
		var r *core.Record
		if id, ok := s.threads[t.Key]; ok {
			if r, err = tx.FindRecordById(col, id); err != nil {
				return n, err
			}
			if r.GetInt("last_ts") >= int(t.Last) && r.GetString("name") == t.Name &&
				(t.Preview == "" || r.GetString("preview") == t.Preview) &&
				(t.Waiting == nil || r.GetBool("waiting") == *t.Waiting) &&
				(t.Folder == "" || r.GetString("folder") == t.Folder) {
				continue
			}
		} else {
			r = core.NewRecord(col)
			r.Set("key", t.Key)
			n++
		}
		r.Set("name", t.Name)
		r.Set("is_group", t.Group)
		r.Set("e2ee", t.E2EE)
		r.Set("last_ts", t.Last)
		if t.Waiting != nil {
			r.Set("waiting", *t.Waiting)
		}
		if t.Folder != "" {
			r.Set("folder", t.Folder)
		}
		if t.Preview != "" { // E2EE chats have no snippet; keep the one fillPreviews wrote earlier
			r.Set("preview", t.Preview)
		}
		if err := tx.Save(r); err != nil {
			return n, fmt.Errorf("thread %s: %w", t.Key, err)
		}
		s.threads[t.Key] = r.Id
	}
	return n, nil
}

func (s *store) threadID(tx core.App, key string) (string, error) {
	if id, ok := s.threads[key]; ok {
		return id, nil
	}
	if _, err := s.upsertThreads(tx, []srcThread{{Key: key, Name: s.names[key]}}); err != nil {
		return "", err
	}
	return s.threads[key], nil
}

func (s *store) insertMessages(tx core.App, msgs []srcMsg) (n int, err error) {
	col, err := tx.FindCollectionByNameOrId("messages")
	if err != nil {
		return 0, err
	}
	for _, m := range msgs {
		slot := m.Thread + "|" + itoa(m.TS)
		if m.MID == "" || s.mids[m.MID] || s.slots[slot] {
			continue
		}
		tid, err := s.threadID(tx, m.Thread)
		if err != nil {
			return n, err
		}
		r := core.NewRecord(col)
		r.Set("mid", m.MID)
		r.Set("thread", tid)
		r.Set("thread_key", m.Thread)
		r.Set("ts", m.TS)
		r.Set("sender", m.Sender)
		r.Set("sender_name", s.senderName(m.Sender))
		r.Set("text", m.Text)
		r.Set("kind", m.Kind)
		r.Set("source", m.Source)
		if m.Reply != nil {
			r.Set("reply", m.Reply)
		}
		if len(m.Atts) > 0 {
			r.Set("atts", m.Atts)
		}
		if err := tx.Save(r); err != nil {
			return n, fmt.Errorf("message %s: %w", m.MID, err)
		}
		s.mids[m.MID], s.slots[slot] = true, true
		n++
	}
	return n, nil
}

func (s *store) senderName(id string) string {
	if id == ownerID {
		return "You"
	}
	return s.names[id]
}

// fillPreviews gives threads without a snippet (E2EE chats) the text of their newest message
// in this batch, written the way the chat list shows it ("You: …", "Name: …" in groups).
func (s *store) fillPreviews(threads []srcThread, msgs []srcMsg) {
	newest := map[string]srcMsg{}
	for _, m := range msgs {
		if m.Kind != "admin" && m.TS >= newest[m.Thread].TS {
			newest[m.Thread] = m
		}
	}
	for i, t := range threads {
		m, ok := newest[t.Key]
		if ok { // the newest real message in this batch decides "not answered yet"
			w := m.Sender != ownerID
			threads[i].Waiting = &w
		}
		if t.Preview != "" || !ok || m.TS < t.Last-60_000 {
			continue
		}
		text := m.Text
		if text == "" && len(m.Atts) > 0 {
			text = "sent an attachment."
		}
		switch {
		case m.Sender == ownerID:
			text = "You: " + text
		case t.Group:
			if n := strings.Fields(s.senderName(m.Sender)); len(n) > 0 {
				text = n[0] + ": " + text
			}
		}
		threads[i].Preview = text
	}
}

// dropThreads deletes records of threads folded into another one, if nothing was filed under them.
func (s *store) dropThreads(tx core.App, keys map[string]string) error {
	for k := range keys {
		id, ok := s.threads[k]
		if !ok {
			continue
		}
		if n, _ := tx.CountRecords("messages", dbx.HashExp{"thread_key": k}); n > 0 {
			continue
		}
		r, err := tx.FindRecordById("threads", id)
		if err == nil {
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		delete(s.threads, k)
	}
	return nil
}

// pass reads sources past the cursors and writes everything new in one transaction.
func (s *store) pass(ftsPath, msysPath string, c cursors) (cursors, [3]int, error) {
	var counts [3]int
	fts, _ := openRO(ftsPath)
	ms, _ := openRO(msysPath)
	if fts != nil {
		defer fts.Close()
	}
	if ms != nil {
		defer ms.Close()
	}
	if fts == nil && ms == nil {
		return c, counts, fmt.Errorf("no source db (fts=%q msys=%q)", ftsPath, msysPath)
	}
	var names, pics map[string]string
	if ms != nil {
		names, pics = readContacts(ms)
	}
	threads, folded := readThreads(ms, fts, names)
	msgs, next := readMessages(ms, fts, c)
	s.fillPreviews(threads, msgs)
	seen := readSeen(ms, folded)
	err := s.app.RunInTransaction(func(tx core.App) error {
		var err error
		if counts[0], err = s.upsertContacts(tx, names, pics); err != nil {
			return err
		}
		if err = s.dropThreads(tx, folded); err != nil {
			return err
		}
		if _, err = s.writeSeen(tx, seen); err != nil {
			return err
		}
		if counts[1], err = s.upsertThreads(tx, threads); err != nil {
			return err
		}
		counts[2], err = s.insertMessages(tx, msgs)
		return err
	})
	if err != nil {
		return c, counts, err
	}
	return next, counts, nil
}

func registerImport(app *pocketbase.PocketBase) {
	var ftsFlag, msysFlag string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Bulk-load the Messenger archive (fts.db + msys db) — MQTT hooks muted",
		RunE: func(cmd *cobra.Command, args []string) error {
			fts, ms := pick(ftsFlag, "FTS_DB", phoneFTS), pick(msysFlag, "MSYS_DB", phoneMsys)
			s, err := loadStore(app)
			if err != nil {
				return err
			}
			muted.Store(true)
			t0 := time.Now()
			_, n, err := s.pass(fts, ms, cursors{})
			if err != nil {
				return err
			}
			log.Printf("import: +%d contacts, +%d threads, +%d messages in %s (fts=%s msys=%s)",
				n[0], n[1], n[2], time.Since(t0).Round(time.Millisecond), fts, ms)
			return nil
		},
	}
	cmd.Flags().StringVar(&ftsFlag, "fts", "", "fts.db path (default $FTS_DB or the phone's live db)")
	cmd.Flags().StringVar(&msysFlag, "msys", "", "msys_database path (default $MSYS_DB or the phone's live db)")
	app.RootCmd.AddCommand(cmd)
}

// registerSync: while serving, poll the source dbs and create records for new
// messages — those creates fire the MQTT hooks. First pass catches up muted.
func registerSync(app core.App) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		fts, ms := pick("", "FTS_DB", phoneFTS), pick("", "MSYS_DB", phoneMsys)
		if fts == "" && ms == "" || envOr("SYNC", "1") == "0" {
			log.Println("sync: off (no source db)")
			return e.Next()
		}
		every, _ := time.ParseDuration(envOr("SYNC_EVERY", "3s"))
		go func() {
			s, err := loadStore(app)
			if err != nil {
				log.Println("sync:", err)
				return
			}
			muted.Store(true)
			c, n, err := s.pass(fts, ms, cursors{})
			muted.Store(false)
			log.Printf("sync: catch-up +%d contacts +%d threads +%d messages (err=%v); live every %s", n[0], n[1], n[2], err, every)
			publish("status", map[string]any{"event": "sync-ready", "messages": len(s.mids)})
			for range time.Tick(every) {
				if c, n, err = s.pass(fts, ms, c); err != nil {
					log.Println("sync:", err)
				} else if n[2] > 0 {
					log.Printf("sync: +%d messages", n[2])
				}
			}
		}()
		return e.Next()
	})
}
