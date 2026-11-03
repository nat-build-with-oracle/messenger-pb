// Passkey login (WebAuthn): Touch ID / Face ID through iCloud Keychain, or a hardware key.
//
//	POST   /api/passkey/register/begin                 reviewer or superuser   → {sid, options}
//	POST   /api/passkey/register/finish?sid=&name=     reviewer or superuser   body: the credential
//	POST   /api/passkey/login/begin                    anyone                  → {sid, options}
//	POST   /api/passkey/login/finish?sid=[&only=col]   anyone                  body: the assertion → {token, record}
//	GET    /api/passkey                                reviewer or superuser   my passkeys
//	DELETE /api/passkey/{id}                           reviewer or superuser   remove one of mine
//
// Env: PASSKEY_RP_ID (mac.example.ts.net) and PASSKEY_ORIGIN (https://mac.example.ts.net:8447,
// comma-separated for more). Unset = every route answers 503 and the page hides the button.
// WebAuthn needs HTTPS with a valid certificate for the RP ID; `tailscale serve` on m5 gives one.
//
// Login is discoverable: login/begin names no account and lists no credentials, the authenticator
// picks the passkey and returns its user handle ("<collection>:<record id>"). The library then
// accepts the signature only with a credential stored for that same record, so a passkey of one
// account never signs another in. User verification is required on both ceremonies: a key that
// only taps, without a fingerprint, face or PIN, is refused.
// Only a reviewer or superuser adds a passkey: those sign in by password. An agent or viewer comes
// in by a login link that expires; a passkey added from it would outlive the link.
// The password stays; a passkey is another way in, not a replacement.
// Login ends in the same response as auth-with-password ({token, record}), so the page keeps one
// sign-in path. Challenges live in memory for 5 minutes and are used once.
package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/tools/security"
)

const (
	passkeyTTL      = 5 * time.Minute // the browser prompt waits as long (webauthn default timeout)
	passkeyMaxOpen  = 200             // challenges waiting at once
	passkeyPerOwner = 10
)

func registerPasskeys(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		rpID, origins := os.Getenv("PASSKEY_RP_ID"), os.Getenv("PASSKEY_ORIGIN")
		if rpID == "" && origins == "" {
			rpID, origins = localPasskeyDefaults(se.Server.Addr)
		}
		pk, err := newPasskeys(rpID, origins)
		if err != nil {
			return err
		}
		pk.bind(se.Router)
		return se.Next()
	})
}

// localPasskeyDefaults: a server bound to the loopback address with no PASSKEY_* settings (a dev or
// mock copy) gets passkeys for http://localhost:<port>. Browsers treat http://localhost as a secure
// context and accept "localhost" as the RP ID; an IP address such as 127.0.0.1 is never a valid RP
// ID, so the page there points to localhost. Anything bound elsewhere stays unconfigured.
func localPasskeyDefaults(addr string) (rpID, origins string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "", ""
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", ""
	}
	return "localhost", "http://localhost:" + port
}

type passkeySession struct {
	data    webauthn.SessionData
	login   bool
	ownerID string // register only: who began it
	expires time.Time
}

type passkeys struct {
	wa   *webauthn.WebAuthn // nil = not configured
	mu   sync.Mutex
	open map[string]passkeySession
	now  func() time.Time
}

func newPasskeys(rpID, origins string) (*passkeys, error) {
	pk := &passkeys{open: map[string]passkeySession{}, now: time.Now}
	if rpID == "" || origins == "" {
		return pk, nil
	}
	var list []string
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			list = append(list, o)
		}
	}
	wa, err := webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: "send-queue", RPOrigins: list})
	if err != nil {
		return nil, err
	}
	pk.wa = wa
	return pk, nil
}

func (pk *passkeys) bind(r *router.Router[*core.RequestEvent]) {
	g := r.Group("/api/passkey")
	g.POST("/register/begin", pk.registerBegin)
	g.POST("/register/finish", pk.registerFinish)
	g.POST("/login/begin", pk.loginBegin)
	// Where passkeys work: the configured https origin, so a page opened over plain http (the
	// phone's own :8097) can send the user there instead of hiding the option. Public: it is
	// the same address the server already announces in every WebAuthn challenge.
	g.GET("/where", func(e *core.RequestEvent) error {
		if pk.wa == nil || len(pk.wa.Config.RPOrigins) == 0 {
			return e.JSON(http.StatusOK, map[string]any{"origin": ""})
		}
		return e.JSON(http.StatusOK, map[string]any{"origin": pk.wa.Config.RPOrigins[0]})
	})
	g.POST("/login/finish", pk.loginFinish)
	g.GET("", pk.list)
	g.DELETE("/{id}", pk.remove)
}

