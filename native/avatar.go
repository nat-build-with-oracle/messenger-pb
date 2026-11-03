// Profile / group pictures: msys stores signed fbcdn URLs that expire. Fetch on
// first request, keep a copy on the phone so the picture survives the URL dying.
package main

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

var (
	avatarDir   = envStr("MESSENGER_AVATARS", "/data/local/tmp/messenger-avatars")
	avatarHTTP  = &http.Client{Timeout: 15 * time.Second}
	avatarSem   = make(chan struct{}, 6) // the UI asks for every thread's picture at once
	avatarMiss  sync.Map                 // id -> time of last failed fetch
	reAvatarID  = regexp.MustCompile(`^[0-9]+$`)
	avatarFresh = 7 * 24 * time.Hour
)

func init() {
	// Android has no /etc/resolv.conf, so Go's pure resolver (CGO_ENABLED=0)
	// would ask 127.0.0.1:53 and fail. Point it at public DNS instead.
	if _, err := os.Stat("/etc/resolv.conf"); err == nil {
		return
	}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			c, err := d.DialContext(ctx, network, "1.1.1.1:53")
			if err != nil {
				c, err = d.DialContext(ctx, network, "8.8.8.8:53")
			}
			return c, err
		},
	}
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fetchAvatar(id, path string) error { return fetchURL(picOf(id), path) }

// fetchURL downloads a signed fbcdn URL to path (atomic rename; nothing written on failure).
func fetchURL(url, path string) error {
	avatarSem <- struct{}{}
	defer func() { <-avatarSem }()
	if url == "" {
		return os.ErrNotExist
	}
	resp, err := avatarHTTP.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return os.ErrNotExist // expired signature (403) or gone
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, path)
}

// prefetchAvatars caches every history thread/sender picture while its signed URL
// is still valid — URLs expire within days, the cached file lasts forever.
func prefetchAvatars(db *sql.DB) {
	rows, err := db.Query(`SELECT DISTINCT CAST(c1thread_id AS TEXT) FROM messages_content
		UNION SELECT DISTINCT CAST(c4sender_id AS TEXT) FROM messages_content`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var wg sync.WaitGroup
	for _, id := range ids {
		path := filepath.Join(avatarDir, id+".jpg")
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) < avatarFresh {
			continue
		}
		if picOf(id) == "" {
			continue
		}
		if last, missed := avatarMiss.Load(id); missed && time.Since(last.(time.Time)) < time.Hour {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if fetchAvatar(id, path) != nil {
				avatarMiss.Store(id, time.Now())
			}
		}()
	}
	wg.Wait()
}

// GET /api/avatar/{id} → cached jpeg, refreshed weekly while the URL is still valid; 404 → UI shows initials.
func handleAvatar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !reAvatarID.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(avatarDir, id+".jpg")
	st, statErr := os.Stat(path)
	if statErr != nil || time.Since(st.ModTime()) > avatarFresh {
		last, missed := avatarMiss.Load(id)
		if !missed || time.Since(last.(time.Time)) > 10*time.Minute {
			if err := fetchAvatar(id, path); err != nil {
				avatarMiss.Store(id, time.Now())
			} else {
				avatarMiss.Delete(id)
			}
		}
	}
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("cache-control", "max-age=86400")
	http.ServeFile(w, r, path)
}
