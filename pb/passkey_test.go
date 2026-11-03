package main

// The passkey routes against a software authenticator: real ECDSA keys, real CBOR, real
// signatures, so the library's checks run as they do for Touch ID or a hardware key.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

const (
	testRPID   = "m5.example.ts.net"
	testOrigin = "https://m5.example.ts.net:8447"
)

// --- software authenticator ----------------------------------------------------------------

type softKey struct {
	id     []byte
	priv   *ecdsa.PrivateKey
	handle []byte // the user handle it stored at registration
	count  uint32
	noUV   bool // a key that only taps: no fingerprint, face or PIN
}

func newSoftKey(t *testing.T) *softKey {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softKey{id: id, priv: priv}
}

func (k *softKey) flags(extra byte) byte {
	f := byte(0x01) | extra // UP
	if !k.noUV {
		f |= 0x04 // UV
	}
	return f
}

func authData(rpID string, flags byte, count uint32, attested []byte) []byte {
	h := sha256.Sum256([]byte(rpID))
	b := append([]byte{}, h[:]...)
	b = append(b, flags)
	b = binary.BigEndian.AppendUint32(b, count)
	return append(b, attested...)
}

func clientData(typ, challenge, origin string) []byte {
	j, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return j
}

func (k *softKey) create(t *testing.T, opts map[string]any, origin string) []byte {
	pub := opts["publicKey"].(map[string]any)
	k.handle = b64dec(t, pub["user"].(map[string]any)["id"].(string))
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1,
		-2: k.priv.PublicKey.X.FillBytes(make([]byte, 32)), -3: k.priv.PublicKey.Y.FillBytes(make([]byte, 32))})
	if err != nil {
		t.Fatal(err)
	}
	att := make([]byte, 16) // AAGUID
	att = binary.BigEndian.AppendUint16(att, uint16(len(k.id)))
	att = append(att, k.id...)
	att = append(att, cose...)
	ad := authData(pub["rp"].(map[string]any)["id"].(string), k.flags(0x40), 0, att) // AT
	obj, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": ad})
	if err != nil {
		t.Fatal(err)
	}
	return mustJSON(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(clientData("webauthn.create", pub["challenge"].(string), origin)),
			"attestationObject": b64.EncodeToString(obj)}})
}

