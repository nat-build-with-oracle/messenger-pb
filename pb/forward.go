// Forwarding: every new message in a chat with an enabled rule (collection `forwards`) is
// posted to that rule's Discord webhook. The ledger (`forward_log`) gets its row BEFORE the
// post, under a unique hash of (rule, message id): a message the ledger has seen is never
// posted again, even after a restart or a re-sync — a crash mid-post loses one post rather
// than risking a duplicate. Mentions are disabled, so a forwarded "@everyone" pings nobody.
//
//	messenger-pb forward add --name "My Shop" --thread 1234567890123456   (webhook from $DISCORD_WEBHOOK)
//	messenger-pb forward add --name "Chat" --thread 2345678901234567 --daily   (a Discord forum: one post per day)
//	messenger-pb forward list | off <id> | on <id>
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/spf13/cobra"
)

var discordHTTP = &http.Client{Timeout: 15 * time.Second}

// Android ships no zoneinfo where Go looks, so Bangkok is a fixed UTC+7 (no DST there).
var bangkok = time.FixedZone("ICT", 7*3600)

// webhookOK: https anywhere, or plain http only to a tailnet address (100.64.0.0/10, *.ts.net).
func webhookOK(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("not a URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	h := u.Hostname()
	if ip := net.ParseIP(h); u.Scheme == "http" && (strings.HasSuffix(h, ".ts.net") || (ip != nil && tailnet.Contains(ip))) {
		return nil
	}
	return fmt.Errorf("webhook must be https (plain http only to a tailnet address)")
}

var _, tailnet, _ = net.ParseCIDR("100.64.0.0/10")

func registerForward(app *pocketbase.PocketBase) {
	// An agent may propose a rule; it is created DISABLED. Only a superuser enables it (⚙ in
	// the page or PocketBase admin), because a webhook carries private chats off the tailnet.
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.POST("/api/forwards/propose", func(e *core.RequestEvent) error {
			if r := sqRole(e); r != "agent" && r != "reviewer" {
				return e.ForbiddenError("sign in as agent or reviewer", nil)
			}
			var b struct {
				Name, ThreadKey, Webhook, Format string
			}
			if err := e.BindBody(&b); err != nil {
				return e.BadRequestError("bad body", err)
			}
			if b.Format == "" {
				b.Format = "discord"
			}
			if strings.TrimSpace(b.Name) == "" || strings.TrimSpace(b.ThreadKey) == "" {
				return e.BadRequestError("name and threadKey are required", nil)
			}
			if err := webhookOK(b.Webhook); err != nil {
				return e.BadRequestError(err.Error(), nil)
			}
			col, err := e.App.FindCollectionByNameOrId("forwards")
			if err != nil {
				return err
			}
			r := core.NewRecord(col)
			for k, v := range map[string]any{"name": b.Name, "platform": "Messenger", "thread_key": b.ThreadKey,
				"webhook": b.Webhook, "format": b.Format, "enabled": false, "proposed_by": actorOf(e)} {
				r.Set(k, v)
			}
			if err := e.App.Save(r); err != nil {
				return e.BadRequestError("could not save the proposal", err)
			}
			return e.JSON(http.StatusOK, map[string]any{"id": r.Id, "enabled": false,
				"note": "proposed, NOT enabled — a superuser turns it on in the chat's ⚙ settings"})
		})
		return se.Next()
	})

	for _, ev := range []*hook.TaggedHook[*core.RecordRequestEvent]{app.OnRecordCreateRequest("forwards"), app.OnRecordUpdateRequest("forwards")} {
		ev.BindFunc(func(e *core.RecordRequestEvent) error {
			if err := webhookOK(e.Record.GetString("webhook")); err != nil {
				return e.BadRequestError(err.Error(), nil)
			}
			return e.Next()
		})
	}
	app.OnRecordAfterCreateSuccess("messages").BindFunc(func(e *core.RecordEvent) error {
		if !muted.Load() { // bulk import and the sync's first catch-up are history, not news
			forwardMessage(e.App, e.Record)
		}
		return e.Next()
	})
	registerForwardCommands(app)
}

