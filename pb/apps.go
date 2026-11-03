// Keeping LINE and Messenger awake. Their databases only change while the apps run, and
// Android puts them to sleep. These routes run on the phone (the server is root there):
//
//	GET  /api/apps/status        is each app running, when did its db last change, newest message
//	POST /api/apps/wake {app}    wake one app WITHOUT opening a chat (an open chat is a read
//	                             receipt to another person): Messenger by its inbox intent, which
//	                             starts it silently; LINE by line://nv/chat, its chat list, then
//	                             Home once it is still showing that list.
//
// The phone's own cron (android-cron/rotate.sh, every 15 min) keeps one of the two apps in front.
// Wake refuses while someone is using the phone (any app but the launcher in front).
package main

import (
	"database/sql"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

type appInfo struct {
	pkg, db, newest string // package, its db, SQL for the newest message time in ms
	wake            []string
}

var apps = map[string]appInfo{
	"messenger": {pkg: "com.facebook.orca", db: phoneMsys, newest: `SELECT MAX(timestamp_ms) FROM messages`,
		wake: []string{"start", "-a", "android.intent.action.VIEW", "-d", "fb-messenger://threads", "com.facebook.orca"}},
	"line": {pkg: "jp.naver.line.android", db: phoneLine, newest: `SELECT MAX(CAST(created_time AS INTEGER)) FROM chat_history`,
		wake: []string{"start", "-a", "android.intent.action.VIEW", "-d", "line://nv/chat", "jp.naver.line.android"}},
}

var (
	reFocus = regexp.MustCompile(`mCurrentFocus=Window\{\S+ \S+ ([^/}\s]+)/?([^}\s]*)`)
	wakeMu  sync.Mutex
)

func sh(args ...string) string {
	out, _ := exec.Command(args[0], args[1:]...).CombinedOutput()
	return string(out)
}

// foreground: package and activity in front, "" when the screen is off.
func foreground() (pkg, activity string) {
	if !strings.Contains(sh("/system/bin/dumpsys", "power"), "mWakefulness=Awake") {
		return "", ""
	}
	if m := reFocus.FindStringSubmatch(sh("/system/bin/dumpsys", "window")); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

func appStatus(name string) map[string]any {
	a := apps[name]
	pid := strings.TrimSpace(sh("/system/bin/pidof", a.pkg))
	out := map[string]any{"app": name, "running": pid != "", "pid": pid}
	if st, err := os.Stat(a.db); err == nil {
		out["db_changed"] = st.ModTime().UnixMilli()
	}
	if db, err := openRO(a.db); err == nil {
		var ts sql.NullInt64
		if db.QueryRow(a.newest).Scan(&ts) == nil && ts.Valid {
			out["newest_message"] = ts.Int64
		}
		db.Close()
	}
	return out
}

func registerApps(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/api/apps/status", func(e *core.RequestEvent) error {
			if !canRead(sqRole(e)) {
				return e.ForbiddenError("sign in first", nil)
			}
			fg, _ := foreground()
			return e.JSON(http.StatusOK, map[string]any{"foreground": fg, "apps": []any{appStatus("messenger"), appStatus("line")}})
		})

		se.Router.POST("/api/apps/wake", func(e *core.RequestEvent) error {
			if r := sqRole(e); r != "agent" && r != "reviewer" {
				return e.ForbiddenError("sign in as agent or reviewer", nil)
			}
			var b struct{ App string }
			_ = e.BindBody(&b)
			if _, ok := apps[b.App]; !ok {
				return e.BadRequestError(`app must be "messenger" or "line"`, nil)
			}
			out, status := wakeApp(b.App)
			return e.JSON(status, out)
		})
		return se.Next()
	})
}

// wakeApp wakes one app without opening a chat. It refuses while someone is using the phone.
func wakeApp(name string) (map[string]any, int) {
	a := apps[name]
	if !wakeMu.TryLock() {
		return map[string]any{"error": "a wake is already running"}, http.StatusConflict
	}
	defer wakeMu.Unlock()
	if fg, _ := foreground(); fg != "" && fg != "com.sec.android.app.launcher" && fg != a.pkg {
		return map[string]any{"error": "the phone is in use (" + fg + " in front) — not interrupting", "foreground": fg}, http.StatusConflict
	}
	before := appStatus(name)
	out := sh(append([]string{"/system/bin/am"}, a.wake...)...)
	note := "started by intent"
	if name == "line" { // LINE comes to the front on its chat list; go Home only if it is still there
		time.Sleep(8 * time.Second)
		if fg, act := foreground(); fg == a.pkg && strings.Contains(act, "main.MainActivity") {
			sh("/system/bin/input", "keyevent", "KEYCODE_HOME")
			note = "opened its chat list, then Home"
		} else {
			note = "opened; left the screen alone (" + fg + " " + act + " in front)"
		}
	}
	return map[string]any{"app": name, "before": before, "after": appStatus(name),
		"note": note, "am": strings.TrimSpace(out)}, http.StatusOK
}
