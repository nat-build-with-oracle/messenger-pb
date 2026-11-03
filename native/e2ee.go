// E2EE (1:1 "advanced crypto") messages: advanced_crypto_transport_messages.serialized_payload
// is empty on this build — the app keeps the already-decrypted message in msys
// client_messages (text, replies, attachments, link cards). Threads link through
// advanced_crypto_transport_threads.thread_id, the same key space as fts. Media
// lives as plaintext files under files/bankAndEcho/media_bank, but only the few
// the app still has cached (client_attachment_store_keys maps them).
package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const bankRoot = "/data/data/com.facebook.orca/files/bankAndEcho"

// display_ts_ms is INT64_MIN on system placeholders; fall back, then drop rows with no time.
const e2eeSelect = `SELECT m.pk, m.persistent_id, CAST(t.thread_id AS TEXT),
	CASE WHEN m.display_ts_ms > 0 THEN m.display_ts_ms ELSE COALESCE(m.authoritative_ts_ms, 0) END, CAST(m.sender_contact_pk AS TEXT),
	COALESCE(m.text,''), m.message_content_type,
	COALESCE(m.reply_message_text,''), COALESCE(CAST(m.reply_sender_contact_pk AS TEXT),''), COALESCE(m.reply_snippet,''),
	m.reply_message_pk IS NOT NULL OR m.reply_message_text IS NOT NULL
	FROM client_messages m JOIN advanced_crypto_transport_threads t ON t.client_thread_pk = m.thread_pk
	WHERE m.is_hidden = 0 AND (m.display_ts_ms > 0 OR m.authoritative_ts_ms > 0) `

// e2eeMsgs returns decrypted E2EE messages with replies + attachments; also the max pk seen.
func e2eeMsgs(db *sql.DB, where string, args ...any) ([]apiMsg, int64, error) {
	rows, err := db.Query(e2eeSelect+where, args...)
	if err != nil {
		return nil, 0, err
	}
	out := []apiMsg{}
	byPK := map[int64]int{}
	var maxPK int64
	for rows.Next() {
		var m apiMsg
		var pk int64
		var ctype int
		var hasReply bool
		var r reply
		if err := rows.Scan(&pk, &m.ID, &m.Thread, &m.TS, &m.Sender, &m.Text, &ctype,
			&r.Text, &r.To, &r.Snippet, &hasReply); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if hasReply {
			m.Reply = &r
		}
		if ctype == 5 && m.Text == "" {
			m.Text = "Couldn't load message"
		}
		maxPK = max(maxPK, pk)
		byPK[pk] = len(out)
		out = append(out, m)
	}
	rows.Close()
	if len(out) == 0 {
		return out, maxPK, rows.Err()
	}
	pks := make([]any, 0, len(byPK))
	for pk := range byPK {
		pks = append(pks, pk)
	}
	arows, err := db.Query(`SELECT a.message_pk, a.pk, a.attachment_type, COALESCE(a.width, a.preview_width, 0), COALESCE(a.height, a.preview_height, 0),
		COALESCE(a.mime_type,''), COALESCE(a.filename,''), COALESCE(a.filesize,0),
		COALESCE(NULLIF(x.title_text,''), x.header_title, ''), COALESCE(NULLIF(x.subtitle_text,''), x.header_subtitle, '')
		FROM client_attachments a LEFT JOIN client_xmas x ON x.attachment_pk = a.pk
		WHERE a.message_pk IN (?`+strings.Repeat(",?", len(pks)-1)+`) ORDER BY a.attachment_index`, pks...)
	if err != nil {
		return out, maxPK, nil
	}
	defer arows.Close()
	for arows.Next() {
		var mpk, apk int64
		var a attachment
		if arows.Scan(&mpk, &apk, &a.Type, &a.W, &a.H, &a.Mime, &a.File, &a.Size, &a.Title, &a.Sub) != nil {
			continue
		}
		a.ID = "e" + strconv.FormatInt(apk, 10)
		if a.Type == 6 && a.File == "" { // file without a name: show its type
			a.File = a.Mime
		}
		if i, ok := byPK[mpk]; ok {
			out[i].Atts = append(out[i].Atts, a)
		}
	}
	return out, maxPK, nil
}

