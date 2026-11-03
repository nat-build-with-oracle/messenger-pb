package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/ui"
)

func TestAdminIndexAddsOneScript(t *testing.T) {
	b, err := adminIndex(ui.DistDirFS)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Count(s, adminPasskeyScript) != 1 || strings.Index(s, adminPasskeyScript) > strings.Index(s, "</head>") {
		t.Fatalf("script not added once inside <head>:\n%s", s)
	}
	if !strings.Contains(s, `src="./assets/index-`) || strings.Contains(s, "<script>") {
		t.Fatalf("the admin's own scripts changed, or an inline script crept in:\n%s", s)
	}
}

// The two routes beside PocketBase's own /_/{path...} (as apis.Serve registers it): ours answer
// for / and the script, PocketBase still for every other file.
func TestAdminPasskeyRoutes(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	r.GET("/_/{path...}", apis.Static(ui.DistDirFS, false))
	index, err := adminIndex(ui.DistDirFS)
	if err != nil {
		t.Fatal(err)
	}
	bindAdminPasskey(r, index)
	h, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}

	w := get("/_/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), adminPasskeyScript) || w.Header().Get("Content-Security-Policy") != adminCSP {
		t.Fatalf("/_/: %d csp=%q\n%s", w.Code, w.Header().Get("Content-Security-Policy"), w.Body.String())
	}
	if !strings.Contains(adminCSP, "script-src 'self' http://127.0.0.1:*;") {
		t.Fatalf("CSP loosened: %s", adminCSP)
	}
	w = get("/_/messenger-pb-passkey.js")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(w.Body.String(), "only=_superusers") {
		t.Fatalf("script: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w = get("/_/libs/uplot/uplot.css"); w.Code != 200 || strings.Contains(w.Body.String(), adminPasskeyScript) {
		t.Fatalf("PocketBase's own files: %d", w.Code)
	}
}

func TestPasskeyAdminSuperusersOnly(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	login := func(k *softKey) (int, map[string]any) {
		code, b := env.do("POST", "/api/passkey/login/begin", "", nil)
		if code != 200 {
			t.Fatalf("login/begin %d %v", code, b)
		}
		return env.do("POST", "/api/passkey/login/finish?only=_superusers&sid="+b["sid"].(string), "",
			k.get(t, b["options"].(map[string]any), testOrigin, k.handle))
	}

	su, suTok := env.record("_superusers", "admin@example.com", "")
	ks := newSoftKey(t)
	env.register(suTok, ks, testOrigin)
	code, b := login(ks)
	if r, _ := b["record"].(map[string]any); code != 200 || r["id"] != su.Id || r["collectionName"] != "_superusers" {
		t.Fatalf("superuser on the admin: %d %v", code, b)
	}

	_, revTok := env.record("users", "nat@example.com", "reviewer")
	kr := newSoftKey(t)
	env.register(revTok, kr, testOrigin)
	code, b = login(kr)
	if code != 403 || b["token"] != nil || !strings.Contains(b["message"].(string), "superuser passkeys only") {
		t.Fatalf("reviewer on the admin: %d %v", code, b)
	}
	_, l := env.do("GET", "/api/passkey", revTok, nil)
	if it := l["items"].([]any); it[0].(map[string]any)["last_used"] != "" {
		t.Fatalf("a refused sign-in moved last_used: %v", l)
	}
	// the review page, without only=, still signs the reviewer in
	if code, _ := env.login(kr, testOrigin, kr.handle); code != 200 {
		t.Fatalf("reviewer on the review page: %d", code)
	}
}
