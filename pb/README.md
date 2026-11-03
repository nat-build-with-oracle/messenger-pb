# messenger-pb — the archive as a PocketBase app

PocketBase v0.40 used as a Go framework: the Messenger archive (contacts,
threads, messages from fts.db + msys) lives in PocketBase collections defined
by Go migrations; a sync loop inside `serve` turns new phone messages into
records, and record hooks publish every created message to MQTT.

```
phone dbs ──import / sync──▶ PocketBase (pb_data/data.db) ──OnRecordAfterCreateSuccess──▶ MQTT
 fts.db                       contacts · threads · messages      messenger/messages/<thread_key>
 msys (groups, e2ee)          REST API + admin UI :8091/:8092    messenger/threads/<key>
```

## Run (Mac, against pulled snapshots)

```bash
(cd ui && bun install && bun run build)                  # 0. the page (ui/ → sqweb/dist)
go build -o messenger-pb .
./messenger-pb migrate up                                 # 1. schema
./messenger-pb superuser upsert demo@m5.local admindemo   # demo admin — local only
./messenger-pb import --fts source/fts.db --msys source/msys.db   # 2. bulk load, hooks muted
mosquitto -c mosquitto.conf -v   # listener 18830 127.0.0.1 — NOT 1883/1884: those are the fleet bus
MQTT_URL=tcp://127.0.0.1:18830 FTS_DB=source/fts.db MSYS_DB=source/msys.db \
  ./messenger-pb serve --http 127.0.0.1:8091             # 3. API + admin + sync + hooks
mosquitto_sub -h 127.0.0.1 -p 18830 -t 'messenger/#' -v    # 4. watch
```

## Run on the phone (live dbs, root)

```bash
(cd ui && bun install && bun run build)
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags=-s -o messenger-pb-android .
adb push messenger-pb-android /data/local/tmp/messenger-pb
adb reverse tcp:18830 tcp:18830 && adb forward tcp:8092 tcp:8092
adb shell "su -c 'cd /data/local/tmp && ./messenger-pb --dir pb/pb_data migrate up \
  && ./messenger-pb --dir pb/pb_data import \
  && MQTT_URL=tcp://127.0.0.1:18830 ./messenger-pb --dir pb/pb_data serve --http 127.0.0.1:8092'"
```

Without flags the importer and sync read the phone's live fts.db and msys db.

## Pieces

| File | What |
|---|---|
| `migrations/1792040000_init_archive.go` | collections + unique indexes (`fbid`, `key`, `mid`); reads need sign-in since `1792070000` |
| `source.go` | readers for fts / msys messages / E2EE `client_messages`, with cursors |
| `store.go` | idempotent upserts, thread+timestamp dedupe, `import` command, sync loop |
| `hooks.go` | paho MQTT client; `OnRecordAfterCreateSuccess` → publish JSON (QoS 1) |

Env: `MQTT_URL` (unset = MQTT off; there is no default broker), `MQTT_USER`/`MQTT_PASS`, `MQTT_PREFIX` (default `messenger`),
`FTS_DB`, `MSYS_DB`, `SYNC=0` to disable, `SYNC_EVERY` (default `3s`).

The demo password is for 127.0.0.1 only. Bind elsewhere → change it first.

## Pick the broker deliberately