func forwardMessage(app core.App, msg *core.Record) {
	rules, err := app.FindRecordsByFilter("forwards", "enabled = true && thread_key = {:k}", "", 0, 0,
		dbx.Params{"k": msg.GetString("thread_key")})
	if err != nil || len(rules) == 0 {
		return
	}
	ledger, err := app.FindCollectionByNameOrId("forward_log")
	if err != nil {
		return
	}
	for _, rule := range rules {
		// Only what arrives after the rule was made: turning a rule on is not a backfill.
		if msg.GetInt("ts") < int(rule.GetDateTime("created").Time().UnixMilli()) {
			continue
		}
		sum := sha256.Sum256([]byte(rule.Id + "|" + msg.GetString("mid")))
		row := core.NewRecord(ledger)
		row.Set("forward", rule.Id)
		row.Set("mid", msg.GetString("mid"))
		row.Set("hash", hex.EncodeToString(sum[:]))
		row.Set("status", "sending")
		if err := app.Save(row); err != nil {
			continue // unique hash: already posted (or being posted) — never again
		}
		go postToDiscord(app, rule, msg, row)
	}
}

func postToDiscord(app core.App, rule, msg, row *core.Record) {
	code, errText := postOne(app, rule, msg, row.GetString("hash"))
	finishRows(app, code, errText, row)
}

// finishRows records one post's HTTP result on every ledger row it carried.
func finishRows(app core.App, code int, errText string, rows ...*core.Record) {
	for _, row := range rows {
		row.Set("http", code)
		row.Set("error", trimRunes(errText, 2000))
		row.Set("status", map[bool]string{true: "sent", false: "failed"}[errText == "" && code/100 == 2])
		if err := app.Save(row); err != nil {
			log.Println("forward: ledger:", err)
		}
	}
}

// postOne: one message as one post (json or Discord, with its pictures).
func postOne(app core.App, rule, msg *core.Record, hash string) (int, string) {
	url, body := rule.GetString("webhook"), []byte(nil)
	if rule.GetString("format") == "json" {
		var atts any
		_ = json.Unmarshal([]byte(msg.GetString("atts")), &atts)
		body, _ = json.Marshal(map[string]any{
			"event": "message", "rule": rule.Id, "chat": rule.GetString("name"), "thread_key": msg.GetString("thread_key"),
			"mid": msg.GetString("mid"), "hash": hash, "ts": msg.GetInt("ts"), "sender": msg.GetString("sender"),
			"sender_name": msg.GetString("sender_name"), "me": msg.GetString("sender") == ownerID, "kind": msg.GetString("kind"),
			"text": msg.GetString("text"), "atts": atts,
		})
	}
	ctype := "application/json"
	if rule.GetString("format") != "json" {
		files := discordFiles(msg)
		build := func(threadName string) ([]byte, string) {
			p := map[string]any{
				"username":         trimRunes(rule.GetString("name"), 80),
				"content":          discordText(msg, files, chatName(app, rule, msg)),
				"allowed_mentions": map[string]any{"parse": []string{}},
			}
			if threadName != "" {
				p["thread_name"] = threadName
			}
			payload, _ := json.Marshal(p)
			if len(files) == 0 {
				return payload, "application/json"
			}
			var buf bytes.Buffer // images go up with the post as attachments
			mw := multipart.NewWriter(&buf)
			_ = mw.WriteField("payload_json", string(payload))
			for i, f := range files {
				h := textproto.MIMEHeader{}
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files[%d]"; filename="%s"`, i, f.name))
				h.Set("Content-Type", f.mime)
				w, _ := mw.CreatePart(h)
				_, _ = w.Write(f.data)
			}
			_ = mw.Close()
			return buf.Bytes(), mw.FormDataContentType()
		}
		url = withQuery(url, "wait=true")
		if rule.GetBool("daily") {
			return sendDaily(app, rule, msg.GetInt("ts"), url, build, hash)
		}
		body, ctype = build("")
	}
	code, errText, _ := sendWebhook(rule, url, body, ctype, hash)
	return code, errText
}

