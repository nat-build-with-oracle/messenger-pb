package main

// The phone's cron log (pb/android-cron/rotate.sh) for the Logs tab: one record per run, what it
// tried and whether it worked. Without this the log only said what the job tried, and 155 runs in
// a row that never brought an app to the front (2026-10-28/29, the notification shade on top)
// looked like work being done.

import (
	"bufio"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

const defaultCronLog = "/data/adb/cron/rotate.log"

// Lines written before the key=value format carry no offset; the phone runs on Bangkok time.
var legacyZone = time.FixedZone("+0700", 7*3600)

type cronRun struct {
	At     string `json:"at"`     // RFC 3339
	App    string `json:"app"`    // line | messenger | - (skipped before choosing)
	Ok     string `json:"ok"`     // yes | no | skip | note
	Before string `json:"before"` // window in front before the run
	After  string `json:"after"`  // window in front after it
	Front  string `json:"front"`  // app in front after it (the result is judged by this)
	VPN    string `json:"vpn"`    // up | down | "" (not recorded)
	PB     string `json:"pb"`     // up | down | ""
	Note   string `json:"note"`
	Raw    string `json:"raw"`
}

func registerCronLog(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/phone/cronlog", func(e *core.RequestEvent) error {
			if r := sqRole(e); r != "reviewer" && r != "agent" {
				return e.ForbiddenError("sign in as agent or reviewer", nil)
			}
			path := os.Getenv("CRON_LOG")
			if path == "" {
				path = defaultCronLog
			}
			limit, _ := strconv.Atoi(e.Request.URL.Query().Get("limit"))
			if limit <= 0 || limit > 500 {
				limit = 300
			}
			runs, err := readCronLog(path, limit)
			if os.IsNotExist(err) {
				return e.JSON(http.StatusOK, map[string]any{"available": false, "path": path, "runs": []cronRun{}})
			}
			if err != nil {
				return e.InternalServerError("cron log unreadable", err)
			}
			return e.JSON(http.StatusOK, map[string]any{"available": true, "path": path, "runs": runs})
		})
		return se.Next()
	})
}

// readCronLog returns the newest `limit` runs, newest first.
func readCronLog(path string, limit int) ([]cronRun, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []cronRun
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := parseCronLine(sc.Text()); ok {
			all = append(all, r)
		}
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, sc.Err()
}

func parseCronLine(line string) (cronRun, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 20 {
		return cronRun{}, false
	}
	r := cronRun{Raw: line}
	rest := ""
	// New format: "2006-01-02 15:04:05 -0700 key=value ..."
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", safeSlice(line, 25)); err == nil && strings.Contains(line, "ok=") {
		r.At = t.Format(time.RFC3339)
		for _, kv := range strings.Fields(line[25:]) {
			k, v, _ := strings.Cut(kv, "=")
			v = strings.ReplaceAll(v, "_", " ")
			switch k {
			case "app":
				r.App = v
			case "ok":
				r.Ok = v
			case "before":
				r.Before = v
			case "after":
				r.After = v
			case "front":
				r.Front = v
			case "vpn":
				r.VPN = v
			case "pb":
				r.PB = v
			case "note":
				r.Note = strings.ReplaceAll(v, "-", " ")
			}
		}
		return r, r.Ok != ""
	}
	// Legacy format: "2006-01-02 15:04:05 line: <focus>" and friends; the result is inferred.
	t, err := time.ParseInLocation("2006-01-02 15:04:05", line[:19], legacyZone)
	if err != nil {
		return cronRun{}, false
	}
	r.At = t.Format(time.RFC3339)
	rest = strings.TrimSpace(line[19:])
	switch {
	case strings.HasPrefix(rest, "skip: phone in use"):
		r.App, r.Ok, r.Note = "-", "skip", "phone in use"
		r.Before = shortFocus(strings.Trim(strings.TrimPrefix(rest, "skip: phone in use"), " ()"))
	case rest == "line: was on a chat, pressed Back":
		r.App, r.Ok, r.Note = "line", "note", "was on a chat, pressed Back"
	case strings.HasPrefix(rest, "line: "):
		r.App, r.After = "line", strings.TrimPrefix(rest, "line: ")
		r.Ok = okIf(strings.HasPrefix(r.After, "jp.naver.line.android"))
	case strings.HasPrefix(rest, "messenger: "):
		r.App, r.After = "messenger", strings.TrimPrefix(rest, "messenger: ")
		r.Ok = okIf(strings.HasPrefix(r.After, "com.facebook.orca") || strings.HasPrefix(r.After, "inbox"))
	default:
		return cronRun{}, false
	}
	return r, true
}

func okIf(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func safeSlice(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

// shortFocus turns "mCurrentFocus=Window{1a2b u0 pkg/Activity}" into "pkg/Activity", as rotate.sh does.
func shortFocus(f string) string {
	if i := strings.Index(f, " u0 "); i >= 0 {
		f = f[i+4:]
	}
	return strings.TrimSuffix(strings.SplitN(f, "}", 2)[0], " ")
}