// --- the user the library sees -------------------------------------------------------------

type passkeyUser struct {
	rec   *core.Record
	creds []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return []byte(u.rec.Collection().Name + ":" + u.rec.Id) }
func (u *passkeyUser) WebAuthnName() string                       { return u.rec.GetString("email") }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.rec.GetString("email") }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func passkeyRows(app core.App, rec *core.Record) ([]*core.Record, error) {
	return app.FindRecordsByFilter("passkeys", "owner_col = {:c} && owner_id = {:i}", "created", 0, 0,
		dbx.Params{"c": rec.Collection().Name, "i": rec.Id})
}

func loadPasskeyUser(app core.App, rec *core.Record) (*passkeyUser, error) {
	rows, err := passkeyRows(app, rec)
	if err != nil {
		return nil, err
	}
	u := &passkeyUser{rec: rec}
	for _, row := range rows {
		var c webauthn.Credential
		if err := row.UnmarshalJSONField("credential", &c); err == nil {
			u.creds = append(u.creds, c)
		}
	}
	return u, nil
}

var b64 = base64.RawURLEncoding

// --- challenges ----------------------------------------------------------------------------

func (pk *passkeys) put(s passkeySession) (string, bool) {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	now := pk.now()
	for k, v := range pk.open {
		if now.After(v.expires) {
			delete(pk.open, k)
		}
	}
	if len(pk.open) >= passkeyMaxOpen {
		return "", false
	}
	sid := security.RandomString(32)
	s.expires = now.Add(passkeyTTL)
	s.data.Expires = s.expires
	pk.open[sid] = s
	return sid, true
}

// take removes the challenge whatever happens next: one try per challenge.
func (pk *passkeys) take(sid string, login bool) (passkeySession, bool) {
	pk.mu.Lock()
	defer pk.mu.Unlock()
	s, ok := pk.open[sid]
	delete(pk.open, sid)
	if !ok || s.login != login || pk.now().After(s.expires) {
		return passkeySession{}, false
	}
	return s, true
}

// --- handlers ------------------------------------------------------------------------------

func (pk *passkeys) ready(e *core.RequestEvent) error {
	if pk.wa == nil {
		return e.Error(http.StatusServiceUnavailable, "passkey is not configured on this server (PASSKEY_RP_ID, PASSKEY_ORIGIN)", nil)
	}
	return nil
}

func (pk *passkeys) owner(e *core.RequestEvent) (*core.Record, error) {
	if sqRole(e) != "reviewer" {
		return nil, e.ForbiddenError("sign in as reviewer or superuser", nil)
	}
	return e.Auth, nil
}

func (pk *passkeys) registerBegin(e *core.RequestEvent) error {
	if err := pk.ready(e); err != nil {
		return err
	}
	rec, err := pk.owner(e)
	if err != nil {
		return err
	}
	u, err := loadPasskeyUser(e.App, rec)
	if err != nil {
		return err
	}
	if len(u.creds) >= passkeyPerOwner {
		return e.BadRequestError("10 passkeys at most: remove one first", nil)
	}
	var exclude []protocol.CredentialDescriptor
	for i := range u.creds {
		exclude = append(exclude, u.creds[i].Descriptor())
	}
	// No attachment: platform (Touch ID, Face ID) and cross-platform (a hardware key) both welcome.
	opts, data, err := pk.wa.BeginRegistration(u,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(exclude))
	if err != nil {
		return e.InternalServerError("could not begin", err)
	}
	sid, ok := pk.put(passkeySession{data: *data, ownerID: rec.Collection().Name + ":" + rec.Id})
	if !ok {
		return e.TooManyRequestsError("too many passkey requests at once, try again in a minute", nil)
	}
	return e.JSON(http.StatusOK, map[string]any{"sid": sid, "options": opts})
}

func (pk *passkeys) registerFinish(e *core.RequestEvent) error {
	if err := pk.ready(e); err != nil {
		return err
	}
	rec, err := pk.owner(e)
	if err != nil {
		return err
	}
	s, ok := pk.take(e.Request.URL.Query().Get("sid"), false)
	if !ok || s.ownerID != rec.Collection().Name+":"+rec.Id {
		return e.BadRequestError("the request expired, start again", nil)
	}
	u, err := loadPasskeyUser(e.App, rec)
	if err != nil {
		return err
	}
	cred, err := pk.wa.FinishRegistration(u, s.data, e.Request)
	if err != nil {
		return e.BadRequestError("the passkey was not accepted", err)
	}
	col, err := e.App.FindCollectionByNameOrId("passkeys")
	if err != nil {
		return err
	}
	name := strings.TrimSpace(e.Request.URL.Query().Get("name"))
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	if name == "" {
		name = "passkey"
	}
	row := core.NewRecord(col)
	row.Set("owner_col", rec.Collection().Name)
	row.Set("owner_id", rec.Id)
	row.Set("cred_id", b64.EncodeToString(cred.ID))
	row.Set("name", name)
	row.Set("credential", cred)
	if err := e.App.Save(row); err != nil {
		return e.BadRequestError("could not save the passkey (already added?)", err)
	}
	return e.JSON(http.StatusOK, passkeyView(row))
}