func dayKey(ts int) string { k, _ := dayOf(ts); return k }

// dailyMu: one daily post opened at a time, so two messages of a new day cannot open two posts.
var dailyMu sync.Mutex

// dayOf: the Bangkok day of a message, as the forward_days key and as the forum post's title.
func dayOf(ts int) (key, title string) {
	t := time.UnixMilli(int64(ts)).In(bangkok)
	return t.Format("2006-01-02"), t.Format("02/01/2006")
}

// sendDaily posts into the forum post of the message's day: an existing post goes by
// ?thread_id=, and the day's first message opens the post (thread_name) and its id is kept in
// forward_days. A day whose post was deleted in Discord (404) is opened again once.
func sendDaily(app core.App, rule *core.Record, ts int, url string, build func(threadName string) ([]byte, string), hash string) (int, string) {
	key, title := dayOf(ts)
	dailyMu.Lock()
	defer dailyMu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if row, err := app.FindFirstRecordByFilter("forward_days", "forward = {:f} && day = {:d}",
			dbx.Params{"f": rule.Id, "d": key}); err == nil {
			body, ctype := build("")
			code, errText, _ := sendWebhook(rule, withQuery(url, "thread_id="+row.GetString("thread_id")), body, ctype, hash)
			if code == http.StatusNotFound && attempt == 0 {
				_ = app.Delete(row)
				continue
			}
			return code, errText
		}
		body, ctype := build(title)
		code, errText, resp := sendWebhook(rule, url, body, ctype, hash)
		if errText == "" && code/100 == 2 {
			var m struct {
				ChannelID string `json:"channel_id"`
			}
			col, err := app.FindCollectionByNameOrId("forward_days")
			if json.Unmarshal(resp, &m) == nil && m.ChannelID != "" && err == nil {
				r := core.NewRecord(col)
				r.Set("forward", rule.Id)
				r.Set("day", key)
				r.Set("thread_id", m.ChannelID)
				if err := app.Save(r); err != nil {
					log.Println("forward: daily:", err)
				}
			}
		}
		return code, errText
	}
	return 0, "daily: the day's post could not be found or opened"
}