// e2eeThreads: latest activity per E2EE thread (picks up photos/links fts never indexes).
func e2eeThreads(db *sql.DB) []threadRow {
	rows, err := db.Query(`SELECT CAST(t.thread_id AS TEXT), c.display_ts_ms, COALESCE(c.snippet,''),
		COALESCE(CAST(c.snippet_sender_contact_pk AS TEXT),''),
		(SELECT COUNT(*) FROM client_messages m WHERE m.thread_pk = c.pk)
		FROM client_threads c JOIN advanced_crypto_transport_threads t ON t.client_thread_pk = c.pk`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []threadRow
	for rows.Next() {
		var t threadRow
		if rows.Scan(&t.ID, &t.Last, &t.Preview, &t.Sender, &t.C) == nil {
			out = append(out, t)
		}
	}
	return out
}

// e2eeMediaPath maps an "e<attachment pk>" id to a cached plaintext file, full size first.
func e2eeMediaPath(db *sql.DB, id string) string {
	pk, err := strconv.ParseInt(strings.TrimPrefix(id, "e"), 10, 64)
	if err != nil || db == nil {
		return ""
	}
	rows, err := db.Query(`SELECT persisted_path FROM client_attachment_store_keys
		WHERE attachment_pk = ? AND persisted_path <> '' ORDER BY persisted_path LIKE '%/att.%' DESC`, pk)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil || strings.Contains(p, "..") {
			continue
		}
		full := filepath.Join(bankRoot, p)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	return ""
}

func serveE2EEMedia(db *sql.DB, w http.ResponseWriter, r *http.Request, id string) {
	path := e2eeMediaPath(db, id) // the app's own plaintext cache first
	if path == "" {
		path = filepath.Join(mediaDir, id) // then ours: decrypted from the CDN
		if _, err := os.Stat(path); err != nil {
			pk, _ := strconv.ParseInt(strings.TrimPrefix(id, "e"), 10, 64)
			if last, missed := avatarMiss.Load("m" + id); !missed || time.Since(last.(time.Time)) > 10*time.Minute {
				if fetchE2EEMedia(db, pk, path) != nil {
					avatarMiss.Store("m"+id, time.Now())
				}
			}
			if _, err := os.Stat(path); err != nil {
				http.NotFound(w, r)
				return
			}
		}
	}
	w.Header().Set("cache-control", "max-age=86400")
	http.ServeFile(w, r, path)
}

// --- E2EE media from Facebook's CDN ----------------------------------------
// advanced_crypto_transport_attachments holds WhatsApp-style media metadata:
// media_key (32B), direct_path (signed CDN path, host in _nc_ht), encrypted_hash
// and plaintext_hash (SHA-256). Keys: HKDF-SHA256(media_key, info="WhatsApp
// <Kind> Keys") → iv(16) | aes key(32) | mac key(32). Body: AES-256-CBC (PKCS#7)
// followed by HMAC-SHA256(iv|ciphertext)[:10]. Verified on-device 2026-10-15.

var mediaInfos = [][]byte{
	[]byte("WhatsApp Image Keys"), []byte("WhatsApp Video Keys"),
	[]byte("WhatsApp Document Keys"), []byte("WhatsApp Audio Keys"),
}

func hkdfSHA256(key, info []byte, n int) []byte {
	prk := hmacSum(make([]byte, 32), key)
	var out, t []byte
	for i := byte(1); len(out) < n; i++ {
		t = hmacSum(prk, append(append(append([]byte{}, t...), info...), i))
		out = append(out, t...)
	}
	return out[:n]
}

func hmacSum(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func decryptMedia(enc, mediaKey []byte) ([]byte, error) {
	if len(enc) < 26 {
		return nil, errors.New("short media")
	}
	body, tag := enc[:len(enc)-10], enc[len(enc)-10:]
	for _, info := range mediaInfos {
		k := hkdfSHA256(mediaKey, info, 80)
		iv, ck, mk := k[:16], k[16:48], k[48:80]
		if !hmac.Equal(hmacSum(mk, append(append([]byte{}, iv...), body...))[:10], tag) {
			continue // wrong media kind
		}
		if len(body)%aes.BlockSize != 0 {
			return nil, errors.New("bad block size")
		}
		block, _ := aes.NewCipher(ck)
		pt := make([]byte, len(body))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(pt, body)
		pad := int(pt[len(pt)-1])
		if pad == 0 || pad > aes.BlockSize || pad > len(pt) {
			return nil, errors.New("bad padding")
		}
		// The HMAC already authenticates the body. plaintext_hash matches on most
		// rows but not all (seen on a valid, MAC-verified JPEG), so it's not a gate.
		return pt[:len(pt)-pad], nil
	}
	return nil, errors.New("no media key info matched")
}

// fetchE2EEMedia downloads + decrypts attachment pk into path. Tries every
// transport row for the attachment (rows can carry different direct paths).
func fetchE2EEMedia(db *sql.DB, pk int64, path string) error {
	rows, err := db.Query(`SELECT media_key, direct_path, COALESCE(encrypted_hash, x''), COALESCE(plaintext_hash, x'')
		FROM advanced_crypto_transport_attachments
		WHERE client_attachment_pk = ? AND media_key IS NOT NULL AND direct_path <> '' ORDER BY content_type`, pk)
	if err != nil {
		return err
	}
	type src struct {
		key, eh, ph []byte
		dp          string
	}
	var srcs []src
	for rows.Next() {
		var s src
		if rows.Scan(&s.key, &s.dp, &s.eh, &s.ph) == nil {
			srcs = append(srcs, s)
		}
	}
	rows.Close()
	lastErr := errors.New("no transport row")
	for _, s := range srcs {
		u, err := url.Parse(s.dp)
		if err != nil {
			continue
		}
		host := u.Query().Get("_nc_ht")
		if host == "" {
			host = "scontent.xx"
		}
		avatarSem <- struct{}{}
		resp, err := avatarHTTP.Get("https://" + host + ".fbcdn.net" + s.dp)
		if err != nil {
			<-avatarSem
			lastErr = err
			continue
		}
		enc, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
		resp.Body.Close()
		<-avatarSem
		if err != nil || resp.StatusCode != 200 {
			lastErr = fmt.Errorf("cdn %d", resp.StatusCode)
			continue
		}
		if len(s.eh) == 32 {
			if sum := sha256.Sum256(enc); !bytes.Equal(sum[:], s.eh) {
				lastErr = errors.New("encrypted hash mismatch")
				continue
			}
		}
		pt, err := decryptMedia(enc, s.key)
		if err != nil {
			lastErr = err
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path+".tmp", pt, 0o644); err != nil {
			return err
		}
		return os.Rename(path+".tmp", path)
	}
	return lastErr
}

// prefetchE2EEMedia decrypts every E2EE image/sticker while its CDN path is
// still signed (~30 days); videos (4) and documents (3) decrypt on first request.
func prefetchE2EEMedia(db *sql.DB) {
	if db == nil {
		return
	}
	rows, err := db.Query(`SELECT DISTINCT client_attachment_pk FROM advanced_crypto_transport_attachments
		WHERE media_key IS NOT NULL AND direct_path <> '' AND attachment_type NOT IN (3, 4)`)
	if err != nil {
		return
	}
	var pks []int64
	for rows.Next() {
		var pk int64
		if rows.Scan(&pk) == nil {
			pks = append(pks, pk)
		}
	}
	rows.Close()
	for _, pk := range pks {
		id := "e" + strconv.FormatInt(pk, 10)
		path := filepath.Join(mediaDir, id)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if last, missed := avatarMiss.Load("m" + id); missed && time.Since(last.(time.Time)) < time.Hour {
			continue
		}
		if fetchE2EEMedia(db, pk, path) != nil {
			avatarMiss.Store("m"+id, time.Now())
		}
	}
}