func (pk *passkeys) loginBegin(e *core.RequestEvent) error {
	if err := pk.ready(e); err != nil {
		return err
	}
	opts, data, err := pk.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return e.InternalServerError("could not begin", err)
	}
	sid, ok := pk.put(passkeySession{data: *data, login: true})
	if !ok {
		return e.TooManyRequestsError("too many passkey requests at once, try again in a minute", nil)
	}
	return e.JSON(http.StatusOK, map[string]any{"sid": sid, "options": opts})
}

func (pk *passkeys) loginFinish(e *core.RequestEvent) error {
	if err := pk.ready(e); err != nil {
		return err
	}
	// One answer for every failure: it says nothing about which accounts or passkeys exist.
	fail := func(err error) error { return e.UnauthorizedError("passkey sign-in failed", err) }
	s, ok := pk.take(e.Request.URL.Query().Get("sid"), true)
	if !ok {
		return fail(nil)
	}
	var row *core.Record
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		colName, id, ok := strings.Cut(string(userHandle), ":")
		if !ok || (colName != "_superusers" && colName != "users") {
			return nil, protocol.ErrBadRequest.WithDetails("unknown user handle")
		}
		rec, err := e.App.FindRecordById(colName, id)
		if err != nil {
			return nil, err
		}
		// the stored row of this credential must belong to the record the handle names
		row, err = e.App.FindFirstRecordByFilter("passkeys", "cred_id = {:c} && owner_col = {:o} && owner_id = {:i}",
			dbx.Params{"c": b64.EncodeToString(rawID), "o": colName, "i": id})
		if err != nil {
			return nil, err
		}
		return loadPasskeyUser(e.App, rec)
	}
	// read the body once and put it back: RecordAuthResponse reads the request info again
	raw, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return fail(err)
	}
	e.Request.Body = io.NopCloser(bytes.NewReader(raw))
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		return fail(err)
	}
	user, cred, err := pk.wa.ValidatePasskeyLogin(handler, s.data, parsed)
	if err != nil || row == nil {
		return fail(err)
	}
	rec := user.(*passkeyUser).rec
	// ?only=_superusers (the admin's login page, adminpasskey.go): the signature was good, so the
	// person holds this passkey and may hear whose it is. Refused before last_used moves.
	if only := e.Request.URL.Query().Get("only"); only != "" && only != rec.Collection().Name {
		return e.ForbiddenError("this passkey belongs to a reviewer account; the admin takes superuser passkeys only", nil)
	}
	row.Set("credential", cred) // new sign count and flags
	row.Set("last_used", time.Now().UTC())
	if err := e.App.Save(row); err != nil {
		return err
	}
	return apis.RecordAuthResponse(e, rec, "passkey", nil)
}

func passkeyView(r *core.Record) map[string]any {
	return map[string]any{"id": r.Id, "name": r.GetString("name"), "created": r.GetString("created"), "last_used": r.GetString("last_used")}
}

func (pk *passkeys) list(e *core.RequestEvent) error {
	rec, err := pk.owner(e)
	if err != nil {
		return err
	}
	rows, err := passkeyRows(e.App, rec)
	if err != nil {
		return err
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, passkeyView(r))
	}
	return e.JSON(http.StatusOK, map[string]any{"configured": pk.wa != nil, "items": out})
}

func (pk *passkeys) remove(e *core.RequestEvent) error {
	rec, err := pk.owner(e)
	if err != nil {
		return err
	}
	row, err := e.App.FindFirstRecordByFilter("passkeys", "id = {:id} && owner_col = {:o} && owner_id = {:i}",
		dbx.Params{"id": e.Request.PathValue("id"), "o": rec.Collection().Name, "i": rec.Id})
	if err != nil {
		return e.NotFoundError("no such passkey of yours", nil)
	}
	if err := e.App.Delete(row); err != nil {
		return err
	}
	return e.NoContent(http.StatusNoContent)
}