// sendWebhook posts a body, waiting once if Discord answers 429. It returns the reply body too.
func sendWebhook(rule *core.Record, url string, body []byte, ctype, hash string) (int, string, []byte) {
	code, errText, out := 0, "", []byte(nil)
	for attempt := 0; attempt < 2; attempt++ { // one wait-and-retry, only on 429
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", ctype)
		req.Header.Set("X-Forward-Hash", hash) // receivers can dedupe too
		if secret := rule.GetString("secret"); secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			req.Header.Set("X-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := discordHTTP.Do(req)
		if err != nil {
			errText = err.Error()
			break
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		code, out = resp.StatusCode, b
		if code == http.StatusTooManyRequests && attempt == 0 {
			var rl struct {
				RetryAfter float64 `json:"retry_after"`
			}
			_ = json.Unmarshal(b, &rl)
			time.Sleep(time.Duration(min(rl.RetryAfter, 10)*1000) * time.Millisecond)
			continue
		}
		if code/100 != 2 {
			errText = trimRunes(strings.TrimSpace(string(b)), 2000)
		}
		break
	}
	return code, errText, out
}

type discordFile struct {
	id, name, mime string
	data           []byte
}

// discordFiles: the message's photos, GIFs and stickers, from the native server's picture
// cache on the phone (sqmedia.go's NATIVE_URL), which fetches a picture the first time it is
// asked. A new message's picture can take a few seconds to be fetchable: three tries.
func discordFiles(msg *core.Record) []discordFile {
	native := envOr("NATIVE_URL", "http://127.0.0.1:8722")
	if native == "off" {
		return nil
	}
	var atts []map[string]any
	_ = json.Unmarshal([]byte(msg.GetString("atts")), &atts)
	var out []discordFile
	total := 0
	for _, a := range atts {
		t := toFloat(a["type"])
		mime, _ := a["mime"].(string)
		id, _ := a["id"].(string)
		if id == "" || !(t == 1 || t == 2 || t == 3 || t == 15 || strings.HasPrefix(mime, "image/")) || len(out) == 10 {
			continue
		}
		for try := 0; try < 3; try++ {
			if try > 0 {
				time.Sleep(4 * time.Second)
			}
			resp, err := discordHTTP.Get(native + "/api/media/" + neturl.PathEscape(id))
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
			resp.Body.Close()
			ct := resp.Header.Get("Content-Type")
			if resp.StatusCode != 200 || len(data) > 8<<20 || total+len(data) > 24<<20 || !strings.HasPrefix(ct, "image/") {
				continue
			}
			ext := map[string]string{"image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}[ct]
			if ext == "" {
				ext = ".jpg"
			}
			out = append(out, discordFile{id: id, name: fmt.Sprintf("%s-%d%s", map[bool]string{true: "sticker", false: "photo"}[t == 1], len(out)+1, ext), mime: ct, data: data})
			total += len(data)
			break
		}
	}
	return out
}

// chatName: the chat's current name from the archive, else the rule's label.
func chatName(app core.App, rule, msg *core.Record) string {
	if t, err := app.FindFirstRecordByFilter("threads", "key = {:k}", dbx.Params{"k": msg.GetString("thread_key")}); err == nil && t.GetString("name") != "" {
		return t.GetString("name")
	}
	return rule.GetString("name")
}

// "24/10/2026 15:19 - from - message" (Nat, 2026-10-24; the chat is the post's username); attachments not uploaded stay
// as labels. Plain text in Bangkok time, so it can be searched and copied like the rest.
func discordText(msg *core.Record, files []discordFile, chat string) string {
	up := map[string]bool{}
	for _, f := range files {
		up[f.id] = true
	}
	text := msg.GetString("text")
	if len(files) > 0 && strings.HasSuffix(text, "sent an attachment.") {
		text = "" // the app's stand-in text for what is now attached
	}
	var atts []map[string]any
	_ = json.Unmarshal([]byte(msg.GetString("atts")), &atts)
	for _, a := range atts {
		if id, _ := a["id"].(string); up[id] {
			continue
		}
		label := map[float64]string{1: "sticker", 2: "photo", 3: "GIF", 15: "GIF", 4: "video", 7: "link/file"}[toFloat(a["type"])]
		if label == "" {
			label = "attachment"
		}
		if t, _ := a["title"].(string); t != "" {
			label += " · " + t
		}
		text = strings.TrimSpace(text + "\n[" + label + "]")
	}
	_ = chat // the post's username already names the chat
	head := time.UnixMilli(int64(msg.GetInt("ts"))).In(bangkok).Format("02/01/2006 15:04")
	switch msg.GetString("kind") {
	case "admin":
		return trimRunes(head+" - _"+text+"_", 2000)
	case "unsent":
		text = "_unsent a message_"
	}
	who := msg.GetString("sender_name")
	if msg.GetString("sender") == ownerID {
		who = "You"
	}
	return trimRunes(head+" - "+who+" - "+text, 2000)
}

// withQuery adds a query parameter to a webhook URL that may already carry one — a Discord
// thread is "<webhook>?thread_id=<id>", so "?wait=true" would break it.
func withQuery(url, kv string) string {
	if strings.Contains(url, "?") {
		return url + "&" + kv
	}
	return url + "?" + kv
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func trimRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func registerForwardCommands(app *pocketbase.PocketBase) {
	fw := &cobra.Command{Use: "forward", Short: "rules that post a chat's new messages to a Discord webhook"}

	var name, thread, webhook string
	var daily bool
	add := &cobra.Command{Use: "add", Short: "add an enabled rule (webhook from --webhook or $DISCORD_WEBHOOK)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if webhook == "" {
				webhook = os.Getenv("DISCORD_WEBHOOK")
			}
			if name == "" || thread == "" || webhook == "" {
				return fmt.Errorf("--name, --thread and a webhook are required")
			}
			col, err := app.FindCollectionByNameOrId("forwards")
			if err != nil {
				return err
			}
			r := core.NewRecord(col)
			r.Set("name", name)
			r.Set("platform", "Messenger")
			r.Set("thread_key", thread)
			r.Set("webhook", webhook)
			r.Set("enabled", true)
			r.Set("daily", daily)
			if err := app.Save(r); err != nil {
				return err
			}
			fmt.Printf("forward %s: thread %s → Discord (enabled%s)\n", r.Id, thread, map[bool]string{true: ", one forum post per day"}[daily])
			return nil
		}}
	add.Flags().StringVar(&name, "name", "", "label, also the Discord username of the posts")
	add.Flags().StringVar(&thread, "thread", "", "thread key (messages.thread_key)")
	add.Flags().StringVar(&webhook, "webhook", "", "Discord webhook URL (prefer $DISCORD_WEBHOOK)")
	add.Flags().BoolVar(&daily, "daily", false, "the webhook is a forum channel's: one post per Bangkok day, titled dd/mm/yyyy")

	list := &cobra.Command{Use: "list", Short: "rules, with the ledger counts (webhooks masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			rs, err := app.FindRecordsByFilter("forwards", "", "created", 0, 0)
			if err != nil {
				return err
			}
			for _, r := range rs {
				n := map[string]int64{}
				for _, s := range []string{"sent", "failed", "sending"} {
					n[s], _ = app.CountRecords("forward_log", dbx.HashExp{"forward": r.Id, "status": s})
				}
				w := r.GetString("webhook")
				if i := strings.LastIndex(w, "/"); i > 0 {
					w = w[:i+1] + "…"
				}
				fmt.Printf("%s  %-5v  %-28s thread %-18s sent %d · failed %d · sending %d  %s%s\n", r.Id, r.GetBool("enabled"),
					r.GetString("name"), r.GetString("thread_key"), n["sent"], n["failed"], n["sending"], w,
					map[bool]string{true: "  daily"}[r.GetBool("daily")])
			}
			return nil
		}}

	toggle := func(on bool) *cobra.Command {
		use := map[bool]string{true: "on", false: "off"}[on]
		return &cobra.Command{Use: use + " <id>", Short: "enable or disable a rule", Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				r, err := app.FindRecordById("forwards", args[0])
				if err != nil {
					return err
				}
				r.Set("enabled", on)
				if err := app.Save(r); err != nil {
					return err
				}
				fmt.Println("forward", r.Id, use+" ("+strconv.FormatBool(on)+")")
				return nil
			}}
	}
	var dry bool
	backfill := &cobra.Command{Use: "backfill <id>", Short: "post a rule's chat history, oldest first, to its webhook (ledger-deduped, batched)",
		Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			rule, err := app.FindRecordById("forwards", args[0])
			if err != nil {
				return err
			}
			return forwardBackfill(app, rule, dry)
		}}
	backfill.Flags().BoolVar(&dry, "dry", false, "count what would be posted, post nothing")
	fw.AddCommand(add, list, toggle(true), toggle(false), backfill)
	app.RootCmd.AddCommand(fw)
}