// get signs the challenge; handle is the user handle it claims (normally its own).
func (k *softKey) get(t *testing.T, opts map[string]any, origin string, handle []byte) []byte {
	pub := opts["publicKey"].(map[string]any)
	k.count++
	ad := authData(pub["rpId"].(string), k.flags(0), k.count, nil)
	cd := clientData("webauthn.get", pub["challenge"].(string), origin)
	h := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return mustJSON(map[string]any{"id": b64.EncodeToString(k.id), "rawId": b64.EncodeToString(k.id), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(ad),
			"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(handle)}})
}

// --- harness -------------------------------------------------------------------------------

type pkEnv struct {
	t   *testing.T
	app *tests.TestApp
	h   http.Handler
}

func newPKEnv(t *testing.T, rpID, origin string) *pkEnv {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := newPasskeys(rpID, origin)
	if err != nil {
		t.Fatal(err)
	}
	pk.bind(r)
	h, err := r.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	return &pkEnv{t: t, app: app, h: h}
}

func (env *pkEnv) record(col, email, role string) (*core.Record, string) {
	c, err := env.app.FindCollectionByNameOrId(col)
	if err != nil {
		env.t.Fatal(err)
	}
	rec := core.NewRecord(c)
	rec.SetEmail(email)
	rec.SetPassword("password-123456")
	if role != "" {
		rec.Set("role", role)
	}
	if err := env.app.Save(rec); err != nil {
		env.t.Fatal(err)
	}
	tok, err := rec.NewAuthToken()
	if err != nil {
		env.t.Fatal(err)
	}
	return rec, tok
}

func (env *pkEnv) do(method, path, token string, body []byte) (int, map[string]any) {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	w := httptest.NewRecorder()
	env.h.ServeHTTP(w, req)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (env *pkEnv) register(token string, k *softKey, origin string) int {
	code, b := env.do("POST", "/api/passkey/register/begin", token, nil)
	if code != 200 {
		env.t.Fatalf("register/begin %d %v", code, b)
	}
	code, _ = env.do("POST", "/api/passkey/register/finish?sid="+b["sid"].(string)+"&name="+url.QueryEscape("MacBook Touch ID"),
		token, k.create(env.t, b["options"].(map[string]any), origin))
	return code
}

func (env *pkEnv) login(k *softKey, origin string, handle []byte) (int, map[string]any) {
	code, b := env.do("POST", "/api/passkey/login/begin", "", nil)
	if code != 200 {
		env.t.Fatalf("login/begin %d %v", code, b)
	}
	return env.do("POST", "/api/passkey/login/finish?sid="+b["sid"].(string), "", k.get(env.t, b["options"].(map[string]any), origin, handle))
}

func b64dec(t *testing.T, s string) []byte {
	b, err := b64.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// --- tests ---------------------------------------------------------------------------------

func TestPasskeyRegisterAndLogin(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	for _, c := range []struct{ col, role string }{{"_superusers", ""}, {"users", "reviewer"}} {
		rec, tok := env.record(c.col, c.col+"@example.com", c.role)
		k := newSoftKey(t)
		if code := env.register(tok, k, testOrigin); code != 200 {
			t.Fatalf("%s register: %d", c.col, code)
		}
		if got, want := string(k.handle), c.col+":"+rec.Id; got != want {
			t.Fatalf("user handle %q, want %q", got, want)
		}
		code, b := env.login(k, testOrigin, k.handle)
		if code != 200 {
			t.Fatalf("%s login: %d %v", c.col, code, b)
		}
		r, _ := b["record"].(map[string]any)
		if b["token"] == "" || r["id"] != rec.Id || r["collectionName"] != c.col {
			t.Fatalf("%s login answer: %v", c.col, b)
		}
		// the same shape as auth-with-password: the token signs the record in
		if code, _ := env.do("POST", "/api/collections/"+c.col+"/auth-refresh", b["token"].(string), nil); code != 200 {
			t.Fatalf("%s token refused: %d", c.col, code)
		}
		code, l := env.do("GET", "/api/passkey", tok, nil)
		items, _ := l["items"].([]any)
		if code != 200 || len(items) != 1 || items[0].(map[string]any)["last_used"] == "" || items[0].(map[string]any)["name"] != "MacBook Touch ID" {
			t.Fatalf("%s list: %d %v", c.col, code, l)
		}
	}
}

func TestPasskeyBeginLeaksNothing(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	_, tok := env.record("users", "nat@example.com", "reviewer")
	env.register(tok, newSoftKey(t), testOrigin)
	code, b := env.do("POST", "/api/passkey/login/begin", "", nil)
	raw := string(mustJSON(b))
	pub := b["options"].(map[string]any)["publicKey"].(map[string]any)
	if code != 200 || pub["allowCredentials"] != nil || strings.Contains(raw, "nat@") || strings.Contains(raw, "users:") {
		t.Fatalf("login/begin says too much: %s", raw)
	}
	if pub["userVerification"] != "required" {
		t.Fatalf("login/begin userVerification = %v", pub["userVerification"])
	}
}

func TestPasskeyRequiresUserVerification(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	_, tok := env.record("users", "nat@example.com", "reviewer")
	tap := newSoftKey(t)
	tap.noUV = true
	if code := env.register(tok, tap, testOrigin); code != 400 {
		t.Fatalf("register without UV: %d, want 400", code)
	}
	k := newSoftKey(t)
	if code := env.register(tok, k, testOrigin); code != 200 {
		t.Fatalf("register: %d", code)
	}
	k.noUV = true
	if code, _ := env.login(k, testOrigin, k.handle); code != 401 {
		t.Fatalf("login without UV: %d, want 401", code)
	}
}

func TestPasskeyCannotCrossAccounts(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	_, tokA := env.record("users", "a@example.com", "reviewer")
	_, tokB := env.record("_superusers", "b@example.com", "")
	ka, kb := newSoftKey(t), newSoftKey(t)
	env.register(tokA, ka, testOrigin)
	env.register(tokB, kb, testOrigin)
	// A's key claims B's handle: B has no such credential
	if code, b := env.login(ka, testOrigin, kb.handle); code != 401 || b["token"] != nil {
		t.Fatalf("A's key as B: %d %v", code, b)
	}
	// A's key, an unknown record
	if code, _ := env.login(ka, testOrigin, []byte("users:nosuchrecord0001")); code != 401 {
		t.Fatalf("A's key as nobody: %d", code)
	}
	// B removes nothing of A's
	_, l := env.do("GET", "/api/passkey", tokA, nil)
	id := l["items"].([]any)[0].(map[string]any)["id"].(string)
	if code, _ := env.do("DELETE", "/api/passkey/"+id, tokB, nil); code != 404 {
		t.Fatalf("B deleted A's passkey: %d", code)
	}
	if code, _ := env.do("DELETE", "/api/passkey/"+id, tokA, nil); code != 204 {
		t.Fatalf("A could not delete own passkey: %d", code)
	}
	if code, _ := env.login(ka, testOrigin, ka.handle); code != 401 {
		t.Fatalf("deleted passkey still signs in: %d", code)
	}
}

func TestPasskeyChallengeOnceAndOrigin(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	_, tok := env.record("users", "nat@example.com", "reviewer")
	k := newSoftKey(t)
	env.register(tok, k, testOrigin)
	_, b := env.do("POST", "/api/passkey/login/begin", "", nil)
	path := "/api/passkey/login/finish?sid=" + b["sid"].(string)
	assertion := k.get(t, b["options"].(map[string]any), testOrigin, k.handle)
	if code, _ := env.do("POST", path, "", assertion); code != 200 {
		t.Fatalf("first use: %d", code)
	}
	if code, _ := env.do("POST", path, "", assertion); code != 401 {
		t.Fatalf("replay: %d, want 401", code)
	}
	if code, _ := env.login(k, "https://evil.example.com", k.handle); code != 401 {
		t.Fatalf("wrong origin: %d, want 401", code)
	}
	if code := env.register(tok, newSoftKey(t), "https://evil.example.com"); code != 400 {
		t.Fatalf("register from wrong origin: %d, want 400", code)
	}
}

func TestPasskeyRoles(t *testing.T) {
	env := newPKEnv(t, testRPID, testOrigin)
	for _, role := range []string{"viewer", "agent"} {
		_, tok := env.record("users", role+"@example.com", role)
		if code, _ := env.do("POST", "/api/passkey/register/begin", tok, nil); code != 403 {
			t.Fatalf("%s register/begin: %d, want 403", role, code)
		}
	}
	if code, _ := env.do("POST", "/api/passkey/register/begin", "", nil); code != 403 {
		t.Fatalf("anonymous register/begin: %d, want 403", code)
	}
	if code, _ := env.do("GET", "/api/passkey", "", nil); code != 403 {
		t.Fatalf("anonymous list: %d, want 403", code)
	}
}

func TestPasskeyNotConfigured(t *testing.T) {
	env := newPKEnv(t, "", "")
	_, tok := env.record("users", "nat@example.com", "reviewer")
	if code, _ := env.do("POST", "/api/passkey/login/begin", "", nil); code != 503 {
		t.Fatalf("login/begin: %d, want 503", code)
	}
	if code, _ := env.do("POST", "/api/passkey/register/begin", tok, nil); code != 503 {
		t.Fatalf("register/begin: %d, want 503", code)
	}
	if code, b := env.do("GET", "/api/passkey", tok, nil); code != 200 || b["configured"] != false {
		t.Fatalf("list: %d %v", code, b)
	}
}

func TestLocalPasskeyDefaults(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8099": "http://localhost:8099", "localhost:8098": "http://localhost:8098", "[::1]:9000": "http://localhost:9000",
		"100.64.0.2:8097": "", "0.0.0.0:8097": "", ":8097": "",
	} {
		rp, o := localPasskeyDefaults(addr)
		if o != want || (want != "" && rp != "localhost") || (want == "" && rp != "") {
			t.Errorf("%s -> %q %q, want %q", addr, rp, o, want)
		}
	}
}
