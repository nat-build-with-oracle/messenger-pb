// `messenger serve` — the whole live archive as one on-device process (root):
// history from the fts.db shadow table, names from msys, realtime from
// `dumpsys notification` + fts rowid deltas, UI = embedded React build (web/ → webdist/).
// Mac: adb forward tcp:8721 tcp:8721 && open http://localhost:8721
package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed all:webdist
var webdist embed.FS

type hub struct {
	mu     sync.Mutex
	subs   map[chan string]bool
	status string
}

func (h *hub) emit(event string, data any) {
	b, _ := json.Marshal(map[string]any{"event": event, "data": data})
	frame := string(b)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- frame:
		default: // slow client: drop rather than block the poller
		}
	}
}

func (h *hub) setStatus(s string) {
	h.mu.Lock()
	changed := h.status != s
	h.status = s
	h.mu.Unlock()
	if changed {
		h.emit("status", map[string]any{"status": s})
	}
}

var (
	reRecord = regexp.MustCompile(`NotificationRecord\(0x[0-9a-f]+: pkg=com\.facebook\.orca .*?tag=(\S+)`)
	reWhen   = regexp.MustCompile(`\bwhen=(\d+)`)
	reTitle  = regexp.MustCompile(`android\.title=String \((.*)\)`)
	reConv   = regexp.MustCompile(`android\.conversationTitle=String \((.*)\)`)
	reText   = regexp.MustCompile(`android\.text=String \((.*)\)`)
	reReact  = regexp.MustCompile(`^(.+?) reacted with (.+?) to `)
)

type liveMsg struct {
	Thread string `json:"thread"`
	TS     int64  `json:"ts"`
	Name   string `json:"name"`
	Sender string `json:"sender"`
	Text   string `json:"text"`
	Kind   string `json:"kind,omitempty"`
	Emoji  string `json:"emoji,omitempty"`
}

// pollNotifications parses Messenger's posted notifications. Tags look like
// GROUP:<thread_key> / ONE_TO_ONE:<id>; the key is msys-space, so names[] resolves it.
func pollNotifications(seen map[string]bool) ([]liveMsg, bool) {
	out, err := exec.Command("dumpsys", "notification", "--noredact").Output()
	if err != nil {
		return nil, false
	}
	var msgs []liveMsg
	chunks := strings.Split(string(out), "NotificationRecord(")
	for _, ch := range chunks[1:] {
		m := reRecord.FindStringSubmatch("NotificationRecord(" + ch)
		if m == nil {
			continue
		}
		tag := m[1]
		when := first(reWhen, ch)
		key := tag + ":" + when
		if seen[key] {
			continue
		}
		seen[key] = true
		title, conv, text := first(reTitle, ch), first(reConv, ch), strings.TrimSpace(first(reText, ch))
		sender := title
		if conv != "" && strings.HasPrefix(title, conv+":") {
			sender = strings.TrimSpace(title[len(conv)+1:])
		}
		thread := tag[strings.LastIndex(tag, ":")+1:]
		name := conv
		if n := nameOf(thread); n != thread {
			name = n
		} else if name == "" {
			name = sender
		}
		ts, _ := strconv.ParseInt(when, 10, 64)
		lm := liveMsg{Thread: thread, TS: ts, Name: name, Sender: sender, Text: text}
		if r := reReact.FindStringSubmatch(text); r != nil {
			lm.Kind, lm.Sender, lm.Emoji = "reaction", strings.TrimSpace(r[1]), strings.TrimSpace(r[2])
		}
		msgs = append(msgs, lm)
	}
	return msgs, true
}

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

type apiMsg struct {
	ID     string       `json:"id"`
	Thread string       `json:"thread"`
	TS     int64        `json:"ts"`
	Sender string       `json:"sender"`
	Text   string       `json:"text"`
	Kind   string       `json:"kind,omitempty"` // "admin" (call/system line) | "unsent"
	Reply  *reply       `json:"reply,omitempty"`
	Atts   []attachment `json:"atts,omitempty"`
}