// forwardBackfill posts every message of the rule's chat that the ledger has not seen, oldest
// first. Consecutive messages without pictures share one Discord post (one line each, under
// 2000 characters); a message with pictures is its own post. Each message gets its ledger row
// before the post it rides in, so a re-run never posts it twice. ~1.2 s between posts keeps
// under Discord's webhook limit (5 per 2 s); a 429 waits and retries once.
func forwardBackfill(app core.App, rule *core.Record, dry bool) error {
	ledger, err := app.FindCollectionByNameOrId("forward_log")
	if err != nil {
		return err
	}
	msgs, err := app.FindRecordsByFilter("messages", "thread_key = {:k}", "ts", 0, 0, dbx.Params{"k": rule.GetString("thread_key")})
	if err != nil {
		return err
	}
	chat := ""
	if len(msgs) > 0 {
		chat = chatName(app, rule, msgs[0])
	}
	webhook := withQuery(rule.GetString("webhook"), "wait=true")
	posts, sent, skipped := 0, 0, 0
	var lines []string
	var rows []*core.Record
	size, lineTs, daily := 0, 0, rule.GetBool("daily")
	flush := func() {
		if len(lines) == 0 {
			return
		}
		build := func(threadName string) ([]byte, string) {
			p := map[string]any{"username": trimRunes(chat, 80), "content": strings.Join(lines, "\n"),
				"allowed_mentions": map[string]any{"parse": []string{}}}
			if threadName != "" {
				p["thread_name"] = threadName
			}
			body, _ := json.Marshal(p)
			return body, "application/json"
		}
		var code int
		var errText string
		if daily {
			code, errText = sendDaily(app, rule, lineTs, webhook, build, rows[0].GetString("hash"))
		} else {
			body, ctype := build("")
			code, errText, _ = sendWebhook(rule, webhook, body, ctype, rows[0].GetString("hash"))
		}
		finishRows(app, code, errText, rows...)
		if errText != "" {
			log.Printf("backfill: post failed (HTTP %d): %s", code, errText)
		}
		posts++
		lines, rows, size = nil, nil, 0
		time.Sleep(1200 * time.Millisecond)
	}
	take := func(m *core.Record) *core.Record {
		sum := sha256.Sum256([]byte(rule.Id + "|" + m.GetString("mid")))
		row := core.NewRecord(ledger)
		row.Set("forward", rule.Id)
		row.Set("mid", m.GetString("mid"))
		row.Set("hash", hex.EncodeToString(sum[:]))
		row.Set("status", "sending")
		if dry {
			if n, _ := app.CountRecords("forward_log", dbx.HashExp{"hash": row.GetString("hash")}); n > 0 {
				return nil
			}
			return row
		}
		if app.Save(row) != nil {
			return nil // already in the ledger: posted before
		}
		return row
	}
	for i, m := range msgs {
		row := take(m)
		if row == nil {
			skipped++
			continue
		}
		sent++
		if dry {
			continue
		}
		hasPics := false
		var atts []map[string]any
		_ = json.Unmarshal([]byte(m.GetString("atts")), &atts)
		for _, a := range atts {
			if t := toFloat(a["type"]); t == 1 || t == 2 || t == 3 || t == 15 {
				hasPics = true
			}
		}
		if hasPics {
			flush()
			code, errText := postOne(app, rule, m, row.GetString("hash"))
			finishRows(app, code, errText, row)
			posts++
			time.Sleep(1200 * time.Millisecond)
		} else {
			line := discordText(m, nil, chat)
			if size+len(line)+1 > 1900 || (daily && len(lines) > 0 && dayKey(lineTs) != dayKey(m.GetInt("ts"))) {
				flush() // a daily rule never mixes two days in one post
			}
			if len(lines) == 0 {
				lineTs = m.GetInt("ts")
			}
			lines, rows, size = append(lines, line), append(rows, row), size+len(line)+1
		}
		if (i+1)%100 == 0 {
			log.Printf("backfill: %d/%d messages, %d posts", i+1, len(msgs), posts)
		}
	}
	flush()
	log.Printf("backfill %s: %d messages %s, %d already in the ledger, %d posts", rule.GetString("name"), sent,
		map[bool]string{true: "would be posted", false: "posted"}[dry], skipped, posts)
	return nil
}
