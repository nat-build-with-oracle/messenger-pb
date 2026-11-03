# messenger-pb

Your own Messenger and LINE chats, on your own rooted Android phone, as a PocketBase app.

messenger-pb runs on the phone itself. It reads the chat apps' own SQLite databases (no OCR,
no screen scraping), keeps Messenger in PocketBase collections, reads LINE live, and gives you
a review page where every outgoing reply waits for a human to approve it.

```
rooted Android phone
 ├─ Messenger  msys / fts.db ──sync──▶ PocketBase collections (contacts, threads, messages)
 ├─ LINE       naver_line + contact ──read live on every request (nothing copied)
 └─ messenger-pb (PocketBase in Go) :8097
      ├─ review page (pb/ui): inbox, chats, send queue, approve / reject, log
      ├─ send queue: an agent proposes a reply, a human approves it, the sender posts it
      ├─ forwards: chat → Discord webhook rules
      └─ MCP server (pb/mcp) for agents: read chats, search, propose replies
```

## What is here

| Folder | What |
|---|---|
| `pb/` | the PocketBase app (Go): migrations, sync, LINE reader, send queue, passkeys, forwards, UI, MCP |
| `native/` | `messenger`, a small on-device CLI over Messenger's live `fts.db` |
| `landing/` | screenshots of the review page, made from invented data (`landing/mock/seed.py`) |

Start with [`pb/README.md`](pb/README.md): build, run on a Mac against pulled snapshots,
deploy to the phone.

## Configuration

Addresses in the docs and defaults are placeholders: `phone.example.ts.net`,
`mac.example.ts.net`, `100.64.0.2`. Set your own with the flags and environment variables
the code reads, for example `SQ_API`, `PASSKEY_RP_ID`, `PASSKEY_ORIGIN`, `S3_ENDPOINT`, and
`LINE_OWNER` (your own LINE mid, so your messages are shown as yours).

## Read this first

- It reads **your own** phone. It needs root, and it reads private app data: use it only on a
  device and accounts that are yours.
- Nothing is sent without a human approving it in the review page.
- Never commit `pb_data/`, pulled databases, `.env` files or webhook URLs. `.gitignore`
  already excludes them.

## Origin

Extracted from a private working repo on 2026-11-03 as a clean snapshot with no history.
Personal hosts, addresses, ids and names were replaced with placeholders.

## License

MIT