// mergeNewest merges newest-first lists, keeps n, returns oldest-first. Earlier lists
// win ties: the same message seen by fts and client_messages has different ids but
// the same thread+timestamp, so pass the richer source first.
func mergeNewest(n int, lists ...[]apiMsg) []apiMsg {
	var all []apiMsg
	for _, l := range lists {
		all = append(all, l...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].TS > all[j].TS })
	seen := map[string]bool{}
	out := []apiMsg{}
	for _, m := range all {
		at := m.Thread + "|" + strconv.FormatInt(m.TS, 10)
		if seen[m.ID] || seen[at] {
			continue
		}
		seen[m.ID], seen[at] = true, true
		out = append(out, m)
		if len(out) == n {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func queryMsgs(db *sql.DB, q string, args ...any) ([]apiMsg, int64, error) {
	rows, err := db.Query(`SELECT docid, CAST(c0message_id AS TEXT), CAST(c1thread_id AS TEXT), c2timestamp_ms,
		CAST(c4sender_id AS TEXT), COALESCE(c5message,'') FROM messages_content `+q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []apiMsg{}
	var maxID int64
	for rows.Next() {
		var m apiMsg
		var doc int64
		if err := rows.Scan(&doc, &m.ID, &m.Thread, &m.TS, &m.Sender, &m.Text); err != nil {
			return nil, 0, err
		}
		maxID = max(maxID, doc)
		out = append(out, m)
	}
	return out, maxID, rows.Err()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func serve(db *sql.DB, addr string) {
	ms := openMsys() // nil → fts-only, like before
	h := &hub{subs: map[chan string]bool{}, status: "boot"}
	poll := time.Duration(envInt("POLL_MS", 3000)) * time.Millisecond

	go func() {
		var last, msysLast, e2eeLast int64
		_ = db.QueryRow(`SELECT COALESCE(MAX(docid),0) FROM messages_content`).Scan(&last)
		if ms != nil {
			_ = ms.QueryRow(`SELECT COALESCE(MAX(timestamp_ms),0) FROM messages`).Scan(&msysLast)
			_ = ms.QueryRow(`SELECT COALESCE(MAX(pk),0) FROM client_messages`).Scan(&e2eeLast)
		}
		go func() { prefetchMedia(ms); prefetchE2EEMedia(ms) }()
		seen := map[string]bool{}
		_, _ = pollNotifications(seen) // prime: don't replay notifications already on screen
		namesAt := time.Now()
		go prefetchAvatars(db)
		for {
			time.Sleep(poll)
			if time.Since(namesAt) > time.Minute {
				loadMsysNames()
				namesAt = time.Now()
				go prefetchAvatars(db) // pick up URLs the app refreshed since last pass
				go func() { prefetchMedia(ms); prefetchE2EEMedia(ms) }()
				h.mu.Lock()
				st := h.status
				h.mu.Unlock()
				h.emit("status", map[string]any{"status": st, "names": namesCopy()})
			}
			live, ok := pollNotifications(seen)
			rows, maxID, err := queryMsgs(db, `WHERE docid > ? ORDER BY docid`, last)
			if !ok || err != nil {
				h.setStatus("offline")
				continue
			}
			h.setStatus("live")
			for _, m := range live {
				h.emit("message", m)
			}
			if len(rows) > 0 {
				last = maxID
				h.emit("messages", rows)
			}
			if ms != nil {
				if fresh, err := msysMsgs(ms, `WHERE timestamp_ms > ? ORDER BY timestamp_ms`, msysLast); err == nil && len(fresh) > 0 {
					msysLast = fresh[len(fresh)-1].TS
					h.emit("messages", fresh)
					go prefetchMedia(ms)
				}
				if fresh, maxPK, err := e2eeMsgs(ms, `AND m.pk > ? ORDER BY m.pk`, e2eeLast); err == nil && len(fresh) > 0 {
					e2eeLast = maxPK
					h.emit("messages", fresh)
				}
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/avatar/{id}", handleAvatar)
	mux.HandleFunc("GET /api/media/{id}", handleMedia(ms))
	mux.HandleFunc("GET /api/names", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, namesCopy()) })
	mux.HandleFunc("GET /api/threads", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(`SELECT CAST(c1thread_id AS TEXT), COUNT(*), MAX(c2timestamp_ms),
			(SELECT COALESCE(c5message,'') FROM messages_content x WHERE x.c1thread_id=m.c1thread_id ORDER BY c2timestamp_ms DESC LIMIT 1)
			FROM messages_content m GROUP BY c1thread_id ORDER BY 3 DESC`)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		byID := map[string]*threadRow{}
		out := []*threadRow{}
		for rows.Next() {
			t := &threadRow{}
			_ = rows.Scan(&t.ID, &t.C, &t.Last, &t.Preview)
			byID[t.ID] = t
			out = append(out, t)
		}
		if ms != nil {
			for _, et := range e2eeThreads(ms) { // E2EE threads share fts ids: bump to the newest activity
				if t, ok := byID[et.ID]; ok {
					if et.Last > t.Last {
						t.Last, t.Preview, t.Sender = et.Last, et.Preview, et.Sender
					}
					continue
				}
				if et.C > 0 {
					byID[et.ID] = &et
					out = append(out, &et)
				}
			}
			for _, mt := range msysThreads(ms) {
				if t, ok := byID[mt.ID]; ok { // same key in both: keep the newer preview
					t.C += mt.C
					if mt.Last > t.Last {
						t.Last, t.Preview, t.Sender = mt.Last, mt.Preview, mt.Sender
					}
					continue
				}
				out = append(out, &mt)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Last > out[j].Last })
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /api/thread/{id}", func(w http.ResponseWriter, r *http.Request) {
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if before == 0 {
			before = 1 << 62
		}
		rows, _, err := queryMsgs(db, `WHERE c1thread_id = ? AND c2timestamp_ms < ? ORDER BY c2timestamp_ms DESC LIMIT 200`,
			r.PathValue("id"), before)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var extra, e2 []apiMsg
		if ms != nil {
			extra, _ = msysMsgs(ms, `WHERE CAST(thread_key AS TEXT) = ? AND timestamp_ms < ? ORDER BY timestamp_ms DESC LIMIT 200`,
				r.PathValue("id"), before)
			e2, _, _ = e2eeMsgs(ms, `AND CAST(t.thread_id AS TEXT) = ? AND m.display_ts_ms < ? ORDER BY m.display_ts_ms DESC LIMIT 200`,
				r.PathValue("id"), before)
		}
		writeJSON(w, mergeNewest(200, e2, extra, rows))
	})
	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, []apiMsg{})
			return
		}
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		rows, _, err := queryMsgs(db, `WHERE c5message LIKE ? ESCAPE '\' ORDER BY c2timestamp_ms DESC LIMIT 100`, "%"+esc+"%")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var extra, e2 []apiMsg
		if ms != nil {
			extra, _ = msysMsgs(ms, `WHERE text LIKE ? ESCAPE '\' ORDER BY timestamp_ms DESC LIMIT 100`, "%"+esc+"%")
			e2, _, _ = e2eeMsgs(ms, `AND m.text LIKE ? ESCAPE '\' ORDER BY m.display_ts_ms DESC LIMIT 100`, "%"+esc+"%")
		}
		res := mergeNewest(100, e2, extra, rows)
		for i, j := 0, len(res)-1; i < j; i, j = i+1, j-1 { // search reads newest-first
			res[i], res[j] = res[j], res[i]
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("GET /api/ws", h.handleWS)
	sub, _ := fs.Sub(webdist, "webdist")
	mux.Handle("/", http.FileServerFS(sub))

	fmt.Fprintf(os.Stderr, "messenger serve → http://%s  (%d names, poll %s)\n", addr, len(namesCopy()), poll)
	fatalf("%v", http.ListenAndServe(addr, mux))
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