On this Mac `127.0.0.1:1883` and `127.0.0.1:1884` belong to the fleet's own
mosquitto (another user's service, invisible to `lsof` as this user). A second
broker started with `mosquitto -p 1884` binds only `[::1]` and *looks* fine,
while clients dialing `127.0.0.1` reach the fleet bus instead. Before using a
port, check both families and confirm the listener is yours:
`nc -z 127.0.0.1 PORT; nc -z ::1 PORT; lsof -nP -iTCP:PORT -sTCP:LISTEN`.

## send-queue (approval gate for replies)

Ported from `send-queue` (Nexus Oracle, 2026-09-24) into this app: collections
`sends` + `send_events` (migration `1792060000_send_queue.go`), routes and guard in `sq.go`,
the agent CLI in `sqcli.go`. Same invariants as the original:

- nothing is recorded `sent` unless its **current** revision was approved;
- revising drops a draft back to `pending` (approval binds to the exact text);
- the agent can never approve its own drafts.

The original enforced the last rule by network address; here it is **identity**. The agent
logs in as a `users` record with `role=agent`; approve/reject need a superuser or
`role=reviewer`. Direct edits in the admin UI go through the same guard (illegal status
moves are refused, editing the text bumps the revision and resets to pending), and every
change is a `send_events` row.

```bash
./messenger-pb sq user agent@m5.local '<agent password>' --role agent       # once (operator)
export SQ_API=http://127.0.0.1:8092 SQ_AGENT_EMAIL=agent@m5.local SQ_AGENT_PASSWORD=…
./messenger-pb sq add --platform Messenger --chat "Exact Chat Name" --file msg.txt --note "why"
#   → reviewer opens the review page at / (or <SQ_BASE>/#<id>) and approves, rejects or comments
./messenger-pb sq ok <id>                     # exit 0 only if the CURRENT rev is approved
./messenger-pb sq sent <id> --proof "…" --img shot.png
```

The review page (Preact + Tailwind components in `ui/`, built into `sqweb/dist` and embedded
by `sqweb.go`, served at `/`) loads nothing from a CDN: it holds the reviewer's login and the
approve button, so every script it runs is in the binary, behind a `default-src 'self'` CSP. It signs
in against this server's API: a superuser or `role=reviewer` gets **อนุมัติ rev N**, reject
and comment buttons; an agent can read and comment only. Approving sends the revision
number the reviewer read. If the agent revised in the meantime, the server answers 409
instead of approving text nobody saw. The admin UI gets the same check: a form opened
before the revision is refused. Proof screenshots (`proof_img`) are protected files and
need a file token.

A login link opens the page already signed in, for `role=agent` and `role=viewer` only (a
reviewer approves sends, so always types a password). `role=viewer` reads the chats, seen
receipts and aliases and nothing else: no queue, no forwards, no writes.

```bash
./messenger-pb sq user viewer@example.com '<password>' --role viewer   # once
./messenger-pb login-link viewer@example.com --for 24h                 # prints http://100.64.0.2:8097/#login=…
./messenger-pb login-link viewer@example.com --revoke                  # kills every link and login of that user
```

The token sits after `#`, so it never reaches the server's logs, and the page drops it from the
address bar on load. It is a static token: it cannot be refreshed and ends with the link. Anyone
holding the link is signed in until then, so send it privately.

**Passkeys** (`passkey.go`): a reviewer or superuser can add Touch ID / Face ID (iCloud
Keychain) or a hardware key from the account panel (the `passkey` button, or the round avatar),
then sign in with **เข้าด้วย passkey**. The password keeps working. User verification is
required: a key that only taps, with no finger, face or PIN, is refused. WebAuthn needs HTTPS with
a valid certificate, so the page must be opened through m5's tailscale serve,
`https://mac.example.ts.net:8447/` (`tailscale serve --bg --https=8447 http://100.64.0.2:8097`);
on `http://100.64.0.2:8097` the button does not show. The server reads `PASSKEY_RP_ID` and
`PASSKEY_ORIGIN` (set in `android-sq-start.sh`); unset, the passkey routes answer 503.
`login-link --revoke` also removes that user's passkeys.

`SQ_NOTIFY=herdr:<pane>` (or `maw:<target>`) on `serve` pings the agent's terminal when the
reviewer approves, rejects or comments, with the reviewer's text quoted as review data.
`SQ_READ_ONLY="Chat A,Chat B"` blocks queueing into those chats.

**The reviewer's password must be one the agent does not know.** If the agent holds a
superuser login it can approve its own drafts, and the gate is only discipline again.

### On the phone

The queue runs as its own instance on the phone: `/data/local/tmp/sq`, listening on the
Tailscale address only (`http://phone.example.ts.net:8097/`). Its sync loop reads
the phone's live Messenger dbs, so the review page also has a **แชท** tab: every chat, newest
first, with search, and each chat's last 200 messages (replies, unsent markers, attachments
as labels — no images; those stay in the native viewer on :8722). Reading chats needs the same
sign-in as the queue (migration `1792070000_archive_signed_in.go`); without it the API lists
nothing. Messages sent from messenger.com reach the phone's db only when the Messenger app
syncs, so open the app if one is missing.

