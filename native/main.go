// messenger — stateless on-device CLI over the live Messenger fts.db (root).
// Same UX as the Mac cli.ts: latest/threads/thread/search, names, colors.
// Reads the FTS4 shadow table directly (plain SQL; modernc sqlite has no FTS3/4 module).
// Build (host test):  MESSENGER_DB=/tmp/fts.db go build -o messenger .
// Build (android):    CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags=-s -o messenger-android .
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	uid       = "896050346"
	deviceDB  = "/data/data/com.facebook.orca/files/bankAndEcho/echo/AdvancedCrypto/" + uid + "/fts.db"
	namesPath = "/data/local/tmp/messenger-names.json"
	fallback  = "/data/local/tmp/fts-snapshot.db"
	msysDB    = "/data/data/com.facebook.orca/databases/msys_database_" + uid
)

var colors = os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == ""

func c(code, s string) string {
	if !colors {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

var (
	names   = map[string]string{}
	pics    = map[string]string{} // id -> signed fbcdn picture URL (expires; msys refreshes it)
	namesMu sync.RWMutex          // serve: msys refresh writes while handlers read
)

func picOf(id string) string {
	namesMu.RLock()
	defer namesMu.RUnlock()
	return pics[id]
}

func namesCopy() map[string]string {
	namesMu.RLock()
	defer namesMu.RUnlock()
	out := make(map[string]string, len(names))
	for k, v := range names {
		out[k] = v
	}
	return out
}

type msg struct {
	TS     int64
	Thread string
	Sender string
	Text   string
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func openDB() *sql.DB {
	path := os.Getenv("MESSENGER_DB")
	if path == "" {
		path = deviceDB
	}
	if _, err := os.Stat(path); err != nil {
		path = fallback
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		fatalf("%s", "open: "+err.Error())
	}
	if err := db.Ping(); err != nil {
		fatalf("%s", "cannot open "+path+": "+err.Error())
	}
	return db
}

func loadNames() {
	b, err := os.ReadFile(namesPath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, &names)
}

// loadMsysNames layers real names (and picture URLs) from the app's msys db over
// the notification names: contacts (id -> person) first, then threads (groups) so
// a group title/picture wins over any person sharing the key.
func loadMsysNames() {
	path := os.Getenv("MESSENGER_MSYS")
	if path == "" {
		path = msysDB
	}
	if _, err := os.Stat(path); err != nil {
		return
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return
	}
	defer db.Close()
	for _, q := range []string{
		`SELECT CAST(id AS TEXT), COALESCE(name,''), COALESCE(profile_picture_url,'') FROM contacts`,
		`SELECT CAST(thread_key AS TEXT), COALESCE(thread_name,''), COALESCE(thread_picture_url,'') FROM threads`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id, n, pic string
			if rows.Scan(&id, &n, &pic) == nil {
				namesMu.Lock()
				if n != "" {
					names[id] = n
				}
				if pic != "" {
					pics[id] = pic
				}
				namesMu.Unlock()
			}
		}
		rows.Close()
	}
}

func nameOf(id string) string {
	namesMu.RLock()
	defer namesMu.RUnlock()
	if n, ok := names[id]; ok {
		return n
	}
	return id
}

func hhmm(ts int64) string  { return time.Unix(ts/1000, 0).Format("15:04") }
func dayOf(ts int64) string { return time.Unix(ts/1000, 0).Format("Mon, Jan 2 2006") }

func pad(s string, n int) string {
	r := []rune(s)
	if len(r) >= n {
		r = r[:n-1]
	}
	return string(r) + strings.Repeat(" ", n-len(r))
}

func show(rows []msg) {
	last := ""
	for _, r := range rows {
		d := dayOf(r.TS)
		if d != last {
			pad := strings.Repeat("─", maxInt(0, 56-len(d)))
			fmt.Println(c("33", "── "+d+" "+pad))
			last = d
		}
		who, code := nameOf(r.Sender), "36"
		if r.Sender == uid {
			who, code = "you", "94"
		}
		text := strings.Join(strings.Fields(r.Text), " ")
		if r := []rune(text); len(r) > 90 {
			text = string(r[:90])
		}
		fmt.Printf("%s %s %s   %s\n", c("2", hhmm(r.TS)), c(code, pad(who, 18)), text, c("2", "["+nameOf(r.Thread)+"]"))
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func query(db *sql.DB, sql string, args ...any) []msg {
	rows, err := db.Query(sql, args...)
	if err != nil {
		fatalf("query: %v", err)
	}
	defer rows.Close()
	var all []msg
	for rows.Next() {
		var m msg
		if err := rows.Scan(&m.TS, &m.Thread, &m.Sender, &m.Text); err != nil {
			fatalf("scan: %v", err)
		}
		all = append(all, m)
	}
	return all
}

const cols = `c2timestamp_ms, c1thread_id, c4sender_id, c5message FROM messages_content`

func main() {
	args := os.Args[1:]
	cmd := "latest"
	if len(args) > 0 {
		cmd = args[0]
	}
	loadNames()
	loadMsysNames()
	db := openDB()
	defer db.Close()

	switch cmd {
	case "notifs":
		// one-shot: print currently-posted Messenger notifications (root: dumpsys direct)
		seen := map[string]bool{}
		msgs, ok := pollNotifications(seen)
		if !ok {
			fatalf("cannot read notifications (need root)")
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].TS < msgs[j].TS })
		for _, m := range msgs {
			who := m.Sender
			if m.Kind == "reaction" {
				who = m.Sender + " " + m.Emoji
			}
			fmt.Printf("%s %s %s   %s\n", c("2", hhmm(m.TS)), c("36", pad(nameOf(m.Thread), 18)), pad(who, 20), m.Text)
		}
		if len(msgs) == 0 {
			fmt.Println("(no Messenger notifications currently posted)")
		}

	case "serve":
		addr := "127.0.0.1:8721"
		if len(args) > 1 {
			addr = args[1]
		}
		serve(db, addr)

	case "threads":
		rows, err := db.Query(`SELECT c1thread_id, COUNT(*), MAX(c2timestamp_ms),
			(SELECT c5message FROM messages_content x WHERE x.c1thread_id=m.c1thread_id ORDER BY c2timestamp_ms DESC LIMIT 1)
			FROM messages_content m GROUP BY c1thread_id ORDER BY 3 DESC`)
		if err != nil {
			fatalf("query: %v", err)
		}
		type row struct {
			id   string
			n    int
			last int64
			prev string
		}
		var out []row
		for rows.Next() {
			var r row
			_ = rows.Scan(&r.id, &r.n, &r.last, &r.prev)
			out = append(out, r)
		}
		rows.Close()
		for _, r := range out {
			prev := strings.Join(strings.Fields(r.prev), " ")
			if r := []rune(prev); len(r) > 46 {
				prev = string(r[:46])
			}
			fmt.Printf("%s %5d msgs  %s  %s\n", c("1", pad(nameOf(r.id), 28)), r.n,
				time.Unix(r.last/1000, 0).Format("01/02/2006"), prev)
		}

	case "thread":
		if len(args) < 2 {
			fatalf("usage: messenger thread <id|name> [N]")
		}
		want, n := args[1], 40
		if len(args) > 2 {
			fmt.Sscan(args[2], &n)
		}
		ids, err := db.Query("SELECT DISTINCT c1thread_id FROM messages_content")
		if err != nil {
			fatalf("query: %v", err)
		}
		var all []string
		for ids.Next() {
			var id string
			_ = ids.Scan(&id)
			all = append(all, id)
		}
		ids.Close()
		hit := ""
		for _, id := range all {
			if id == want {
				hit = id
				break
			}
		}
		if hit == "" {
			for _, id := range all {
				if strings.Contains(strings.ToLower(nameOf(id)), strings.ToLower(want)) {
					hit = id
					break
				}
			}
		}
		if hit == "" {
			fatalf("no thread matches '%s'", want)
		}
		fmt.Println(c("1", "== "+nameOf(hit)+" ("+hit+") =="))
		rows := query(db, `SELECT `+cols+` WHERE c1thread_id=? ORDER BY c2timestamp_ms DESC LIMIT ?`, hit, n)
		sort.Slice(rows, func(i, j int) bool { return rows[i].TS < rows[j].TS })
		show(rows)

	case "search":
		if len(args) < 2 {
			fatalf("usage: messenger search <query>")
		}
		clean := regexp.MustCompile(`[%_"\\^*]`).ReplaceAllString(strings.Join(args[1:], " "), " ")
		clean = strings.TrimSpace(clean)
		if clean == "" {
			fatalf("empty search")
		}
		rows := query(db, `SELECT `+cols+` WHERE c5message LIKE ? ESCAPE '\' ORDER BY c2timestamp_ms DESC LIMIT 50`,
			"%"+strings.ReplaceAll(clean, `\`, `\\`)+"%")
		fmt.Println(c("1", "== search '"+clean+"' ("+fmt.Sprint(len(rows))+") =="))
		show(rows)

	default:
		n := 30
		if len(args) > 1 {
			fmt.Sscan(args[1], &n)
		} else if cmd != "latest" {
			fmt.Sscan(cmd, &n)
		}
		rows := query(db, `SELECT `+cols+` ORDER BY c2timestamp_ms DESC LIMIT ?`, n)
		sort.Slice(rows, func(i, j int) bool { return rows[i].TS < rows[j].TS })
		fmt.Println(c("1", "== latest "+fmt.Sprint(len(rows))+" =="))
		show(rows)
	}
}