The chat tabs: **รวม** (Messenger and LINE in one list, newest first), **Messenger** and
**LINE**, laid out like messenger.com. Profile pictures and attachment previews come from the
native server's cache on the phone (`messenger serve 127.0.0.1:8722`, started by `start.sh` if
it is not running) through `/chat/avatar/{id}` and `/chat/media/{id}` (`sqmedia.go`), which
need a file token like protected files do. LINE is read live from the LINE app's own db
(`line.go`: `naver_line` + `contact`); nothing is copied, and stickers, photos and files show
as labels. An E2EE 1:1 chat's empty twin row in msys is folded into the real chat, and E2EE
chats get their preview from their newest message.
`android-sq-start.sh` (installed as `/data/local/tmp/sq/start.sh`) starts or restarts it
with no `MQTT_URL` and `--dev=false`. `just pb-deploy-phone` (`deploy-phone.sh`) does all of it: UI build, Android build, push, restart. By hand: push the binary to `/data/local/tmp`
first (adb cannot write into the root-owned `sq/` directory), then `su -c 'mv …'` it into place
and run `start.sh`. Processes die when the phone reboots, so run
`su -c 'sh /data/local/tmp/sq/start.sh'` again after a reboot. `SQ_NOTIFY` is unset there
(the phone has no herdr), so the agent finds approvals with `sq ok`.

### Forwarding to Discord (`forward.go`)

Rules in `forwards` (superuser-only; set them from a chat's ⚙ settings in the page, in
PocketBase admin, or `messenger-pb forward add --name … --thread <key>` with `$DISCORD_WEBHOOK`)
post each NEW message of that chat to a Discord webhook as `**Name**: text`, mentions off.
Every post first takes a row in `forward_log` under a unique hash of (rule, message id), then
records the HTTP result there: a message is never posted twice, even across restarts and
re-syncs, and a crash mid-post loses that one post rather than duplicating it. Turning a rule
on does not backfill history. The page's **บันทึก** tab lists every forward (hash, HTTP status,
error) and every delivery event from the queue. Messenger only: LINE is not copied into the
archive. `dns.go` gives the phone a resolver (Android has no `/etc/resolv.conf`).

**One forum post per day** (`--daily`, field `daily`): when the webhook belongs to a Discord
*forum* channel, each Bangkok day of the chat gets its own post, titled `dd/mm/yyyy`. The day's
first message opens the post (`thread_name`). Its id is kept in `forward_days`, one row per
(rule, day), and later messages that day go to `?thread_id=`. If a post is deleted in Discord,
the next message opens it again. Backfill never mixes two days in one post. A webhook in a plain
text channel cannot open threads, so `--daily` needs a forum.
`messenger-pb forward add --name "Chat" --thread <key> --daily`, then `forward backfill <id>`.

### The sender (`sqsender.go`, `sender/messenger.mjs`)

A `users` record with `role=sender` may only read the outbox (`GET /api/sq/outbox`), claim an
approved revision (`POST /api/sq/sends/{id}/claim {rev}` → `sending`, which blocks revising),
and report it (`POST /api/sq/sends/{id}/done {rev, ok, proof}`). It can never approve.

`sender/messenger.mjs` runs on m5 next to the Messenger Surrogate hub
(`nazt/messenger-surrogate`: `node m2/hub.mjs`, the extension in Chrome on messenger.com). It
claims nothing unless the extension is attached, needs the draft's `threadKey`, drives the
extension's own draft → approve → send for exactly the approved bytes, and reports `sent` only
when the extension saw the thread's last row carry that text. No retries.

```bash
./messenger-pb sq user sender@m5.local '<password>' --role sender     # once, on the phone
set -a; . ~/.config/messenger-pb/sq-sender.env; set +a
node sender/messenger.mjs --check     # login, outbox, hub status — claims nothing
node sender/messenger.mjs             # loop every 10 s (run it in a herdr pane)
```
